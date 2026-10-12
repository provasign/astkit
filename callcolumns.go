package astkit

import (
	"strings"
	"unicode/utf8"

	"github.com/provasign/astkit/textmask"
)

// fillCallColumns sets each call site's Col: the 1-based character column
// of the called name on its line. The name is the callee's last segment
// ("out.value" -> "value"), found in the line's code (comments and strings
// masked) as an identifier followed by "(", "<" (type arguments) or "!" (a
// Rust macro). Several same-named calls on one line take the occurrences
// in order. A site whose name cannot be found keeps 0 (unknown).
func fillCallColumns(lang string, syms []Symbol, src []byte) {
	text := string(src)
	if textmask.Supported(lang) {
		text = textmask.MaskFile(lang, text)
	}
	var lines []string
	lineOf := func(n int) string {
		if lines == nil {
			lines = strings.Split(text, "\n")
		}
		if n < 1 || n > len(lines) {
			return ""
		}
		return lines[n-1]
	}
	type key struct {
		line int
		name string
	}
	cache := map[key][]int{}
	used := map[key]int{}
	for i := range syms {
		for j := range syms[i].CallSites {
			cs := &syms[i].CallSites[j]
			if cs.Line <= 0 || cs.Col > 0 {
				continue
			}
			name := cs.Callee
			for _, sep := range []string{".", "::", "->", ":"} {
				if k := strings.LastIndex(name, sep); k >= 0 {
					name = name[k+len(sep):]
				}
			}
			if name == "" {
				continue
			}
			k := key{cs.Line, name}
			cols, ok := cache[k]
			if !ok {
				cols = nameColumns(lineOf(cs.Line), name)
				cache[k] = cols
			}
			if n := used[k]; n < len(cols) {
				cs.Col = cols[n]
				used[k] = n + 1
			}
		}
	}
}

// nameColumns returns the 1-based character columns where name occurs in
// line as a called identifier.
func nameColumns(line, name string) []int {
	var out []int
	for start := 0; ; {
		i := strings.Index(line[start:], name)
		if i < 0 {
			return out
		}
		at := start + i
		end := at + len(name)
		start = end
		if at > 0 && isIdentByte(line[at-1]) || end < len(line) && isIdentByte(line[end]) {
			continue
		}
		rest := strings.TrimLeft(line[end:], " \t")
		if rest == "" || !strings.ContainsRune("(<!", rune(rest[0])) {
			continue
		}
		out = append(out, utf8.RuneCountInString(line[:at])+1)
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '$' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b >= 0x80
}
