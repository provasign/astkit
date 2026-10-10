// Package textmask blanks comments and string literals out of source text so
// that callers scanning for code (identifiers, calls, declarations) never
// read prose or literal text as code.
//
// Every function preserves byte offsets: a masked byte becomes ' ' except
// '\n' and '\r', which are kept, so line and column numbers are unchanged
// and a match in the masked text can be sliced out of the original.
//
// The lexer is a single forward pass per language, linear in the input, with
// no regular expressions. It is tolerant: unterminated comments and literals
// run to the end of the input (or, for single-line literals, the end of the
// line) instead of failing.
//
// Interpolation holes are code. `${...}` (JavaScript, TypeScript, Kotlin),
// `{...}` (Python f/t-strings, C# interpolated strings), `\(...)` (Swift) and
// `{$...}`, `${...}`, `$var`, `$var->prop`, `$var[...]` (PHP double quotes,
// heredocs and backticks) are lexed recursively as code, so their identifiers
// stay visible and literals inside them are masked in turn. The hole
// punctuation itself (`${`, `}`, `\(`, `)`) is kept. Kotlin `$name` is kept
// too. Format specifiers — the text after a top-level `:` in a Python or C#
// hole (`{x:>10}`, `{d:yyyy-MM-dd}`) — are literal text and are masked, except
// for nested holes inside them. A Python `!r`/`!s`/`!a` conversion is kept.
//
// Known gaps, all heuristic boundaries a lexer cannot settle without a
// parser:
//   - JavaScript/TypeScript regex literals are told from division by the
//     previous token. `)` and `]` mean division (so `if (x) /re/.test(s)` is
//     misread), `}` means regex, except `{x} />` (a JSX self-closing tag).
//     A TypeScript non-null `x! / 2` is division.
//   - JSX/TSX text between tags is lexed as code: an apostrophe in
//     `<p>Don't</p>` opens a quote that runs to the end of that line, and
//     `//` in `<a>http://x</a>` starts a comment. A quoted string right
//     after `=` may span lines (a JSX attribute value).
//   - Swift regex literals (`/.../`, `#/.../#`) are not recognized.
//   - C/C++ strings inside `#define` bodies are masked although tree-sitter
//     treats a macro body as opaque text; `#error`/`#warning` text is kept.
//   - COBOL continuation lines (`-` in column 7) are not joined; each line's
//     literal ends at that line's end.
//   - JCL: only `//*` comment lines and quoted strings are handled.
//
// Correctness is checked against tree-sitter (see oracle_test.go): set
// TEXTMASK_CORPUS to a list of directories to run the comparison over real
// code.
package textmask

import (
	"strings"
	"unicode/utf8"
)

// Languages lists the language keys Mask understands (astkit LanguageKey
// strings), plus the alias "jsx" for JavaScript.
var Languages = []string{
	"go", "typescript", "tsx", "javascript", "jsx", "python", "java", "rust",
	"c", "cpp", "csharp", "php", "swift", "kotlin", "objc", "cobol", "jcl",
}

// Supported reports whether lang is a key Mask understands. Mask returns the
// input unchanged for any other key.
func Supported(lang string) bool {
	return familyOf(lang) != famNone
}

// Mask returns src with comment text and string/char literal text replaced by
// spaces. Every byte keeps its offset: masked bytes become ' ', except '\n'
// and '\r', which are kept, so line and column numbers are unchanged. Code
// inside interpolation holes is kept and itself masked recursively. String
// delimiters and prefixes (quotes, r#, @, $, R"delim() are masked too. src may
// be a whole file or a symbol snippet; it is lexed as code from byte 0.
func Mask(lang, src string) string {
	return run(lang, src, true, false)
}

// MaskComments masks only comments; string literals are kept verbatim (they
// are still lexed, so a comment marker inside a string is not a comment).
// Useful for scans that read literal text, such as `#include "x.h"`.
func MaskComments(lang, src string) string {
	return run(lang, src, false, false)
}

// MaskFile is Mask for a whole file. It is identical to Mask except for PHP,
// where the file starts in inline-HTML mode: text outside `<?php ... ?>` and
// `<?= ... ?>` is masked.
func MaskFile(lang, src string) string {
	return run(lang, src, true, true)
}

func run(lang, src string, maskStrings, phpHTML bool) string {
	fam := familyOf(lang)
	if fam == famNone || src == "" {
		return src
	}
	s := &scanner{
		src:         src,
		out:         []byte(src),
		fam:         fam,
		lang:        lang,
		maskStrings: maskStrings,
	}
	switch fam {
	case famCOBOL:
		s.cobol()
	case famJCL:
		s.jcl()
	case famPHP:
		i := 0
		if phpHTML {
			i = s.phpHTML(0)
		}
		for i < len(src) {
			i = s.code(i, 0, false)
			if i < len(src) {
				// code stopped at `?>`; the tag is code, the rest is HTML.
				i = s.phpHTML(i + 2)
			}
		}
	default:
		s.code(0, 0, false)
	}
	return string(s.out)
}

type family int

