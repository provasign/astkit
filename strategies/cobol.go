package strategies

import (
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/provasign/astkit"
)

// COBOL symbol kinds. Consumers treat unknown kinds as "other", so these are
// additive and invisible to modern-language paths.
const (
	kindProgram       astkit.SymbolKind = "program"
	kindDataItem      astkit.SymbolKind = "data-item"
	kindConditionName astkit.SymbolKind = "condition-name"
	kindParagraph     astkit.SymbolKind = "paragraph"
	kindSection       astkit.SymbolKind = "section"
	kindLogicalFile   astkit.SymbolKind = "logical-file"
)

// cobolStrategy extracts COBOL programs and copybooks without a grammar.
// It is deliberately line-structured: the DATA DIVISION is line-shaped
// (level number, name, clauses), and that is where most of the value is.
// PROCEDURE DIVISION coverage is paragraphs/sections plus PERFORM/CALL/COPY
// references — reachability, not read/write direction (that needs an AST
// and arrives in a later phase).
type cobolStrategy struct{}

func NewCOBOL() *cobolStrategy                       { return &cobolStrategy{} }
func (c *cobolStrategy) Language() astkit.LanguageKey { return astkit.LangCOBOL }
func (c *cobolStrategy) Extensions() []string {
	return []string{".cbl", ".cob", ".cobol", ".cpy", ".ccp", ".cpb"}
}
func (c *cobolStrategy) ExtractsFromText() bool { return true }

// srcLine is one normalized line of code: the code-area text with its
// original 1-based line number, so every symbol cites a real location.
type srcLine struct {
	text string
	orig int
}

// normalizeCOBOL applies the column model ahead of extraction. Fixed-format
// lines carry a sequence area (cols 1-6), an indicator (col 7: '*' or '/'
// comment, '-' continuation, 'D' debug), code in cols 8-72, and an
// identification area (73-80) that is NOT code. Feeding those regions to an
// extractor produces confident nonsense (measured: a 19x item-count swing
// from column mishandling alone). Format is detected per file: a line is
// fixed-shaped when cols 1-6 are blank or digits and col 7 is a known
// indicator or space; majority vote over non-blank lines decides.
func normalizeCOBOL(src []byte) []srcLine {
	raw := strings.Split(string(src), "\n")
	fixed := detectFixedFormat(raw)
	out := make([]srcLine, 0, len(raw))
	for i, line := range raw {
		line = strings.TrimRight(line, "\r")
		var code string
		var continuation bool
		if fixed {
			if len(line) < 8 {
				continue
			}
			switch line[6] {
			case '*', '/':
				continue // comment
			case 'D', 'd':
				continue // debug line; all-branches indexing is a later phase
			case '-':
				continuation = true
			}
			end := len(line)
			if end > 72 {
				end = 72 // identification area 73-80 is not code
			}
			code = line[7:end]
		} else {
			code = line
			if idx := strings.Index(code, "*>"); idx >= 0 {
				code = code[:idx]
			}
			trimmed := strings.TrimSpace(code)
			if strings.HasPrefix(trimmed, "*") {
				continue
			}
		}
		if strings.TrimSpace(code) == "" {
			continue
		}
		if continuation && len(out) > 0 {
			out[len(out)-1].text += " " + strings.TrimSpace(code)
			continue
		}
		out = append(out, srcLine{text: code, orig: i + 1})
	}
	return out
}

func detectFixedFormat(lines []string) bool {
	fixedShaped, sampled := 0, 0
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		sampled++
		if sampled > 50 {
			break
		}
		// The sequence area (cols 1-6) may contain ANYTHING — the compiler
		// ignores it, and estates commonly stamp the member name there
		// (measured: such copybooks extracted ZERO symbols when the digits
		// requirement misdetected them as free format, silently breaking
		// member resolution for the whole estate). Only the indicator
		// column decides.
		if len(line) >= 7 && strings.ContainsRune(" *-/Dd", rune(line[6])) {
			fixedShaped++
		}
	}
	if sampled > 50 {
		sampled = 50
	}
	return sampled > 0 && fixedShaped*5 >= sampled*4 // >= 80%
}

