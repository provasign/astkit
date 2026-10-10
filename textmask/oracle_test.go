package textmask

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/swift"
	tstsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	tstype "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/provasign/astkit/thirdparty/tsobjc"
)

// The oracle: tree-sitter's comment and literal nodes say which bytes are
// prose or literal text. Mask must blank every non-space byte inside them
// (minus interpolation holes, which are code) and no non-space byte outside.

func grammar(lang string) *sitter.Language {
	switch lang {
	case "go":
		return golang.GetLanguage()
	case "typescript":
		return tstype.GetLanguage()
	case "tsx":
		return tstsx.GetLanguage()
	case "javascript":
		return javascript.GetLanguage()
	case "python":
		return python.GetLanguage()
	case "java":
		return java.GetLanguage()
	case "rust":
		return rust.GetLanguage()
	case "c":
		return c.GetLanguage()
	case "cpp":
		return cpp.GetLanguage()
	case "csharp":
		return csharp.GetLanguage()
	case "php":
		return php.GetLanguage()
	case "swift":
		return swift.GetLanguage()
	case "kotlin":
		return kotlin.GetLanguage()
	case "objc":
		return tsobjc.GetLanguage()
	}
	return nil
}

// langOfPath maps a file extension to a language key.
func langOfPath(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".go":
		return "go"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	case ".c":
		return "c"
	case ".h":
		return "cpp"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return "cpp"
	case ".m", ".mm":
		return "objc"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".cbl", ".cob", ".cpy":
		return "cobol"
	case ".jcl":
		return "jcl"
	}
	return ""
}

// Node classes. A node type is looked up in these sets; anything else is
// walked into.
var (
	// literalTypes: every byte is literal text, except interpolation children.
	literalTypes = map[string]bool{
		"string": true, "string_literal": true, "raw_string_literal": true,
		"char_literal": true, "character_literal": true,
		"interpreted_string_literal": true, "rune_literal": true,
		"template_string": true, "regex": true,
		"encapsed_string": true, "heredoc": true, "nowdoc": true, "heredoc_body": true,
		"shell_command_expression": true,
		"verbatim_string_literal":  true, "interpolated_string_expression": true,
		"interpolated_verbatim_string_text": true, "interpolated_raw_string_text": true,
		"raw_string_start": true, "raw_string_end": true, "raw_string_content": true,
		"text_block": true, "line_string_literal": true,
		"multi_line_string_literal": true, "raw_string_literal_content": true,
		"string_content":        true,
		"text":                  true, // PHP inline HTML
		"template_literal_type": true, "hash_bang_line": true,
	}
	// holeTypes: interpolation children inside literals; they are code.
	holeTypes = map[string]bool{
		"interpolation": true, "template_substitution": true,
		"string_interpolation": true, "interpolated_expression": true,
		"interpolated_identifier": true, "raw_str_interpolation": true,
		"variable_name": true, "member_access_expression": true,
		"subscript_expression": true, "dynamic_variable_name": true,
		"simple_expression": true, "template_type": true,
	}
	// dontCareTypes: either masked or kept is acceptable.
	dontCareTypes = map[string]bool{
		"format_specifier": true, "interpolation_format_clause": true,
		"preproc_arg": true,
		// Swift `#error("...")` and `#if ... // c`: tree-sitter swallows the
		// string and the trailing comment into the directive token.
		"diagnostic": true, "directive": true,
	}
)

// phpLitParents are PHP literal nodes whose named children are either text
// parts or interpolated expressions (holes) of any expression type.
var phpLitParents = map[string]bool{
	"encapsed_string": true, "heredoc_body": true, "shell_command_expression": true,
}

var textParts = map[string]bool{
	"string_content": true, "string_value": true, "escape_sequence": true,
	"heredoc_start": true, "heredoc_end": true, "nowdoc_body": true,
	"heredoc_body": true, "string": true, "text": true,
}

// isHole reports whether child ch of a literal node of type parent is an
// interpolation hole.
func isHole(parent string, ch *sitter.Node) bool {
	if holeTypes[ch.Type()] {
		return true
	}
	return phpLitParents[parent] && ch.IsNamed() && !textParts[ch.Type()]
}