const (
	famNone family = iota
	famC           // C, C++, Objective-C
	famJava
	famCSharp
	famGo
	famRust
	famJS
	famPython
	famPHP
	famKotlin
	famSwift
	famCOBOL
	famJCL
)

func familyOf(lang string) family {
	switch lang {
	case "c", "cpp", "objc":
		return famC
	case "java":
		return famJava
	case "csharp":
		return famCSharp
	case "go":
		return famGo
	case "rust":
		return famRust
	case "javascript", "jsx", "typescript", "tsx":
		return famJS
	case "python":
		return famPython
	case "php":
		return famPHP
	case "kotlin":
		return famKotlin
	case "swift":
		return famSwift
	case "cobol":
		return famCOBOL
	case "jcl":
		return famJCL
	}
	return famNone
}

type scanner struct {
	src         string
	out         []byte
	fam         family
	lang        string
	maskStrings bool
	// regexOK is the JavaScript previous-token state: true when a `/` at
	// this point starts a regex literal rather than a division.
	regexOK bool
}

// blank masks src[a:b], keeping line breaks.
func (s *scanner) blank(a, b int) {
	if b > len(s.out) {
		b = len(s.out)
	}
	for k := a; k < b; k++ {
		if c := s.out[k]; c != '\n' && c != '\r' {
			s.out[k] = ' '
		}
	}
}