var (
	reProgramID  = regexp.MustCompile(`(?i)^\s*PROGRAM-ID\s*[.]?\s+([A-Za-z0-9][A-Za-z0-9-]*)`)
	reDivision   = regexp.MustCompile(`(?i)^\s*(IDENTIFICATION|ENVIRONMENT|DATA|PROCEDURE)\s+DIVISION`)
	reDataItem   = regexp.MustCompile(`(?i)^\s*(\d{1,2})\s+([A-Za-z0-9][A-Za-z0-9-]*)(.*)$`)
	rePicture    = regexp.MustCompile(`(?i)\bPIC(?:TURE)?\s+(?:IS\s+)?([^\s.]+)`)
	reRedefines  = regexp.MustCompile(`(?i)\bREDEFINES\s+([A-Za-z0-9-]+)`)
	reOccurs     = regexp.MustCompile(`(?i)\bOCCURS\s+(\d+)`)
	reSection    = regexp.MustCompile(`(?i)^\s*([A-Za-z0-9][A-Za-z0-9-]*)\s+SECTION\s*\.`)
	reParagraph  = regexp.MustCompile(`^\s{0,3}([A-Za-z0-9][A-Za-z0-9-]*)\s*\.\s*$`)
	reFD         = regexp.MustCompile(`(?i)^\s*FD\s+([A-Za-z0-9-]+)`)
	reSelect     = regexp.MustCompile(`(?i)\bSELECT\s+(?:OPTIONAL\s+)?([A-Za-z0-9-]+)\s+ASSIGN\s+TO\s+([A-Za-z0-9-]+)`)
	rePerform    = regexp.MustCompile(`(?i)\bPERFORM\s+([A-Za-z0-9][A-Za-z0-9-]*)(?:\s+(?:THRU|THROUGH)\s+([A-Za-z0-9-]+))?`)
	reCallLit    = regexp.MustCompile(`(?i)\bCALL\s+['"]([^'"]+)['"]`)
	reCallVar    = regexp.MustCompile(`(?i)\bCALL\s+([A-Za-z][A-Za-z0-9-]*)`)
	reCopy       = regexp.MustCompile(`(?i)\bCOPY\s+([A-Za-z0-9][A-Za-z0-9-]*)(?:\s+(?:OF|IN)\s+([A-Za-z0-9-]+))?`)
	reReserved   = regexp.MustCompile(`(?i)^(EXIT|STOP|GOBACK|END|ELSE|WHEN|UNTIL|VARYING|TIMES)$`)
)

