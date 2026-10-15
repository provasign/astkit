package strategies

import (
	"strings"
)

// csAlternateInactiveLines returns the 1-based lines that sit in a losing
// branch of a C# `#if ... #elif/#else ... #endif` group, evaluated with no
// conditional symbols defined (what the compiler sees without
// DefineConstants). Only groups with an alternative are decided: a file
// that writes
//
//	#if NET20
//	using Newtonsoft.Json.Utilities.LinqBridge;
//	#else
//	using System.Linq;
//	#endif
//
// imports System.Linq, not both. A lone `#if X` with no #else stays live:
// without the build's symbols, keeping it is the conservative reading.
func csAlternateInactiveLines(src []byte) map[int]bool {
	if !strings.Contains(string(src), "#else") && !strings.Contains(string(src), "#elif") {
		return nil
	}
	type branch struct {
		start, end int // lines strictly inside the branch
		cond       string
		isElse     bool
	}
	type group struct{ branches []branch }
	var stack []*group
	var groups []*group
	lines := strings.Split(string(src), "\n")
	for i, raw := range lines {
		ln := i + 1
		t := strings.TrimSpace(raw)
		if !strings.HasPrefix(t, "#") {
			continue
		}
		t = strings.TrimSpace(t[1:])
		word, rest, _ := strings.Cut(t, " ")
		rest = strings.TrimSpace(rest)
		if j := strings.Index(rest, "//"); j >= 0 {
			rest = strings.TrimSpace(rest[:j])
		}
		switch word {
		case "if":
			g := &group{branches: []branch{{start: ln + 1, cond: rest}}}
			stack = append(stack, g)
			groups = append(groups, g)
		case "elif", "else":
			if len(stack) == 0 {
				continue
			}
			g := stack[len(stack)-1]
			g.branches[len(g.branches)-1].end = ln - 1
			g.branches = append(g.branches, branch{start: ln + 1, cond: rest, isElse: word == "else"})
		case "endif":
			if len(stack) == 0 {
				continue
			}
			g := stack[len(stack)-1]
			g.branches[len(g.branches)-1].end = ln - 1
			stack = stack[:len(stack)-1]
		}
	}
	out := map[int]bool{}
	for _, g := range groups {
		if len(g.branches) < 2 || g.branches[len(g.branches)-1].end == 0 {
			continue // no alternative, or unterminated
		}
		active := -1
		for i, b := range g.branches {
			if b.isElse {
				active = i
				break
			}
			v, ok := csEvalCondition(b.cond)
			if !ok {
				active = -2 // an expression we cannot read: decide nothing
				break
			}
			if v {
				active = i
				break
			}
		}
		if active == -2 {
			continue
		}
		for i, b := range g.branches {
			if i == active {
				continue
			}
			for ln := b.start; ln <= b.end; ln++ {
				out[ln] = true
			}
		}
	}
	return out
}

// csEvalCondition evaluates a C# preprocessor expression with every symbol
// undefined: identifiers are false, true/false are literals, and !, &&, ||,
// ==, != and parentheses combine them. ok is false on anything else.
func csEvalCondition(expr string) (val, ok bool) {
	var toks []string
	for i := 0; i < len(expr); {
		c := expr[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case strings.HasPrefix(expr[i:], "&&"), strings.HasPrefix(expr[i:], "||"),
			strings.HasPrefix(expr[i:], "=="), strings.HasPrefix(expr[i:], "!="):
			toks = append(toks, expr[i:i+2])
			i += 2
		case c == '!' || c == '(' || c == ')':
			toks = append(toks, string(c))
			i++
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(expr) && (expr[j] == '_' || expr[j] >= 'a' && expr[j] <= 'z' || expr[j] >= 'A' && expr[j] <= 'Z' || expr[j] >= '0' && expr[j] <= '9') {
				j++
			}
			toks = append(toks, expr[i:j])
			i = j
		default:
			return false, false
		}
	}
	p := &csCondParser{toks: toks}
	v := p.or()
	if p.bad || p.pos != len(toks) {
		return false, false
	}
	return v, true
}

type csCondParser struct {
	toks []string
	pos  int
	bad  bool
}

func (p *csCondParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *csCondParser) prim() bool {
	t := p.peek()
	p.pos++
	switch t {
	case "!":
		return !p.prim()
	case "(":
		v := p.or()
		if p.peek() != ")" {
			p.bad = true
		}
		p.pos++
		return v
	case "true":
		return true
	case "", "&&", "||", "==", "!=", ")":
		p.bad = true
	}
	return false // `false` or an undefined symbol
}

func (p *csCondParser) eq() bool {
	v := p.prim()
	for p.peek() == "==" || p.peek() == "!=" {
		op := p.peek()
		p.pos++
		w := p.prim()
		v = (v == w) == (op == "==")
	}
	return v
}

func (p *csCondParser) and() bool {
	v := p.eq()
	for p.peek() == "&&" {
		p.pos++
		w := p.eq()
		v = v && w
	}
	return v
}

func (p *csCondParser) or() bool {
	v := p.and()
	for p.peek() == "||" {
		p.pos++
		w := p.and()
		v = v || w
	}
	return v
}