// lit masks literal text src[a:b] when strings are being masked.
func (s *scanner) lit(a, b int) {
	if s.maskStrings {
		s.blank(a, b)
	}
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// at reports whether src has lit at offset i.
func (s *scanner) at(i int, lit string) bool {
	return i >= 0 && i+len(lit) <= len(s.src) && s.src[i:i+len(lit)] == lit
}

func (s *scanner) byteAt(i int) byte {
	if i >= 0 && i < len(s.src) {
		return s.src[i]
	}
	return 0
}

// eol returns the offset of the next '\n' at or after i, or len(src).
func (s *scanner) eol(i int) int {
	if k := strings.IndexByte(s.src[i:], '\n'); k >= 0 {
		return i + k
	}
	return len(s.src)
}

// prevByte returns the last non-blank byte before i, or 0.
func (s *scanner) prevByte(i int) byte {
	for k := i - 1; k >= 0; k-- {
		switch c := s.src[k]; c {
		case ' ', '\t', '\n', '\r':
		default:
			return c
		}
	}
	return 0
}

// backtickName skips a backtick-quoted identifier (Kotlin, Swift), which is
// code even when it holds spaces or apostrophes: fun `it doesn't fail`().
func (s *scanner) backtickName(i int) int {
	for k := i + 1; k < len(s.src); k++ {
		switch s.src[k] {
		case '`':
			return k + 1
		case '\n':
			return i + 1
		}
	}
	return i + 1
}

// atLineStart reports whether only spaces and tabs precede i on its line.
func (s *scanner) atLineStart(i int) bool {
	for k := i - 1; k >= 0; k-- {
		switch s.src[k] {
		case ' ', '\t':
			continue
		case '\n', '\r':
			return true
		default:
			return false
		}
	}
	return true
}

// lineComment masks a comment from i to the end of its line and returns the
// offset of the '\n'. With cont, a backslash before the newline continues the
// comment onto the next line (C preprocessor semantics).
func (s *scanner) lineComment(i int, cont bool) int {
	k := s.eol(i)
	for cont && k < len(s.src) {
		j := k - 1
		if j > i && s.src[j] == '\r' {
			j--
		}
		if j < i || s.src[j] != '\\' {
			break
		}
		k = s.eol(k + 1)
	}
	s.blank(i, k)
	return k
}

// blockComment masks a `/* ... */` comment starting at i and returns the
// offset after it. With nested, inner `/*` pairs nest (Rust, Kotlin, Swift).
func (s *scanner) blockComment(i int, nested bool) int {
	n := len(s.src)
	k := i + 2
	depth := 1
	for k < n {
		if s.src[k] == '*' && k+1 < n && s.src[k+1] == '/' {
			k += 2
			depth--
			if depth == 0 {
				break
			}
			continue
		}
		if nested && s.src[k] == '/' && k+1 < n && s.src[k+1] == '*' {
			depth++
			k += 2
			continue
		}
		k++
	}
	if k > n {
		k = n
	}
	s.blank(i, k)
	return k
}

// quoted masks a literal opening with quote q at i (prefix bytes from start
// are masked with it) and returns the offset after it. With esc, a backslash
// skips the next byte. Without multiline, an unescaped newline ends it.
func (s *scanner) quoted(start, i int, q byte, esc, multiline bool) int {
	n := len(s.src)
	k := i + 1
	for k < n {
		c := s.src[k]
		if c == '\\' && esc {
			k = s.esc(k)
			continue
		}
		if c == q {
			k++
			break
		}
		if c == '\n' && !multiline {
			break
		}
		k++
	}
	if k > n {
		k = n
	}
	s.lit(start, k)
	return k
}

// doubled masks a literal opening with quote q at i where a doubled quote is
// an escaped quote (C# verbatim strings, COBOL, JCL). Multiline unless
// oneLine.
func (s *scanner) doubled(start, i int, q byte, oneLine bool) int {
	n := len(s.src)
	k := i + 1
	for k < n {
		c := s.src[k]
		if c == q {
			if k+1 < n && s.src[k+1] == q {
				k += 2
				continue
			}
			k++
			break
		}
		if c == '\n' && oneLine {
			break
		}
		k++
	}
	s.lit(start, k)
	return k
}

// number skips a numeric literal starting at i. Digit separators: `_` in
// every language, and `'` between alphanumerics in the C family (C++14,
// C23), so `1'000` and `0xFF'FF` are not character literals.
func (s *scanner) number(i int) int {
	n := len(s.src)
	k := i + 1
	for k < n {
		c := s.src[k]
		switch {
		case isIdentByte(c) || c == '.':
			k++
		case (c == '+' || c == '-') && (s.src[k-1] == 'e' || s.src[k-1] == 'E' || s.src[k-1] == 'p' || s.src[k-1] == 'P'):
			k++
		case c == '\'' && s.fam == famC && k+1 < n && isAlnum(s.src[k+1]):
			k++
		default:
			return k
		}
	}
	return k
}

// esc returns the offset after the escape sequence whose backslash is at k:
// two bytes, or three for a backslash before CRLF (a line continuation).
func (s *scanner) esc(k int) int {
	if s.at(k+1, "\r\n") {
		return k + 3
	}
	return k + 2
}

// code lexes src from i as code. It returns the offset of the first
// unmatched closer (')' or '}', when closer is non-zero) or len(src). With
// fmtStop, a top-level ':' (and, for Python, a '!' conversion) also stops it:
// the start of a format specifier inside an interpolation hole. PHP stops at
// `?>` as well.
func (s *scanner) code(i int, closer byte, fmtStop bool) int {
	n := len(s.src)
	depth := 0
	if s.fam == famJS {
		s.regexOK = true
	}
	for i < n {
		c := s.src[i]
		switch c {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			i++
			continue
		case '(', '[', '{':
			depth++
			i++
			s.regexOK = true
			continue
		case ')', ']', '}':
			if depth == 0 && closer != 0 && c == closer {
				return i
			}
			if depth > 0 {
				depth--
			}
			i++
			s.regexOK = c == '}'
			continue
		case ':':
			if fmtStop && depth == 0 && !s.at(i+1, ":") && !s.at(i-1, ":") {
				return i
			}
		case '!':
			if fmtStop && depth == 0 && s.fam == famPython && !s.at(i+1, "=") {
				return i
			}
		}
		if isIdentStart(c) || c == '$' && s.fam == famJS {
			k := i + 1
			for k < n && (isIdentByte(s.src[k]) || s.src[k] == '$' && s.fam == famJS) {
				k++
			}
			if j, ok := s.prefixed(i, k); ok {
				i = j
				s.regexOK = false
				continue
			}
			if s.fam == famJS {
				s.regexOK = jsRegexKeyword(s.src[i:k])
			}
			i = k
			continue
		}
		if isDigit(c) {
			i = s.number(i)
			s.regexOK = false
			continue
		}
		j := s.token(i)
		if j < 0 {
			return i // PHP `?>`
		}
		if j > i {
			i = j
			s.regexOK = false
			continue
		}
		// Plain punctuation.
		if s.fam == famJS {
			s.regexOK = jsRegexAfter(c)
			if c == '!' && i > 0 && (isIdentByte(s.src[i-1]) || s.src[i-1] == ')' || s.src[i-1] == ']') {
				s.regexOK = false // TypeScript non-null assertion `x! / 2`
			}
		}
		i++
	}
	return n
}

func jsRegexAfter(c byte) bool {
	switch c {
	case ',', '=', ':', '!', '&', '|', '?', ';', '+', '-', '*', '%', '>', '~', '^':
		return true
	}
	return false
}

func jsRegexKeyword(w string) bool {
	switch w {
	case "return", "typeof", "instanceof", "in", "of", "new", "delete", "void",
		"throw", "case", "do", "else", "yield", "await":
		return true
	}
	return false
}

// prefixed handles an identifier src[i:k] that may be a string prefix. It
// returns the offset after the literal and true when one was consumed.
func (s *scanner) prefixed(i, k int) (int, bool) {
	if k >= len(s.src) {
		return 0, false
	}
	next := s.src[k]
	w := s.src[i:k]
	switch s.fam {
	case famC:
		if next == '"' || next == '\'' {
			switch w {
			case "L", "u", "U", "u8":
				return s.quoted(i, k, next, true, false), true
			case "R", "LR", "uR", "UR", "u8R":
				if next == '"' && s.lang != "c" {
					if j, ok := s.cppRaw(i, k); ok {
						return j, true
					}
				}
			}
		}
	case famRust:
		switch w {
		case "b":
			if next == '\'' {
				return s.quoted(i, k, '\'', true, false), true
			}
			if next == '"' {
				return s.quoted(i, k, '"', true, true), true
			}
		case "c":
			if next == '"' {
				return s.quoted(i, k, '"', true, true), true
			}
		case "r", "br", "cr":
			if next == '"' || next == '#' {
				return s.rustRaw(i, k)
			}
		}
	case famPython:
		if (next == '"' || next == '\'') && len(w) <= 2 {
			raw, fmt := false, false
			for j := 0; j < len(w); j++ {
				switch w[j] | 0x20 {
				case 'r':
					raw = true
				case 'f', 't':
					fmt = true
				case 'b', 'u':
				default:
					return 0, false
				}
			}
			if len(w) == 2 && w[0]|0x20 == w[1]|0x20 {
				return 0, false
			}
			return s.pyString(i, k, raw, fmt), true
		}
	}
	return 0, false
}

// token lexes a comment or literal starting at a non-identifier byte i and
// returns the offset after it, i when src[i] is plain punctuation, or -1 for
// PHP's `?>`.
func (s *scanner) token(i int) int {
	c := s.src[i]
	next := s.byteAt(i + 1)
	if c == '/' && s.fam != famPython && s.fam != famCOBOL && s.fam != famJCL {
		if next == '/' {
			if s.fam == famPHP {
				return s.phpLineComment(i)
			}
			return s.lineComment(i, s.fam == famC)
		}
		if next == '*' {
			nested := s.fam == famRust || s.fam == famKotlin || s.fam == famSwift
			return s.blockComment(i, nested)
		}
	}
	switch s.fam {
	case famC:
		switch c {
		case '"', '\'':
			return s.quoted(i, i, c, true, false)
		case '@':
			if next == '"' && s.lang == "objc" {
				return s.quoted(i, i+1, '"', true, false)
			}
		case '#':
			if s.atLineStart(i) {
				return s.cDirective(i)
			}
		}
	case famJava:
		switch c {
		case '"':
			if s.at(i, `"""`) {
				return s.javaTextBlock(i)
			}
			return s.quoted(i, i, c, true, false)
		case '\'':
			return s.quoted(i, i, c, true, false)
		}
	case famGo:
		switch c {
		case '"', '\'':
			return s.quoted(i, i, c, true, false)
		case '`':
			return s.quoted(i, i, c, false, true)
		}
	case famRust:
		switch c {
		case '"':
			return s.quoted(i, i, c, true, true)
		case '\'':
			return s.rustQuote(i)
		}
	case famJS:
		switch c {
		case '"', '\'':
			// A JSX attribute value (`title="..."`) may span lines; a
			// plain JavaScript string may not.
			return s.quoted(i, i, c, true, s.prevByte(i) == '=')
		case '`':
			return s.jsTemplate(i)
		case '#':
			if i == 0 && next == '!' {
				return s.lineComment(i, false)
			}
		case '/':
			// `{expr} />` closes a JSX tag; `(/>/g)` is a regex.
			if s.regexOK && !(next == '>' && s.prevByte(i) == '}') {
				return s.jsRegex(i)
			}
		}
	case famPython:
		switch c {
		case '#':
			return s.lineComment(i, false)
		case '"', '\'':
			return s.pyString(i, i, false, false)
		}
	case famPHP:
		switch c {
		case '#':
			if next != '[' {
				return s.phpLineComment(i)
			}
		case '\'':
			return s.quoted(i, i, c, true, true)
		case '"', '`':
			return s.phpInterp(i, i+1, c)
		case '<':
			if s.at(i, "<<<") {
				if j, ok := s.phpHeredoc(i); ok {
					return j
				}
				return i + 3
			}
		case '?':
			if next == '>' {
				return -1
			}
		}
	case famCSharp:
		return s.csharpToken(i)
	case famKotlin:
		switch c {
		case '`':
			return s.backtickName(i)
		case '"':
			return s.kotlinString(i)
		case '\'':
			return s.quoted(i, i, c, true, false)
		}
	case famSwift:
		switch c {
		case '`':
			return s.backtickName(i)
		case '"':
			return s.swiftString(i, i, 0)
		case '#':
			h := i
			for h < len(s.src) && s.src[h] == '#' {
				h++
			}
			if h < len(s.src) && s.src[h] == '"' {
				return s.swiftString(i, h, h-i)
			}
			return h
		}
	}
	return i
}

// ---- C family ----

// cDirective handles a preprocessor line starting at i. `#error` and
// `#warning` take free text (apostrophes included), so their line is skipped
// as code apart from comments. Other directives are lexed as code.
func (s *scanner) cDirective(i int) int {
	k := i + 1
	for k < len(s.src) && (s.src[k] == ' ' || s.src[k] == '\t') {
		k++
	}
	w := k
	for w < len(s.src) && isIdentByte(s.src[w]) {
		w++
	}
	switch s.src[k:w] {
	case "error", "warning":
		return s.freeTextLine(w, true)
	}
	return i + 1
}

// freeTextLine skips text from i to the end of the line (with backslash
// continuation when cont) treating only `//` and `/*` as special.
func (s *scanner) freeTextLine(i int, cont bool) int {
	n := len(s.src)
	for i < n {
		c := s.src[i]
		if c == '\n' {
			j := i - 1
			if j >= 0 && s.src[j] == '\r' {
				j--
			}
			if cont && j >= 0 && s.src[j] == '\\' {
				i++
				continue
			}
			return i
		}
		if c == '/' && i+1 < n && s.src[i+1] == '/' {
			return s.lineComment(i, cont)
		}
		if c == '/' && i+1 < n && s.src[i+1] == '*' {
			i = s.blockComment(i, false)
			continue
		}
		i++
	}
	return n
}

// cppRaw lexes a C++ raw string `R"delim( ... )delim"` whose quote is at q.
func (s *scanner) cppRaw(start, q int) (int, bool) {
	n := len(s.src)
	k := q + 1
	for k < n && k-q <= 17 {
		c := s.src[k]
		if c == '(' {
			break
		}
		if c == ' ' || c == ')' || c == '\\' || c == '\t' || c == '\n' || c == '"' {
			return 0, false
		}
		k++
	}
	if k >= n || s.src[k] != '(' {
		return 0, false
	}
	delim := s.src[q+1 : k]
	end := n
	for p := k + 1; p < n; p++ {
		if s.src[p] == ')' && s.at(p+1, delim) && s.byteAt(p+1+len(delim)) == '"' {
			end = p + 2 + len(delim)
			break
		}
	}
	s.lit(start, end)
	return end, true
}

// ---- Java ----

func (s *scanner) javaTextBlock(i int) int {
	n := len(s.src)
	k := i + 3
	for k < n {
		if s.src[k] == '\\' {
			k = s.esc(k)
			continue
		}
		if s.at(k, `"""`) {
			k += 3
			break
		}
		k++
	}
	if k > n {
		k = n
	}
	s.lit(i, k)
	return k
}

// ---- Rust ----

// rustQuote tells a char literal (`'a'`, `'\n'`, `'é'`, `'"'`) from a
// lifetime or label (`'a`, `'static`, `'_`), which is code.
func (s *scanner) rustQuote(i int) int {
	n := len(s.src)
	if i+1 >= n {
		return i + 1
	}
	if s.src[i+1] == '\\' {
		return s.quoted(i, i, '\'', true, false)
	}
	_, size := utf8.DecodeRuneInString(s.src[i+1:])
	if k := i + 1 + size; k < n && s.src[k] == '\'' {
		s.lit(i, k+1)
		return k + 1
	}
	return i + 1 // lifetime: the name after it lexes as an identifier
}

// rustRaw lexes `r"..."`, `r#"..."#`, `br##"..."##`; the prefix spans
// src[start:k]. `r#ident` (a raw identifier) is not a string.
func (s *scanner) rustRaw(start, k int) (int, bool) {
	h := k
	for h < len(s.src) && s.src[h] == '#' {
		h++
	}
	if h >= len(s.src) || s.src[h] != '"' {
		return 0, false
	}
	hashes := h - k
	end := len(s.src)
	for q := h + 1; q < len(s.src); q++ {
		if s.src[q] != '"' {
			continue
		}
		e := q + 1
		for e < len(s.src) && e-q-1 < hashes && s.src[e] == '#' {
			e++
		}
		if e-q-1 == hashes {
			end = e
			break
		}
	}
	s.lit(start, end)
	return end, true
}

// ---- JavaScript / TypeScript ----

func (s *scanner) jsTemplate(i int) int {
	n := len(s.src)
	seg := i
	k := i + 1
	for k < n {
		c := s.src[k]
		if c == '\\' {
			k = s.esc(k)
			continue
		}
		if c == '`' {
			k++
			s.lit(seg, k)
			return k
		}
		if c == '$' && k+1 < n && s.src[k+1] == '{' {
			s.lit(seg, k)
			k = s.hole(k+2, '}')
			seg = k
			continue
		}
		k++
	}
	s.lit(seg, n)
	return n
}

// hole lexes an interpolation hole's code starting at i up to closer and
// returns the offset after the closer.
func (s *scanner) hole(i int, closer byte) int {
	saved := s.regexOK
	k := s.code(i, closer, false)
	s.regexOK = saved
	if k < len(s.src) {
		k++
	}
	return k
}

// jsRegex lexes a regex literal at i, or returns i when the line ends before
// a closing slash (then the '/' was a division after all).
func (s *scanner) jsRegex(i int) int {
	n := len(s.src)
	k := i + 1
	inClass := false
	for k < n {
		c := s.src[k]
		switch {
		case c == '\\':
			k = s.esc(k)
			continue
		case c == '\n' || c == '\r':
			return i
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			k++
			for k < n && isIdentByte(s.src[k]) {
				k++
			}
			s.lit(i, k)
			return k
		}
		k++
	}
	return i
}

// ---- Python ----

// pyString lexes a Python string whose prefix starts at start and whose
// opening quote is at q.
func (s *scanner) pyString(start, q int, raw, fmtStr bool) int {
	n := len(s.src)
	qc := s.src[q]
	triple := q+2 < n && s.src[q+1] == qc && s.src[q+2] == qc
	k := q + 1
	if triple {
		k = q + 3
	}
	seg := start
	for k < n {
		c := s.src[k]
		switch {
		case c == '\\':
			if fmtStr && s.byteAt(k+1) == '{' && raw {
				k++ // rf"\{x}": the brace still opens a hole
				continue
			}
			if fmtStr && !raw && s.at(k+1, "N{") {
				// \N{NAME} is an escape, not a hole.
				if e := strings.IndexByte(s.src[k:], '}'); e >= 0 {
					k += e + 1
					continue
				}
			}
			k = s.esc(k)
			continue
		case c == qc:
			if !triple {
				k++
				s.lit(seg, k)
				return k
			}
			if k+2 < n && s.src[k+1] == qc && s.src[k+2] == qc {
				k += 3
				s.lit(seg, k)
				return k
			}
		case c == '\n' && !triple:
			s.lit(seg, k)
			return k
		case fmtStr && c == '{':
			if k+1 < n && s.src[k+1] == '{' {
				k += 2
				continue
			}
			s.lit(seg, k)
			k = s.fmtHole(k + 1)
			seg = k
			continue
		}
		k++
	}
	if k > n {
		k = n
	}
	s.lit(seg, k)
	return k
}

// fmtHole lexes a Python f-string or C# interpolation hole whose code starts
// at i: the expression, an optional Python `!r` conversion, and an optional
// format specifier (masked as literal text, nested holes kept). It returns
// the offset after the closing '}'.
func (s *scanner) fmtHole(i int) int {
	n := len(s.src)
	k := s.code(i, '}', true)
	if k < n && s.src[k] == '!' {
		k++
		for k < n && isIdentByte(s.src[k]) {
			k++
		}
	}
	if k < n && s.src[k] == ':' {
		k = s.fmtSpec(k)
	}
	if k < n && s.src[k] == '}' {
		k++
	}
	return k
}

// fmtSpec masks a format specifier starting at its ':' up to the hole's
// closing '}', which it returns the offset of.
func (s *scanner) fmtSpec(i int) int {
	n := len(s.src)
	seg := i
	k := i
	for k < n {
		c := s.src[k]
		if c == '}' {
			break
		}
		if c == '{' && s.fam == famPython {
			s.lit(seg, k)
			k = s.fmtHole(k + 1)
			seg = k
			continue
		}
		if c == '\n' && s.fam == famCSharp {
			break
		}
		k++
	}
	s.lit(seg, k)
	return k
}

// ---- PHP ----

// phpHTML masks inline HTML from i up to the next open tag (`<?php`, `<?=`,
// `<?`) and returns the offset after the tag.
func (s *scanner) phpHTML(i int) int {
	k := strings.Index(s.src[i:], "<?")
	if k < 0 {
		s.lit(i, len(s.src))
		return len(s.src)
	}
	k += i
	s.lit(i, k)
	switch {
	case s.at(k, "<?php"):
		return k + 5
	case s.at(k, "<?="):
		return k + 3
	}
	return k + 2
}

// phpLineComment masks a `//` or `#` comment, which ends at the line end or
// before a `?>` close tag.
func (s *scanner) phpLineComment(i int) int {
	e := s.eol(i)
	if t := strings.Index(s.src[i:e], "?>"); t >= 0 {
		e = i + t
	}
	s.blank(i, e)
	return e
}

// phpInterp lexes a double-quoted or backtick PHP string whose quote is at
// q-1 and whose text starts at q, keeping `{$...}`, `${...}` and simple
// variable interpolations as code.
func (s *scanner) phpInterp(start, k int, q byte) int {
	n := len(s.src)
	seg := start
	for k < n {
		c := s.src[k]
		if c == '\\' {
			k = s.esc(k)
			continue
		}
		if c == q {
			k++
			s.lit(seg, k)
			return k
		}
		if j := s.phpVar(k); j > k {
			s.lit(seg, k)
			seg, k = j, j
			continue
		}
		k++
	}
	s.lit(seg, n)
	return n
}

// phpVar recognises an interpolation at k inside a PHP string: `{$expr}`,
// `${expr}`, `$name`, `$name->prop`, `$name?->prop` or `$name[key]`. It
// returns the offset after it, or k when there is none.
func (s *scanner) phpVar(k int) int {
	n := len(s.src)
	if s.at(k, "{$") {
		return s.hole(k+1, '}')
	}
	if s.at(k, "${") {
		return s.hole(k+2, '}')
	}
	if s.src[k] != '$' || k+1 >= n || !isIdentStart(s.src[k+1]) {
		return k
	}
	j := k + 2
	for j < n && isIdentByte(s.src[j]) {
		j++
	}
	switch {
	case s.at(j, "->") && j+2 < n && isIdentStart(s.src[j+2]):
		j += 3
		for j < n && isIdentByte(s.src[j]) {
			j++
		}
	case s.at(j, "?->") && j+3 < n && isIdentStart(s.src[j+3]):
		j += 4
		for j < n && isIdentByte(s.src[j]) {
			j++
		}
	case s.at(j, "["):
		e := j + 1
		for e < n && s.src[e] != ']' && s.src[e] != '\n' && s.src[e] != '"' {
			e++
		}
		if e < n && s.src[e] == ']' {
			j = e + 1
		}
	}
	return j
}

// phpHeredoc lexes `<<<ID`, `<<<"ID"` (interpolated) and `<<<'ID'`
// (nowdoc). The closing identifier may be indented and followed by any
// non-identifier byte (PHP 7.3+).
func (s *scanner) phpHeredoc(i int) (int, bool) {
	n := len(s.src)
	k := i + 3
	for k < n && (s.src[k] == ' ' || s.src[k] == '\t') {
		k++
	}
	quote := byte(0)
	if k < n && (s.src[k] == '"' || s.src[k] == '\'') {
		quote = s.src[k]
		k++
	}
	idStart := k
	for k < n && isIdentByte(s.src[k]) {
		k++
	}
	if k == idStart || !isIdentStart(s.src[idStart]) {
		return 0, false
	}
	id := s.src[idStart:k]
	if quote != 0 {
		if k >= n || s.src[k] != quote {
			return 0, false
		}
		k++
	}
	if k < n && s.src[k] == '\r' {
		k++
	}
	if k >= n || s.src[k] != '\n' {
		return 0, false
	}
	interp := quote != '\''
	seg := i
	k++
	lineStart := true
	for k < n {
		if lineStart {
			j := k
			for j < n && (s.src[j] == ' ' || s.src[j] == '\t') {
				j++
			}
			if s.at(j, id) && !isIdentByte(s.byteAt(j+len(id))) {
				end := j + len(id)
				s.lit(seg, end)
				return end, true
			}
			lineStart = false
		}
		c := s.src[k]
		if c == '\n' {
			lineStart = true
			k++
			continue
		}
		if interp {
			if c == '\\' {
				k = s.esc(k)
				continue
			}
			if j := s.phpVar(k); j > k {
				s.lit(seg, k)
				seg, k = j, j
				continue
			}
		}
		k++
	}
	s.lit(seg, n)
	return n, true
}

// ---- C# ----

func (s *scanner) csharpToken(i int) int {
	n := len(s.src)
	c := s.src[i]
	switch c {
	case '\'':
		return s.quoted(i, i, c, true, false)
	case '#':
		if s.atLineStart(i) {
			k := i + 1
			for k < n && (s.src[k] == ' ' || s.src[k] == '\t') {
				k++
			}
			w := k
			for w < n && isIdentByte(s.src[w]) {
				w++
			}
			switch s.src[k:w] {
			case "region", "endregion", "error", "warning", "pragma", "line", "nullable":
				return s.freeTextLine(w, false)
			}
		}
		return i
	case '"', '@', '$':
	default:
		return i
	}
	// String prefixes: `$`* and `@` in either order, then quotes.
	k := i
	dollars, verbatim := 0, false
	for k < n {
		if s.src[k] == '$' {
			dollars++
		} else if s.src[k] == '@' && !verbatim {
			verbatim = true
		} else {
			break
		}
		k++
	}
	if k >= n || s.src[k] != '"' {
		return i
	}
	q := k
	for q < n && s.src[q] == '"' {
		q++
	}
	quotes := q - k
	if quotes >= 3 && !verbatim {
		return s.csharpRaw(i, k, quotes, dollars)
	}
	if verbatim {
		if dollars == 0 {
			return s.doubled(i, k, '"', false)
		}
		return s.csharpInterp(i, k+1, true)
	}
	if dollars > 0 {
		return s.csharpInterp(i, k+1, false)
	}
	return s.quoted(i, k, '"', true, false)
}

// csharpInterp lexes `$"..."` (and `$@"..."` when verbatim) from the text
// start k, keeping `{expr}` holes as code.
func (s *scanner) csharpInterp(start, k int, verbatim bool) int {
	n := len(s.src)
	seg := start
	for k < n {
		c := s.src[k]
		switch {
		case c == '\\' && !verbatim:
			k = s.esc(k)
			continue
		case c == '"':
			if verbatim && k+1 < n && s.src[k+1] == '"' {
				k += 2
				continue
			}
			k++
			s.lit(seg, k)
			return k
		case c == '\n' && !verbatim:
			s.lit(seg, k)
			return k
		case c == '{' || c == '}':
			if k+1 < n && s.src[k+1] == c {
				k += 2
				continue
			}
			if c == '{' {
				s.lit(seg, k)
				k = s.fmtHole(k + 1)
				seg = k
				continue
			}
		}
		k++
	}
	s.lit(seg, n)
	return n
}

// csharpRaw lexes a raw string literal of quotes quote characters starting
// at k (prefix from start). With dollars > 0 it is interpolated: a run of
// `dollars` braces opens a hole, fewer are literal text.
func (s *scanner) csharpRaw(start, k, quotes, dollars int) int {
	n := len(s.src)
	seg := start
	k += quotes
	for k < n {
		c := s.src[k]
		if c == '"' {
			r := k
			for r < n && s.src[r] == '"' {
				r++
			}
			if r-k >= quotes {
				s.lit(seg, r)
				return r
			}
			k = r
			continue
		}
		if c == '{' && dollars > 0 {
			r := k
			for r < n && s.src[r] == '{' {
				r++
			}
			if r-k < dollars {
				k = r
				continue
			}
			open := r - dollars
			s.lit(seg, open)
			e := s.code(r, '}', true)
			if e < n && s.src[e] == ':' {
				e = s.fmtSpec(e)
			}
			for j := 0; j < dollars && e < n && s.src[e] == '}'; j++ {
				e++
			}
			seg, k = e, e
			continue
		}
		k++
	}
	s.lit(seg, n)
	return n
}

// ---- Kotlin ----

func (s *scanner) kotlinString(i int) int {
	n := len(s.src)
	raw := s.at(i, `"""`)
	k := i + 1
	if raw {
		k = i + 3
	}
	seg := i
	for k < n {
		c := s.src[k]
		switch {
		case c == '\\' && !raw:
			k = s.esc(k)
			continue
		case c == '"':
			if !raw {
				k++
				s.lit(seg, k)
				return k
			}
			if s.at(k, `"""`) {
				k += 3
				for k < n && s.src[k] == '"' {
					k++
				}
				s.lit(seg, k)
				return k
			}
		case c == '\n' && !raw:
			s.lit(seg, k)
			return k
		case c == '$' && k+1 < n:
			if s.src[k+1] == '{' {
				s.lit(seg, k)
				k = s.hole(k+2, '}')
				seg = k
				continue
			}
			if isIdentStart(s.src[k+1]) || s.src[k+1] == '`' {
				s.lit(seg, k)
				k += 2
				for k < n && isIdentByte(s.src[k]) {
					k++
				}
				seg = k
				continue
			}
		}
		k++
	}
	s.lit(seg, n)
	return n
}

// ---- Swift ----

// swiftString lexes a Swift string whose prefix starts at start, whose
// first quote is at q and which has hashes extended delimiters.
func (s *scanner) swiftString(start, q, hashes int) int {
	n := len(s.src)
	multi := s.at(q, `"""`)
	k := q + 1
	if multi {
		k = q + 3
	}
	seg := start
	for k < n {
		c := s.src[k]
		switch {
		case c == '\\':
			j := k + 1
			h := 0
			for j < n && s.src[j] == '#' && h < hashes {
				j++
				h++
			}
			if h < hashes {
				k++
				continue
			}
			if j < n && s.src[j] == '(' {
				s.lit(seg, k)
				k = s.hole(j+1, ')')
				seg = k
				continue
			}
			k = j + 1
			continue
		case c == '"':
			if multi && !s.at(k, `"""`) {
				k++
				continue
			}
			e := k + 1
			if multi {
				e = k + 3
			}
			h := 0
			for e < n && s.src[e] == '#' && h < hashes {
				e++
				h++
			}
			if h == hashes {
				s.lit(seg, e)
				return e
			}
			k++
			continue
		case c == '\n' && !multi:
			s.lit(seg, k)
			return k
		}
		k++
	}
	s.lit(seg, n)
	return n
}

// ---- COBOL ----

// cobol masks COBOL source line by line. Fixed format (the default): a `*`
// or `/` in column 7 makes the line a comment (columns 7-72 masked; the
// sequence areas are left as-is). `*>` starts an inline comment in either
// format. `>>SOURCE FORMAT FREE` switches to free format.
func (s *scanner) cobol() {
	n := len(s.src)
	free := false
	for ls := 0; ls < n; {
		le := s.eol(ls)
		line := s.src[ls:le]
		if t := strings.TrimSpace(line); strings.HasPrefix(t, ">>") || strings.HasPrefix(t, "$SET") {
			u := strings.ToUpper(t)
			if strings.Contains(u, "FREE") {
				free = true
			} else if strings.Contains(u, "FIXED") {
				free = false
			}
		}
		start := ls
		if !free {
			if len(line) > 6 && (line[6] == '*' || line[6] == '/') {
				end := le
				if end > ls+72 {
					end = ls + 72
				}
				s.blank(ls+6, end)
				ls = le + 1
				continue
			}
			start = ls + 7
			if start > le {
				start = le
			}
		}
		for k := start; k < le; {
			c := s.src[k]
			switch {
			case c == '*' && k+1 < le && s.src[k+1] == '>':
				end := le
				if !free && end > ls+72 {
					end = ls + 72 // leave the sequence area
				}
				s.blank(k, end)
				k = le
			case c == '\'' || c == '"':
				k = s.doubled(k, k, c, true)
			default:
				k++
			}
		}
		ls = le + 1
	}
}

// ---- JCL ----

// jcl masks `//*` comment lines and quoted strings (`”` escapes a quote).
func (s *scanner) jcl() {
	n := len(s.src)
	for ls := 0; ls < n; {
		le := s.eol(ls)
		if s.at(ls, "//*") {
			s.blank(ls, le)
			ls = le + 1
			continue
		}
		for k := ls; k < le; {
			if s.src[k] == '\'' {
				k = s.doubled(k, k, '\'', true)
			} else {
				k++
			}
		}
		ls = le + 1
	}
}