func (c *cobolStrategy) Extract(tree *sitter.Tree, src []byte) ([]astkit.Symbol, error) {
	_ = tree // text strategy: tree is nil by design
	lines := normalizeCOBOL(src)

	var syms []astkit.Symbol
	var programName string
	division := "DATA" // copybooks have no division header; default to DATA
	// levelStack holds (level, name) of open group items for hierarchy.
	type lvl struct {
		level int
		name  string
	}
	var stack []lvl
	var lastItem string // most recent data item; 88-levels bind to it
	var currentProc *astkit.Symbol // paragraph/section receiving call sites
	var progSym *astkit.Symbol

	qualify := func() string {
		parts := make([]string, len(stack))
		for i, l := range stack {
			parts[i] = l.name
		}
		return strings.Join(parts, ".")
	}

	flushProc := func() {
		if currentProc != nil {
			syms = append(syms, *currentProc)
			currentProc = nil
		}
	}

	for _, ln := range lines {
		if m := reDivision.FindStringSubmatch(ln.text); m != nil {
			division = strings.ToUpper(m[1])
			continue
		}
		if m := reProgramID.FindStringSubmatch(ln.text); m != nil {
			flushProc()
			name := strings.TrimSuffix(m[1], ".")
			s := astkit.Symbol{
				Kind: kindProgram, Name: name, QualifiedName: name,
				Signature: strings.TrimSpace(ln.text),
				Span:      astkit.LineRange{Start: ln.orig, End: ln.orig},
				Exported:  true,
			}
			syms = append(syms, s)
			progSym = &syms[len(syms)-1]
			programName = name
			continue
		}

		switch division {
		case "ENVIRONMENT":
			if m := reSelect.FindStringSubmatch(ln.text); m != nil {
				syms = append(syms, astkit.Symbol{
					Kind: kindLogicalFile, Name: m[1],
					QualifiedName: m[1], ParentName: programName,
					Signature: strings.TrimSpace(ln.text),
					Span:      astkit.LineRange{Start: ln.orig, End: ln.orig},
				})
			}
		case "DATA":
			if m := reFD.FindStringSubmatch(ln.text); m != nil {
				stack = stack[:0]
				continue
			}
			if m := reDataItem.FindStringSubmatch(ln.text); m != nil {
				level := parseLevel(m[1])
				name := m[2]
				rest := m[3]
				if level == 0 || strings.EqualFold(name, "FILLER") || reReserved.MatchString(name) {
					continue
				}
				kind := kindDataItem
				switch level {
				case 88:
					kind = kindConditionName
				case 66:
					// RENAMES alternate view; keep as data item with clause in signature
				case 77:
					stack = stack[:0]
				default:
					for len(stack) > 0 && stack[len(stack)-1].level >= level {
						stack = stack[:len(stack)-1]
					}
				}
				parent := ""
				if len(stack) > 0 {
					parent = stack[len(stack)-1].name
				}
				qn := name
				if q := qualify(); q != "" && level != 77 {
					qn = q + "." + name
				}
				if level == 88 {
					// A condition name binds to the item declared just above
					// it, not to the enclosing group.
					parent = lastItem
					if lastItem != "" {
						qn = lastItem + "." + name
					}
				}
				sig := strings.TrimSpace(strings.TrimSuffix(ln.text, "."))
				sym := astkit.Symbol{
					Kind: kind, Name: name, QualifiedName: qn, ParentName: parent,
					Signature: sig,
					Span:      astkit.LineRange{Start: ln.orig, End: ln.orig},
				}
				if pm := rePicture.FindStringSubmatch(rest); pm != nil {
					sym.Modifiers = append(sym.Modifiers, "pic:"+pm[1])
				}
				if rm := reRedefines.FindStringSubmatch(rest); rm != nil {
					sym.Modifiers = append(sym.Modifiers, "redefines:"+strings.ToUpper(rm[1]))
				}
				if om := reOccurs.FindStringSubmatch(rest); om != nil {
					sym.Modifiers = append(sym.Modifiers, "occurs:"+om[1])
				}
				syms = append(syms, sym)
				if kind == kindDataItem {
					lastItem = name
				}
				if kind == kindDataItem && level != 77 && level != 66 && rePicture.FindStringSubmatch(rest) == nil {
					// group item: open a hierarchy scope
					stack = append(stack, lvl{level: level, name: name})
				}
				continue
			}
		case "PROCEDURE":
			if m := reSection.FindStringSubmatch(ln.text); m != nil {
				flushProc()
				name := m[1]
				s := astkit.Symbol{
					Kind: kindSection, Name: name, QualifiedName: name,
					ParentName: programName,
					Signature:  strings.TrimSpace(ln.text),
					Span:       astkit.LineRange{Start: ln.orig, End: ln.orig},
				}
				currentProc = &s
				continue
			}
			if m := reParagraph.FindStringSubmatch(ln.text); m != nil && !reReserved.MatchString(m[1]) {
				flushProc()
				name := m[1]
				s := astkit.Symbol{
					Kind: kindParagraph, Name: name, QualifiedName: name,
					ParentName: programName,
					Signature:  strings.TrimSpace(ln.text),
					Span:       astkit.LineRange{Start: ln.orig, End: ln.orig},
				}
				currentProc = &s
				continue
			}
			target := currentProc
			if target == nil {
				target = progSym
			}
			if target != nil {
				for _, pm := range rePerform.FindAllStringSubmatch(ln.text, -1) {
					if !reReserved.MatchString(pm[1]) {
						target.CallSites = append(target.CallSites, astkit.CallSite{Callee: pm[1], Line: ln.orig})
					}
					if pm[2] != "" && !reReserved.MatchString(pm[2]) {
						target.CallSites = append(target.CallSites, astkit.CallSite{Callee: pm[2], Line: ln.orig})
					}
				}
				if cm := reCallLit.FindStringSubmatch(ln.text); cm != nil {
					target.CallSites = append(target.CallSites, astkit.CallSite{Callee: cm[1], Line: ln.orig})
				} else if cm := reCallVar.FindStringSubmatch(ln.text); cm != nil && !strings.EqualFold(cm[1], "FUNCTION") {
					// Dynamic call through a variable: record the variable name
					// so the edge exists as a known-unknown rather than vanishing.
					target.CallSites = append(target.CallSites, astkit.CallSite{
						Callee: cm[1], Line: ln.orig, Args: []string{"dynamic"},
					})
				}
			}
		}
		// span growth + body accumulation for the enclosing procedure —
		// Body carries the normalized statement text so graph consumers can
		// resolve field references without re-normalizing the file.
		if currentProc != nil && division == "PROCEDURE" {
			if ln.orig > currentProc.Span.End {
				currentProc.Span.End = ln.orig
			}
			if currentProc.Body != "" {
				currentProc.Body += "\n"
			}
			currentProc.Body += strings.TrimSpace(ln.text)
		}
	}
	flushProc()
	if progSym != nil && len(lines) > 0 {
		progSym.Span.End = lines[len(lines)-1].orig
	}
	return syms, nil
}