// expectations builds the per-byte oracle: 0 code, 1 masked, 2 don't care.
// Nodes only reachable through a hole of PHP's grammar (variable_name etc.)
// are holes only when they are children of a literal.
func expectations(root *sitter.Node, n int) []byte {
	exp := make([]byte, n)
	fill := func(nd *sitter.Node, v byte) {
		a, b := int(nd.StartByte()), int(nd.EndByte())
		if b > n {
			b = n
		}
		for k := a; k < b; k++ {
			exp[k] = v
		}
	}
	var walk func(nd *sitter.Node, inLit bool)
	// walkLit visits a literal's children: holes are code (walked afresh),
	// nested literal parts stay masked.
	walkLit := func(nd *sitter.Node) {
		for k := 0; k < int(nd.ChildCount()); k++ {
			ch := nd.Child(k)
			if isHole(nd.Type(), ch) {
				fill(ch, 0)
				for j := 0; j < int(ch.ChildCount()); j++ {
					walk(ch.Child(j), false)
				}
				continue
			}
			walk(ch, true)
		}
	}
	walk = func(nd *sitter.Node, inLit bool) {
		t := nd.Type()
		switch {
		case strings.Contains(t, "comment") && nd.IsNamed():
			fill(nd, 1)
			return
		case dontCareTypes[t]:
			fill(nd, 2)
			return
		case literalTypes[t] && nd.IsNamed():
			fill(nd, 1)
			walkLit(nd)
			return
		}
		for k := 0; k < int(nd.ChildCount()); k++ {
			walk(nd.Child(k), inLit)
		}
	}
	walk(root, false)
	return exp
}

// holePunct relaxes the oracle for interpolation delimiters: grammars
// disagree on whether `${`, `\(`, `{`, `}` and `)` belong to the hole.
func relaxHolePunct(src string, exp []byte, root *sitter.Node) {
	var walk func(nd *sitter.Node, inLit bool)
	walk = func(nd *sitter.Node, inLit bool) {
		t := nd.Type()
		hole := false
		if p := nd.Parent(); inLit && p != nil && isHole(p.Type(), nd) {
			hole = true
		}
		if hole {
			a, b := int(nd.StartByte()), int(nd.EndByte())
			relaxAround(src, exp, a, -1)
			relaxAround(src, exp, a, +1)
			relaxAround(src, exp, b, -1)
			relaxAround(src, exp, b, +1)
		}
		lit := inLit || literalTypes[t] && nd.IsNamed()
		if hole {
			lit = false
		}
		for k := 0; k < int(nd.ChildCount()); k++ {
			walk(nd.Child(k), lit)
		}
	}
	walk(root, false)
}

// relaxAround marks up to three hole-punctuation bytes next to offset k
// (scanning in direction dir, skipping whitespace) as don't-care.
func relaxAround(src string, exp []byte, k, dir int) {
	if dir < 0 {
		k--
	}
	marked := 0
	for k >= 0 && k < len(src) && marked < 3 {
		c := src[k]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		case strings.IndexByte("${}()\\#", c) >= 0:
			exp[k] = 2
			marked++
		default:
			return
		}
		k += dir
	}
}

type mismatch struct {
	line     int
	want     byte
	snippet  string
	nodeKind string
}

// checkOracle returns mismatches between Mask and tree-sitter for one source,
// or ok=false when the source does not parse cleanly.
func checkOracle(t testing.TB, lang, src string) ([]mismatch, bool) {
	g := grammar(lang)
	if g == nil {
		return nil, false
	}
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(g)
	tree, err := p.ParseCtx(context.Background(), nil, []byte(src))
	if err != nil || tree == nil {
		return nil, false
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return nil, false
	}
	exp := expectations(root, len(src))
	relaxHolePunct(src, exp, root)
	got := MaskFile(lang, src)
	var out []mismatch
	lastLine := -1
	for k := 0; k < len(src); k++ {
		c := src[k]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' || exp[k] == 2 {
			continue
		}
		masked := got[k] == ' '
		if masked == (exp[k] == 1) {
			continue
		}
		line := strings.Count(src[:k], "\n") + 1
		if line == lastLine {
			continue
		}
		lastLine = line
		ls := strings.LastIndexByte(src[:k], '\n') + 1
		le := strings.IndexByte(src[k:], '\n')
		if le < 0 {
			le = len(src)
		} else {
			le += k
		}
		snip := src[ls:le]
		if len(snip) > 160 {
			snip = snip[:160]
		}
		kind := ""
		if nd := root.NamedDescendantForPointRange(pointAt(src, k), pointAt(src, k)); nd != nil {
			kind = nd.Type()
			if p := nd.Parent(); p != nil {
				kind = p.Type() + ">" + kind
			}
		}
		out = append(out, mismatch{line: line, want: exp[k], snippet: snip, nodeKind: kind})
	}
	return out, true
}

