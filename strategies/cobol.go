package strategies

import (
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/provasign/astkit"
	"github.com/provasign/astkit/textmask"
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

func NewCOBOL() *cobolStrategy                        { return &cobolStrategy{} }
func (c *cobolStrategy) Language() astkit.LanguageKey { return astkit.LangCOBOL }
func (c *cobolStrategy) Extensions() []string {
	return []string{".cbl", ".cob", ".cobol", ".cpy", ".ccp", ".cpb", ".copy"}
}
func (c *cobolStrategy) ExtractsFromText() bool { return true }

// srcLine is one normalized line of code: the code-area text with its
// original 1-based line number, so every symbol cites a real location.
// COBOL lines also carry two masked views of text, byte-aligned with it:
// code (comments and literals blanked: where verbs and names are read) and
// lits (comments blanked, literals kept: where `CALL 'PROG'` targets are
// read). JCL lines leave them empty.
type srcLine struct {
	text string
	orig int
	code string
	lits string
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
	maskCOBOLLines(out)
	return out
}

// maskCOBOLLines fills each line's code and lits views. The lines are
// already column-normalized (sequence and indicator areas gone), so they
// are masked as free-format source: a `*>` inline comment is blanked in
// either source format, and `DISPLAY 'PLEASE PERFORM BACKUP'` hides its
// PERFORM. A continued literal is joined with a space, so its tail after the
// joint can read as code; that only loses precision on such lines.
func maskCOBOLLines(lines []srcLine) {
	if len(lines) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString(">>SOURCE FORMAT FREE")
	for _, ln := range lines {
		b.WriteByte('\n')
		b.WriteString(strings.ReplaceAll(ln.text, "\r", " "))
	}
	joined := b.String()
	code := strings.Split(textmask.Mask("cobol", joined), "\n")[1:]
	lits := strings.Split(textmask.MaskComments("cobol", joined), "\n")[1:]
	for i := range lines {
		lines[i].code, lines[i].lits = code[i], lits[i]
	}
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
	reProgramID = regexp.MustCompile(`(?i)^\s*PROGRAM-ID\s*[.]?\s+([A-Za-z0-9][A-Za-z0-9-]*)`)
	reDivision  = regexp.MustCompile(`(?i)^\s*(IDENTIFICATION|ENVIRONMENT|DATA|PROCEDURE)\s+DIVISION`)
	reDataItem  = regexp.MustCompile(`(?i)^\s*(\d{1,2})\s+([A-Za-z0-9][A-Za-z0-9-]*)(.*)$`)
	rePicture   = regexp.MustCompile(`(?i)\bPIC(?:TURE)?\s+(?:IS\s+)?([^\s.]+)`)
	reRedefines = regexp.MustCompile(`(?i)\bREDEFINES\s+([A-Za-z0-9-]+)`)
	reOccurs    = regexp.MustCompile(`(?i)\bOCCURS\s+(\d+)`)
	reSection   = regexp.MustCompile(`(?i)^\s*([A-Za-z0-9][A-Za-z0-9-]*)\s+SECTION\s*\.`)
	reParagraph = regexp.MustCompile(`^\s{0,3}([A-Za-z0-9][A-Za-z0-9-]*)\s*\.\s*$`)
	reFD        = regexp.MustCompile(`(?i)^\s*FD\s+([A-Za-z0-9-]+)`)
	reSelect    = regexp.MustCompile(`(?i)\bSELECT\s+(?:OPTIONAL\s+)?([A-Za-z0-9-]+)\s+ASSIGN\s+TO\s+([A-Za-z0-9-]+)`)
	rePerform   = regexp.MustCompile(`(?i)\bPERFORM\s+([A-Za-z0-9][A-Za-z0-9-]*)(?:\s+(?:THRU|THROUGH)\s+([A-Za-z0-9-]+))?`)
	reCallVerb  = regexp.MustCompile(`(?i)\bCALL\s`)
	reCallLit   = regexp.MustCompile(`(?i)^CALL\s+['"]([^'"]+)['"]`)
	reCallVar   = regexp.MustCompile(`(?i)^CALL\s+([A-Za-z][A-Za-z0-9-]*)`)
	// Member names cannot start with a digit (PDS naming): a leading-digit
	// match is a sequence number or numeric operand, not a member
	// (field-reported: 68 numeric "members" like 053300).
	reCopy     = regexp.MustCompile(`(?i)\bCOPY\s+([A-Za-z@#$][A-Za-z0-9@#$-]*)(?:\s+(?:OF|IN)\s+([A-Za-z0-9-]+))?`)
	reReserved = regexp.MustCompile(`(?i)^(EXIT|STOP|GOBACK|END|ELSE|WHEN|UNTIL|VARYING|TIMES|WITH|TEST|THRU|THROUGH|FROM|BY|GIVING)$`)
)

func (c *cobolStrategy) Extract(tree *sitter.Tree, src []byte) ([]astkit.Symbol, error) {
	_ = tree // text strategy: tree is nil by design
	normalized := normalizeCOBOL(src)
	fileEnd := 0
	if len(normalized) > 0 {
		fileEnd = normalized[len(normalized)-1].orig
	}
	lines := joinCOBOLDataClauses(normalized)

	var syms []astkit.Symbol
	var programName string
	division := "DATA" // copybooks have no division header; default to DATA
	// levelStack holds (level, name) of open group items for hierarchy.
	type lvl struct {
		level int
		name  string
	}
	var stack []lvl
	var lastItem string            // most recent data item; 88-levels bind to it
	var currentProc *astkit.Symbol // paragraph/section receiving call sites
	progIndex := -1
	type performRange struct {
		caller, first, last string
		line                int
	}
	var performRanges []performRange

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
		// Structure and verbs are matched on ln.code (comments and literals
		// blanked); signatures keep the line minus its comments (ln.lits).
		if m := reDivision.FindStringSubmatch(ln.code); m != nil {
			division = strings.ToUpper(m[1])
			continue
		}
		if m := reProgramID.FindStringSubmatch(ln.lits); m != nil {
			flushProc()
			name := strings.TrimSuffix(m[1], ".")
			s := astkit.Symbol{
				Kind: kindProgram, Name: name, QualifiedName: name,
				Signature: strings.TrimSpace(ln.lits),
				Span:      astkit.LineRange{Start: ln.orig, End: ln.orig},
				Exported:  true,
			}
			syms = append(syms, s)
			progIndex = len(syms) - 1
			programName = name
			continue
		}

		switch division {
		case "ENVIRONMENT":
			if m := reSelect.FindStringSubmatch(ln.code); m != nil {
				syms = append(syms, astkit.Symbol{
					Kind: kindLogicalFile, Name: m[1],
					QualifiedName: m[1], ParentName: programName,
					Signature: strings.TrimSpace(ln.lits),
					Span:      astkit.LineRange{Start: ln.orig, End: ln.orig},
				})
			}
		case "DATA":
			if m := reFD.FindStringSubmatch(ln.code); m != nil {
				stack = stack[:0]
				continue
			}
			if m := reDataItem.FindStringSubmatch(ln.code); m != nil {
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
					// RENAMES is an alternate record view, never a child of the
					// elementary item that happened to precede it.
					stack = stack[:0]
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
				sig := strings.TrimSpace(strings.TrimSuffix(ln.lits, "."))
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
			if m := reSection.FindStringSubmatch(ln.code); m != nil {
				flushProc()
				name := m[1]
				s := astkit.Symbol{
					Kind: kindSection, Name: name, QualifiedName: name,
					ParentName: programName,
					Signature:  strings.TrimSpace(ln.lits),
					Span:       astkit.LineRange{Start: ln.orig, End: ln.orig},
					Body:       strings.TrimSpace(ln.text),
				}
				currentProc = &s
				continue
			}
			if m := reParagraph.FindStringSubmatch(ln.code); m != nil && !reReserved.MatchString(m[1]) {
				flushProc()
				name := m[1]
				s := astkit.Symbol{
					Kind: kindParagraph, Name: name, QualifiedName: name,
					ParentName: programName,
					Signature:  strings.TrimSpace(ln.lits),
					Span:       astkit.LineRange{Start: ln.orig, End: ln.orig},
					Body:       strings.TrimSpace(ln.text),
				}
				currentProc = &s
				continue
			}
			target := currentProc
			if target == nil && progIndex >= 0 {
				target = &syms[progIndex]
			}
			if target != nil {
				for _, pm := range rePerform.FindAllStringSubmatch(ln.code, -1) {
					if !reReserved.MatchString(pm[1]) {
						target.CallSites = append(target.CallSites, astkit.CallSite{Callee: pm[1], Line: ln.orig})
					}
					if pm[2] != "" && !reReserved.MatchString(pm[2]) {
						target.CallSites = append(target.CallSites, astkit.CallSite{Callee: pm[2], Line: ln.orig})
						performRanges = append(performRanges, performRange{target.QualifiedName, pm[1], pm[2], ln.orig})
					}
				}
				// The CALL verb is found in code; its literal target is read
				// at the same offset in lits, where the literal survives.
				if loc := reCallVerb.FindStringIndex(ln.code); loc != nil {
					if cm := reCallLit.FindStringSubmatch(ln.lits[loc[0]:]); cm != nil {
						target.CallSites = append(target.CallSites, astkit.CallSite{Callee: cm[1], Line: ln.orig})
					} else if cm := reCallVar.FindStringSubmatch(ln.code[loc[0]:]); cm != nil && !strings.EqualFold(cm[1], "FUNCTION") {
						// Dynamic call through a variable: record the variable name
						// so the edge exists as a known-unknown rather than vanishing.
						target.CallSites = append(target.CallSites, astkit.CallSite{
							Callee: cm[1], Line: ln.orig, Args: []string{"dynamic"},
						})
					}
				}
			}
		}
		// span growth + body accumulation for the enclosing procedure —
		// Body carries the normalized statement text so graph consumers can
		// resolve field references without re-normalizing the file.
		if currentProc != nil && division == "PROCEDURE" {
			for currentProc.Span.End < ln.orig {
				currentProc.Body += "\n"
				currentProc.Span.End++
			}
			currentProc.Body += strings.TrimSpace(ln.text)
		}
	}
	flushProc()
	if progIndex >= 0 && fileEnd > 0 {
		syms[progIndex].Span.End = fileEnd
	}
	paragraphOrder := map[string]int{}
	for idx := range syms {
		if syms[idx].Kind == kindParagraph || syms[idx].Kind == kindSection {
			paragraphOrder[strings.ToUpper(syms[idx].Name)] = idx
		}
	}
	for _, span := range performRanges {
		first, firstOK := paragraphOrder[strings.ToUpper(span.first)]
		last, lastOK := paragraphOrder[strings.ToUpper(span.last)]
		if !firstOK || !lastOK || first >= last {
			continue
		}
		caller := -1
		for idx := range syms {
			if syms[idx].QualifiedName == span.caller {
				caller = idx
				break
			}
		}
		if caller < 0 {
			continue
		}
		for idx := first + 1; idx < last; idx++ {
			if syms[idx].Kind == kindParagraph || syms[idx].Kind == kindSection {
				syms[caller].CallSites = append(syms[caller].CallSites, astkit.CallSite{Callee: syms[idx].Name, Line: span.line})
			}
		}
	}
	return syms, nil
}

// cobolTrimLike trims masked the way strings.TrimSpace trims text (both are
// byte-aligned), so a joined line's views stay aligned with its text.
func cobolTrimLike(text, masked string) string {
	lead := len(text) - len(strings.TrimLeft(text, " \t\r\n"))
	trail := len(strings.TrimRight(text, " \t\r\n"))
	if trail < lead {
		return ""
	}
	return masked[lead:trail]
}

func joinCOBOLDataClauses(lines []srcLine) []srcLine {
	out := make([]srcLine, 0, len(lines))
	division := "DATA"
	for idx := 0; idx < len(lines); idx++ {
		line := lines[idx]
		if match := reDivision.FindStringSubmatch(line.text); match != nil {
			division = strings.ToUpper(match[1])
			out = append(out, line)
			continue
		}
		if division != "DATA" || reDataItem.FindStringSubmatch(line.text) == nil || strings.HasSuffix(strings.TrimSpace(line.text), ".") {
			out = append(out, line)
			continue
		}
		for idx+1 < len(lines) {
			next := lines[idx+1]
			if reDivision.MatchString(next.text) || reDataItem.MatchString(next.text) {
				break
			}
			line.text += " " + strings.TrimSpace(next.text)
			line.code += " " + cobolTrimLike(next.text, next.code)
			line.lits += " " + cobolTrimLike(next.text, next.lits)
			idx++
			if strings.HasSuffix(strings.TrimSpace(next.text), ".") {
				break
			}
		}
		out = append(out, line)
	}
	return out
}

var (
	// Pseudo-text (==...==) and quoted literals must be erased before member
	// extraction: REPLACING arguments legally contain '&', '#' and even the
	// word COPY, and a member name harvested from them is confidently wrong
	// (observed on a real estate: 54 members extracted from REPLACING args).
	// Literals and comments are already blanked in srcLine.code.
	rePseudoText = regexp.MustCompile(`==[^=]*(?:=[^=]+)*==`)
	reCopyTail   = regexp.MustCompile(`(?i)\bCOPY\s*$`)
	reMemberHead = regexp.MustCompile(`^\s*([A-Za-z@#$][A-Za-z0-9@#$-]*)`)
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
		clean := rePseudoText.ReplaceAllString(ln.code, " ")
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
