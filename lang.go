package astkit

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/provasign/astkit/textmask"
)

// LanguageKey identifies a programming language or data format known to
// astkit. The empty string is the sentinel for "unknown".
type LanguageKey string

const (
	LangUnknown    LanguageKey = ""
	LangGo         LanguageKey = "go"
	LangTypeScript LanguageKey = "typescript"
	LangTSX        LanguageKey = "tsx"
	LangJavaScript LanguageKey = "javascript"
	LangPython     LanguageKey = "python"
	LangJava       LanguageKey = "java"
	LangRust       LanguageKey = "rust"
	LangC          LanguageKey = "c"
	LangCPP        LanguageKey = "cpp"
	LangCSharp     LanguageKey = "csharp"
	LangPHP        LanguageKey = "php"
	LangSwift      LanguageKey = "swift"
	LangKotlin     LanguageKey = "kotlin"
	LangObjC       LanguageKey = "objc"

	// Non-AST data formats — recognized for detection only; astkit does not
	// extract symbols from them. Callers (e.g. Fuse) handle these via
	// structured-merge.
	LangJSON LanguageKey = "json"
	LangYAML LanguageKey = "yaml"
	LangTOML LanguageKey = "toml"
)

// Mainframe artifact languages: extracted by TextStrategy implementations
// (no tree-sitter grammar). LangCOBOL covers programs and copybooks — the
// strategy distinguishes them by content.
const (
	LangCOBOL LanguageKey = "cobol"
	LangJCL   LanguageKey = "jcl"
)

var cppHeaderMarker = regexp.MustCompile(`(?m)^\s*(?:template\s*<|namespace\b|class\s+[A-Za-z_]|extern\s+"C")`)

// cppGuard matches a conditional on __cplusplus: group 1 is set for the
// negated forms (`#ifndef __cplusplus`, `#if !defined(__cplusplus)`).
var cppGuard = regexp.MustCompile(`^#\s*(?:ifdef\s+__cplusplus\b|if\s+(?:defined\s*\(?\s*__cplusplus\b|__cplusplus\b)|(ifndef\s+__cplusplus\b|if\s+!\s*defined\s*\(?\s*__cplusplus\b))`)

// isCPPHeader reports whether a .h header holds a C++-only declaration form
// in code a C compiler would see. Comments and `#if 0` branches are masked,
// and the C++-only branches of __cplusplus conditionals are blanked: the
// C-library idiom `#ifdef __cplusplus / extern "C" { / #endif` keeps a
// header C, while an unguarded `extern "C"` (invalid C) makes it C++. Grove's
// header sniff (internal/parser sniff.go sniffHeaderCode) applies the same
// rule, so both layers agree on a header's language.
func isCPPHeader(content string) bool {
	if !cppHeaderMarker.MatchString(content) {
		return false
	}
	code := textmask.MaskComments("cpp", textmask.MaskInactivePreprocessor("cpp", content))
	if strings.Contains(code, "__cplusplus") {
		code = blankCPPOnlyBranches(code)
	}
	return cppHeaderMarker.MatchString(code)
}

// blankCPPOnlyBranches blanks the lines of code that only a C++ compiler
// sees: the `#ifdef __cplusplus` branch, or the `#else` of an
// `#ifndef __cplusplus`.
func blankCPPOnlyBranches(code string) string {
	lines := strings.Split(code, "\n")
	type frame struct {
		guard   int // 1: #ifdef __cplusplus, -1: #ifndef __cplusplus, 0: other
		cppOnly bool
	}
	var stack []frame
	for i, line := range lines {
		t := strings.TrimSpace(line)
		directive := strings.HasPrefix(t, "#")
		if directive {
			d := strings.TrimSpace(strings.TrimPrefix(t, "#"))
			switch {
			case strings.HasPrefix(d, "if"):
				f := frame{}
				if m := cppGuard.FindStringSubmatch(t); m != nil {
					f.guard = 1
					if m[1] != "" {
						f.guard = -1
					}
					f.cppOnly = f.guard == 1
				}
				stack = append(stack, f)
			case strings.HasPrefix(d, "el"):
				if n := len(stack); n > 0 && stack[n-1].guard != 0 {
					stack[n-1].cppOnly = stack[n-1].guard == -1
				}
			case strings.HasPrefix(d, "endif"):
				if n := len(stack); n > 0 {
					stack = stack[:n-1]
				}
			}
			continue
		}
		for _, f := range stack {
			if f.cppOnly {
				lines[i] = ""
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// DetectLanguage returns the language for a given file path. Ambiguous .h
// headers are parsed as C++ when their contents contain a declaration form
// that is not valid C; otherwise they retain the conservative C default.
func DetectLanguage(path, content string) LanguageKey {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return LangGo
	case ".ts":
		return LangTypeScript
	case ".tsx":
		return LangTSX
	case ".js", ".jsx", ".mjs", ".cjs":
		return LangJavaScript
	case ".py":
		return LangPython
	case ".java":
		return LangJava
	case ".rs":
		return LangRust
	case ".c":
		return LangC
	case ".h":
		if isCPPHeader(content) {
			return LangCPP
		}
		return LangC
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return LangCPP
	case ".cs":
		return LangCSharp
	case ".php":
		return LangPHP
	case ".swift":
		return LangSwift
	case ".kt", ".kts":
		return LangKotlin
	case ".m", ".mm":
		// Objective-C++ (.mm) is a strict superset the vendored Objective-C
		// grammar does not fully model (raw C++ constructs inside a .mm
		// file can mis-parse); treated as best-effort Objective-C, same
		// spirit as the .h ambiguity above.
		return LangObjC
	case ".json":
		return LangJSON
	case ".yaml", ".yml":
		return LangYAML
	case ".toml":
		return LangTOML
	case ".cbl", ".cob", ".cobol", ".cpy", ".ccp", ".cpb", ".copy":
		return LangCOBOL
	case ".jcl", ".prc":
		return LangJCL
	default:
		return LangUnknown
	}
}

// IsAST reports whether the language has a tree-sitter grammar registered in
// astkit (i.e. Parse will succeed).
func IsAST(lang LanguageKey) bool {
	_, ok := treeSitterLanguage(lang)
	return ok
}

// IsConfigData reports whether the language is a structured data format.
func IsConfigData(lang LanguageKey) bool {
	return lang == LangJSON || lang == LangYAML || lang == LangTOML
}