func pointAt(src string, k int) sitter.Point {
	row := strings.Count(src[:k], "\n")
	col := k - (strings.LastIndexByte(src[:k], '\n') + 1)
	return sitter.Point{Row: uint32(row), Column: uint32(col)}
}

func TestMaskMatchesTreeSitter(t *testing.T) {
	files, err := filepath.Glob("testdata/*")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		lang := langOfPath(f)
		if grammar(lang) == nil {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		ms, ok := checkOracle(t, lang, string(b))
		if !ok {
			if os.Getenv("TEXTMASK_DUMP") != "" {
				dumpErrors(t, lang, string(b))
			}
			t.Errorf("%s: tree-sitter reports a parse error; fix the snippet", f)
			continue
		}
		checked++
		for _, m := range ms {
			t.Errorf("%s:%d: want %s [%s]: %s", f, m.line, wantName(m.want), m.nodeKind, m.snippet)
		}
	}
	if checked == 0 {
		t.Fatal("no testdata checked")
	}
}

func wantName(w byte) string {
	if w == 1 {
		return "masked"
	}
	return "code"
}

func dumpErrors(t testing.TB, lang, src string) {
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(grammar(lang))
	tree, _ := p.ParseCtx(context.Background(), nil, []byte(src))
	defer tree.Close()
	var walk func(nd *sitter.Node)
	walk = func(nd *sitter.Node) {
		if nd.IsError() || nd.IsMissing() {
			t.Logf("  %s at %d:%d %q", nd.Type(), nd.StartPoint().Row+1, nd.StartPoint().Column, clip(src[nd.StartByte():nd.EndByte()]))
			return
		}
		for k := 0; k < int(nd.ChildCount()); k++ {
			walk(nd.Child(k))
		}
	}
	walk(tree.RootNode())
}

func clip(s string) string {
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

// TestMaskCorpus runs the oracle over a directory tree named by
// TEXTMASK_CORPUS. TEXTMASK_CAP bounds files per language (default 400).
func TestMaskCorpus(t *testing.T) {
	root := os.Getenv("TEXTMASK_CORPUS")
	if root == "" {
		t.Skip("TEXTMASK_CORPUS not set")
	}
	limit := 400
	if v, err := strconv.Atoi(os.Getenv("TEXTMASK_CAP")); err == nil && v > 0 {
		limit = v
	}
	verbose := os.Getenv("TEXTMASK_VERBOSE") != ""
	type stat struct{ seen, checked, parseErr, bad, badLines int }
	stats := map[string]*stat{}
	for _, dir := range strings.Split(root, string(os.PathListSeparator)) {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".git", "vendor", ".grove", "dist", "build":
					return filepath.SkipDir
				}
				return nil
			}
			lang := langOfPath(p)
			if lang == "" || grammar(lang) == nil {
				return nil
			}
			st := stats[lang]
			if st == nil {
				st = &stat{}
				stats[lang] = st
			}
			if st.seen >= limit {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() > 512<<10 || info.Size() == 0 {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			st.seen++
			ms, ok := checkOracle(t, lang, string(b))
			if !ok {
				st.parseErr++
				return nil
			}
			st.checked++
			if len(ms) > 0 {
				st.bad++
				st.badLines += len(ms)
				shown := ms
				if !verbose && len(shown) > 3 {
					shown = shown[:3]
				}
				for _, m := range shown {
					t.Logf("MISMATCH %s:%d want %s [%s]: %s", p, m.line, wantName(m.want), m.nodeKind, strings.TrimSpace(m.snippet))
				}
			}
			return nil
		})
	}
	langs := make([]string, 0, len(stats))
	for l := range stats {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	total := 0
	for _, l := range langs {
		st := stats[l]
		t.Logf("%-10s files=%d checked=%d parse-errors=%d mismatched-files=%d mismatched-lines=%d",
			l, st.seen, st.checked, st.parseErr, st.bad, st.badLines)
		total += st.bad
	}
	fmt.Fprintf(os.Stderr, "textmask corpus: %d mismatched files\n", total)
}
