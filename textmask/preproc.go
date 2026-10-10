package textmask

import "strings"

// MaskInactivePreprocessor masks the lines of `#if 0` branches (C, C++,
// Objective-C; `#if false` for C#), including any nested conditionals inside
// them, up to the matching `#else`, `#elif` or `#endif`. The branch after
// `#else`/`#elif` stays live. The directive lines of the disabled
// conditional itself are kept; directives nested inside it are masked.
// Comments and strings are not masked (compose with Mask for that), but they
// are respected: a `#if 0` inside a block comment is not a directive. Other
// languages return src unchanged.
func MaskInactivePreprocessor(lang, src string) string {
	var off []string
	switch lang {
	case "c", "cpp", "objc":
		off = []string{"0", "(0)"}
	case "csharp":
		off = []string{"false", "(false)"}
	default:
		return src
	}
	if !strings.Contains(src, "#") {
		return src
	}
	view := MaskComments(lang, src)
	out := []byte(src)
	changed := false
	depth := 0 // > 0 while inside a disabled branch: conditional nesting depth
	for ls := 0; ls < len(view); {
		le := strings.IndexByte(view[ls:], '\n')
		if le < 0 {
			le = len(view)
		} else {
			le += ls
		}
		dir, arg := directive(view[ls:le])
		switch {
		case depth == 0:
			if dir == "if" && contains(off, arg) {
				depth = 1
			}
		case dir == "if" || dir == "ifdef" || dir == "ifndef":
			depth++
			fallthrough
		default:
			if depth == 1 && (dir == "else" || dir == "elif" || dir == "endif") {
				depth = 0
				break
			}
			if dir == "endif" {
				depth--
			}
			for k := ls; k < le; k++ {
				if c := out[k]; c != '\r' && c != '\n' {
					out[k] = ' '
					changed = true
				}
			}
		}
		ls = le + 1
	}
	if !changed {
		return src
	}
	return string(out)
}

// directive splits a preprocessor line into its directive name and its
// argument text (trimmed). Non-directive lines return "", "".
func directive(line string) (string, string) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, "#") {
		return "", ""
	}
	t = strings.TrimLeft(t[1:], " \t")
	k := 0
	for k < len(t) && isIdentByte(t[k]) {
		k++
	}
	return t[:k], strings.TrimSpace(t[k:])
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