var (
	// Pseudo-text (==...==) and quoted literals must be erased before member
	// extraction: REPLACING arguments legally contain '&', '#' and even the
	// word COPY, and a member name harvested from them is confidently wrong
	// (observed on a real estate: 54 members extracted from REPLACING args).
	rePseudoText = regexp.MustCompile(`==[^=]*(?:=[^=]+)*==`)
	reQuotedLit  = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	reCopyTail   = regexp.MustCompile(`(?i)\bCOPY\s*$`)
	reMemberHead = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9-]*)`)
)

func (c *cobolStrategy) ExtractImports(tree *sitter.Tree, src []byte) ([]astkit.ImportStatement, error) {
	_ = tree
	var imports []astkit.ImportStatement
	emit := func(member, raw string, line int) {
		if member == "" || reReserved.MatchString(member) {
			return
		}
		imports = append(imports, astkit.ImportStatement{
			Raw:   strings.TrimSpace(raw),
			Path:  strings.ToUpper(member),
			Group: "copybook",
			Line:  line,
		})
	}
	pendingCopy := false // previous line ended at COPY; member starts this line
	for _, ln := range normalizeCOBOL(src) {
		clean := rePseudoText.ReplaceAllString(ln.text, " ")
		clean = reQuotedLit.ReplaceAllString(clean, " ")
		if pendingCopy {
			if m := reMemberHead.FindStringSubmatch(clean); m != nil {
				emit(m[1], ln.text, ln.orig)
			}
			pendingCopy = false
		}
		for _, m := range reCopy.FindAllStringSubmatch(clean, -1) {
			emit(m[1], ln.text, ln.orig)
		}
		if reCopyTail.MatchString(clean) {
			pendingCopy = true
		}
	}
	return imports, nil
}

func parseLevel(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	if n < 1 || n > 88 {
		return 0
	}
	return n
}
