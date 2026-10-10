package textmask

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maskCase lists substrings of src that Mask must keep verbatim (code) and
// substrings it must blank entirely (comment or literal text). Each
// substring is located by its first occurrence in src.
type maskCase struct {
	name string
	lang string
	src  string
	keep []string
	gone []string
}

func checkCase(t *testing.T, fn func(lang, src string) string, tc maskCase) {
	t.Helper()
	got := fn(tc.lang, tc.src)
	if len(got) != len(tc.src) {
		t.Fatalf("%s: length %d, want %d", tc.name, len(got), len(tc.src))
	}
	for _, k := range tc.keep {
		i := strings.Index(tc.src, k)
		if i < 0 {
			t.Fatalf("%s: keep %q not in src", tc.name, k)
		}
		if got[i:i+len(k)] != k {
			t.Errorf("%s: want %q kept, got %q\n%s", tc.name, k, got[i:i+len(k)], got)
		}
	}
	for _, g := range tc.gone {
		i := strings.Index(tc.src, g)
		if i < 0 {
			t.Fatalf("%s: gone %q not in src", tc.name, g)
		}
		if strings.Trim(got[i:i+len(g)], " \r\n") != "" {
			t.Errorf("%s: want %q masked, got %q\n%s", tc.name, g, got[i:i+len(g)], got)
		}
	}
}

var maskCases = []maskCase{
	// ---- Python ----
	{name: "py single-quoted string is not a lifetime", lang: "python",
		src:  "log.info('starting up')\nself.client = Foo()\nx = 'a'\n",
		keep: []string{"log.info(", "self.client = Foo()", "x = "},
		gone: []string{"'starting up'", "'a'"}},
	{name: "py docstring with apostrophe", lang: "python",
		src:  "def f():\n    '''Don't do this: self.cache = Redis()'''\n    return g()\n",
		keep: []string{"def f():", "return g()"},
		gone: []string{"'''Don't do this: self.cache = Redis()'''"}},
	{name: "py hash in string is not a comment", lang: "python",
		src:  "a = \"x # y\"; b = c  # real\n",
		keep: []string{"a = ", "; b = c"},
		gone: []string{"\"x # y\"", "# real"}},
	{name: "py prefixes", lang: "python",
		src:  "a = rb'\\x' + BR\"y\" + u'z' + Rb'''w''' + F'{v}'\n",
		keep: []string{"a = ", " + ", "{v}"},
		gone: []string{"rb'\\x'", "BR\"y\"", "u'z'", "Rb'''w'''", "F'"}},
	{name: "py raw string escaped quote", lang: "python",
		src:  "z = r\"\\\"\" + tail()\n",
		keep: []string{"+ tail()"},
		gone: []string{"r\"\\\"\""}},
	{name: "py f-string holes", lang: "python",
		src:  "m = f\"v={label(3)} {{lit}} {x!r} {y:>10} {z:{w}}\"\n",
		keep: []string{"{label(3)}", "{x!r", "{w}"},
		gone: []string{"v=", "{{lit}}", ">10"}},
	{name: "py f-string nested quotes (3.12)", lang: "python",
		src:  "s = f\"{\", \".join(songs)}\"; t = f'{d['k']}'\n",
		keep: []string{".join(songs)}", "; t = ", "{d[", "]}"},
		gone: []string{"\", \"", "'k'"}},
	{name: "py t-string", lang: "python",
		src:  "t = t\"hi {name}\"\n",
		keep: []string{"{name}"},
		gone: []string{"t\"hi "}},
	{name: "py backslash continuation in single-quoted string", lang: "python",
		src:  "c = 'one \\\ntwo'\nd = 1\n",
		keep: []string{"d = 1"},
		gone: []string{"'one \\", "two'"}},
	{name: "py \\N{} is an escape not a hole", lang: "python",
		src:  "n = f\"\\N{EM DASH}{v}\"\n",
		keep: []string{"{v}"},
		gone: []string{"\\N{EM DASH}"}},
	{name: "py triple-quoted f-string hole across lines", lang: "python",
		src:  "m = f'''\n{compute(1,\n  2)}\n'''\n",
		keep: []string{"{compute(1,", "2)}"}},

	// ---- Rust ----
	{name: "rust raw string with hashes", lang: "rust",
		src:  "let f = r#\"\nlet client = Bar::new();\n\"#;\nlet g = 1;\n",
		keep: []string{"let f = ", "let g = 1;"},
		gone: []string{"let client = Bar::new();", "r#\"", "\"#"}},
	{name: "rust lifetimes vs char literals", lang: "rust",
		src:  "fn f<'a>(x: &'a str) -> char { '\"' }\nfn g() -> &'static str { \"s\" }\n",
		keep: []string{"fn f<'a>(x: &'a str) -> char {", "fn g() -> &'static str {"},
		gone: []string{"'\"'", "\"s\""}},
	{name: "rust char forms", lang: "rust",
		src:  "let a = ['a', '\\n', '\\u{1F600}', 'é', b'x', b'\\''];\nlet r#type = 1; 'l: loop { break 'l; }\n",
		keep: []string{"let a = [", "let r#type = 1; 'l: loop { break 'l; }"},
		gone: []string{"'a'", "'\\n'", "'\\u{1F600}'", "'é'", "b'x'", "b'\\''"}},
	{name: "rust nested block comment", lang: "rust",
		src:  "/* a /* b */ fn phantom() {} */ fn real() {}\n",
		keep: []string{"fn real() {}"},
		gone: []string{"fn phantom() {}"}},
	{name: "rust byte, raw byte, c strings", lang: "rust",
		src:  "let x = (b\"by\\\"tes\", br##\"r\"#b\"##, c\"cs\", \"multi\nline\");\n",
		keep: []string{"let x = ("},
		gone: []string{"b\"by\\\"tes\"", "br##\"r\"#b\"##", "c\"cs\"", "\"multi", "line\""}},
	{name: "rust attributes are code", lang: "rust",
		src:  "#![allow(x)]\n#[derive(Debug)]\nstruct S;\n",
		keep: []string{"#![allow(x)]", "#[derive(Debug)]"}},

	// ---- Go ----
	{name: "go raw string has no escapes", lang: "go",
		src:  "p = strings.ReplaceAll(p, `\\`, \"/\")\nq := 1\n",
		keep: []string{"p = strings.ReplaceAll(p, ", "q := 1"},
		gone: []string{"`\\`", "\"/\""}},
	{name: "go runes and multi-line raw", lang: "go",
		src:  "r := '\\''\nraw := `\nfunc phantom() {}\n`\nx := r\n",
		keep: []string{"r := ", "x := r"},
		gone: []string{"'\\''", "func phantom() {}"}},

	// ---- C / C++ / ObjC ----
	{name: "cpp digit separators", lang: "cpp",
		src:  "constexpr int k = 1'000;\nusing namespace foo;\nauto h = 0xFF'FF;\n",
		keep: []string{"constexpr int k = 1'000;", "using namespace foo;", "auto h = 0xFF'FF;"}},
	{name: "cpp raw string spanning lines", lang: "cpp",
		src:  "auto s = R\"(\nint phantom(int x) {\n)\";\nint real();\n",
		keep: []string{"auto s = ", "int real();"},
		gone: []string{"int phantom(int x) {", "R\"("}},
	{name: "cpp raw string custom delimiter and prefixes", lang: "cpp",
		src:  "auto a = R\"xy(has )\" inside)xy\"; auto b = u8R\"(u)\"; auto c = LR\"(l)\"; auto d = uR\"(x)\"; auto e = UR\"d(y)d\";\n",
		keep: []string{"auto a = ", "; auto b = ", "; auto c = ", "; auto d = ", "; auto e = "},
		gone: []string{"R\"xy(has )\" inside)xy\"", "u8R\"(u)\"", "LR\"(l)\"", "uR\"(x)\"", "UR\"d(y)d\""}},
	{name: "c error directive apostrophe", lang: "c",
		src:  "#error don't build\nint after = 1;\n",
		keep: []string{"#error don't build", "int after = 1;"}},
	{name: "c line comment continuation", lang: "c",
		src:  "// comment \\\n int phantom(int x);\nint real;\n",
		keep: []string{"int real;"},
		gone: []string{"int phantom(int x);"}},
	{name: "c apostrophe in comment", lang: "c",
		src:  "/* don't */ int a; // isn't\nint b;\n",
		keep: []string{"int a;", "int b;"},
		gone: []string{"don't", "isn't"}},
	{name: "c include and define", lang: "c",
		src:  "#include <a/b.h>\n#include \"x.h\"\n#define N 'n'\n",
		keep: []string{"#include <a/b.h>", "#include ", "#define N "},
		gone: []string{"\"x.h\"", "'n'"}},
	{name: "c string continuation", lang: "c",
		src:  "const char *s = \"a \\\nb\"; int z;\n",
		keep: []string{"int z;"},
		gone: []string{"\"a \\", "b\""}},
	{name: "objc at-string", lang: "objc",
		src:  "NSString *s = @\"hi \\\" // no\"; int x;\n",
		keep: []string{"NSString *s = ", "; int x;"},
		gone: []string{"@\"hi \\\" // no\""}},

	// ---- Java ----
	{name: "java text block", lang: "java",
		src:  "String s = \"\"\"\n  one \" two \"\" three \\\"\"\"\n  class Phantom {}\n  \"\"\";\nint x;\n",
		keep: []string{"String s = ", "int x;"},
		gone: []string{"class Phantom {}", "one \" two \"\" three \\\"\"\""}},
	{name: "java chars", lang: "java",
		src:  "char c = '\\''; char d = '\"'; int n = 1;\n",
		keep: []string{"char c = ", "; char d = ", "; int n = 1;"},
		gone: []string{"'\\''", "'\"'"}},

	// ---- C# ----
	{name: "cs attribute string", lang: "csharp",
		src:  "[Description(\"class Y : Fake\")]\nclass X {}\n",
		keep: []string{"[Description(", ")]", "class X {}"},
		gone: []string{"class Y : Fake"}},
	{name: "cs verbatim string ending in backslash", lang: "csharp",
		src:  "string p = @\"C:\\x\\\"; int after;\n",
		keep: []string{"string p = ", "; int after;"},
		gone: []string{"@\"C:\\x\\\""}},
	{name: "cs verbatim doubled quotes multi-line", lang: "csharp",
		src:  "string v = @\"one\n\"\"two\"\"\"; int k;\n",
		keep: []string{"string v = ", "; int k;"},
		gone: []string{"@\"one", "\"\"two\"\"\""}},
	{name: "cs interpolation", lang: "csharp",
		src:  "string s = $\"{a} and {{lit}} {price:N2} {Call(\"x\")}\";\n",
		keep: []string{"{a}", "{price", "{Call(", ")}"},
		gone: []string{" and {{lit}} ", "N2", "\"x\""}},
	{name: "cs verbatim interpolation both orders", lang: "csharp",
		src:  "var a = $@\"C:\\{dir}\\f\"; var b = @$\"{dir}\\x\";\n",
		keep: []string{"{dir}", "var b = "},
		gone: []string{"$@\"C:\\", "\\f\"", "\\x\""}},
	{name: "cs raw strings", lang: "csharp",
		src:  "var r = \"\"\"raw \"q\" text\"\"\"; var m = \"\"\"\"\n  has \"\"\" inside\n  \"\"\"\"; var i = $$\"\"\"{{Name}} {lit}\"\"\";\n",
		keep: []string{"var r = ", "; var m = ", "; var i = ", "{{Name}}"},
		gone: []string{"raw \"q\" text", "has \"\"\" inside", "{lit}"}},
	{name: "cs region line is code", lang: "csharp",
		src:  "#region Don't parse\nclass X {}\n#endregion\n",
		keep: []string{"#region Don't parse", "class X {}"}},

	// ---- JavaScript / TypeScript ----
	{name: "js private field and template", lang: "javascript",
		src:  "this.#tpl = `a ${b} c`;\n",
		keep: []string{"this.#tpl = ", "${b}"},
		gone: []string{"`a ", " c`"}},
	{name: "js regex with quotes", lang: "javascript",
		src:  "const re = /[\"']/g; const x = y;\n",
		keep: []string{"const re = ", "; const x = y;"},
		gone: []string{"/[\"']/g"}},
	{name: "js division is not a regex", lang: "javascript",
		src:  "const r = a / b / c; const s = 'x';\n",
		keep: []string{"const r = a / b / c; const s = "},
		gone: []string{"'x'"}},
	{name: "js nested templates", lang: "javascript",
		src:  "const n = `o ${c ? `i ${b + 1}` : 'no'} e`;\n",
		keep: []string{"${c ? ", "${b + 1}", " : "},
		gone: []string{"`o ", "`i ", "'no'", " e`"}},
	{name: "js hashbang", lang: "javascript",
		src:  "#!/usr/bin/env node\nrun();\n",
		keep: []string{"run();"},
		gone: []string{"#!/usr/bin/env node"}},
	{name: "js regex after keyword", lang: "javascript",
		src:  "return /'/.test(s) && typeof /\"/;\n",
		keep: []string{"return ", ".test(s) && typeof "},
		gone: []string{"/'/", "/\"/"}},
	{name: "ts non-null assertion before division", lang: "typescript",
		src:  "const w = `${h! / 2}px`; const q = 'x';\n",
		keep: []string{"${h! / 2}", "const q = "},
		gone: []string{"px`", "'x'"}},
	{name: "tsx self-closing tag after expression", lang: "tsx",
		src:  "const e = <C a={b} />; const s = 'x';\n",
		keep: []string{"<C a={b} />; const s = "},
		gone: []string{"'x'"}},
	{name: "jsx alias", lang: "jsx",
		src:  "const s = 'x'; // c\n",
		keep: []string{"const s = "},
		gone: []string{"'x'", "// c"}},

	// ---- PHP ----
	{name: "php attribute is not a comment", lang: "php",
		src:  "#[Route('/x')] public function act() {} # real comment\n",
		keep: []string{"#[Route(", ")] public function act() {}"},
		gone: []string{"'/x'", "# real comment"}},
	{name: "php interpolation holes", lang: "php",
		src:  "$d = \"r={$this->act(2)} v=$n w=$o->p x=$a[0] y=${z}\";\n",
		keep: []string{"$d = ", "$this->act(2)", "$n", "$o->p", "$a[0]", "z"},
		gone: []string{"r=", " v=", " w=", " x=", " y="}},
	{name: "php single quotes do not interpolate", lang: "php",
		src:  "$s = 'it\\'s {$no} #x'; $t = 1;\n",
		keep: []string{"$s = ", "; $t = 1;"},
		gone: []string{"'it\\'s {$no} #x'"}},
	{name: "php heredoc", lang: "php",
		src:  "$h = <<<EOT\nHeredoc {$this->act(3)} $n\nfunction phantom() {}\nEOT;\n$after = 1;\n",
		keep: []string{"$h = ", "$this->act(3)", "$n\n", ";\n$after = 1;"},
		gone: []string{"<<<EOT", "Heredoc ", "function phantom() {}", "EOT;"[:3]}},
	{name: "php nowdoc and indented closer", lang: "php",
		src:  "$n = <<<'NOW'\nraw {$x} $y\nNOW;\n$i = <<<\"IND\"\n    text\n    IND, 1;\n",
		keep: []string{"$n = ", ";\n$i = ", ", 1;"},
		gone: []string{"raw {$x} $y", "text"}},
	{name: "php close tag ends code", lang: "php",
		src:  "echo 1; ?>\n<p>don't parse</p>\n<?php echo 2;\n",
		keep: []string{"echo 1; ?>", "<?php echo 2;"},
		gone: []string{"<p>don't parse</p>"}},
	{name: "php line comment ends at close tag", lang: "php",
		src:  "echo 1; // c ?><b>x</b><?php f();\n",
		keep: []string{"echo 1; ", "?>", "<?php f();"},
		gone: []string{"// c ", "<b>x</b>"}},

	// ---- Kotlin ----
	{name: "kotlin templates", lang: "kotlin",
		src:  "val v = \"v=${label(3)} and $name \\\"q\\\"\"\n",
		keep: []string{"val v = ", "${label(3)}", "$name"},
		gone: []string{"\"v=", " and ", "\\\"q\\\"\""}},
	{name: "kotlin raw string with templates", lang: "kotlin",
		src:  "val r = \"\"\"\nfun phantom() {}\n${label(4)}\n\"\"\"\nval c = '\\''\n",
		keep: []string{"val r = ", "${label(4)}", "val c = "},
		gone: []string{"fun phantom() {}", "'\\''"}},
	{name: "kotlin backtick names", lang: "kotlin",
		src:  "fun `it doesn't fail`() { val s = \"x\" }\n",
		keep: []string{"fun `it doesn't fail`() { val s = "},
		gone: []string{"\"x\""}},
	{name: "kotlin nested comments", lang: "kotlin",
		src:  "/* a /* b */ fun phantom() */ fun real() {}\n",
		keep: []string{"fun real() {}"},
		gone: []string{"fun phantom()"}},

	// ---- Swift ----
	{name: "swift interpolation", lang: "swift",
		src:  "let v = \"v=\\(label(3)) \\\"q\\\"\"\n",
		keep: []string{"let v = ", "label(3)"},
		gone: []string{"\"v=", "\\\"q\\\"\""}},
	{name: "swift extended delimiters", lang: "swift",
		src:  "let r = #\"raw \"quoted\"\"#; let h = #\"\\#(label(4)) \\(no)\"#; let d = ##\"a \"# b\"##\n",
		keep: []string{"let r = ", "; let h = ", "label(4)", "; let d = "},
		gone: []string{"#\"raw \"quoted\"\"#", "\\(no)\"#", "##\"a \"# b\"##"}},
	{name: "swift multi-line string", lang: "swift",
		src:  "let m = \"\"\"\n  func phantom() {}\n  \\(label(5))\n  \"\"\"\nlet x = 1\n",
		keep: []string{"let m = ", "label(5)", "let x = 1"},
		gone: []string{"func phantom() {}"}},
	{name: "swift apostrophe is code", lang: "swift",
		src:  "let a = b' + 1\n",
		keep: []string{"let a = b' + 1"}},

	// ---- COBOL / JCL ----
	{name: "cobol fixed comment and strings", lang: "cobol",
		src:  "000100* COMMENT 'Q\n000200     DISPLAY 'IT''S'. *> INLINE\n000300     STOP RUN.\n",
		keep: []string{"000100", "000200     DISPLAY ", ".", "000300     STOP RUN."},
		gone: []string{"* COMMENT 'Q", "'IT''S'", "*> INLINE"}},
	{name: "cobol free format", lang: "cobol",
		src:  ">>SOURCE FORMAT FREE\n* not a comment\nDISPLAY \"A\"\"B\". *> c\n",
		keep: []string{"* not a comment", "DISPLAY ", "."},
		gone: []string{"\"A\"\"B\"", "*> c"}},
	{name: "jcl", lang: "jcl",
		src:  "//* COMMENT 'Q\n//S1 EXEC PGM=X,PARM='IT''S'\n",
		keep: []string{"//S1 EXEC PGM=X,PARM="},
		gone: []string{"//* COMMENT 'Q", "'IT''S'"}},
}

func TestMaskRules(t *testing.T) {
	for _, tc := range maskCases {
		t.Run(tc.name, func(t *testing.T) { checkCase(t, Mask, tc) })
	}
}

func TestMaskComments(t *testing.T) {
	cases := []maskCase{
		{name: "c include kept", lang: "c",
			src:  "#include \"x.h\" // c\n/* b */ int a;\n",
			keep: []string{"#include \"x.h\" ", "int a;"},
			gone: []string{"// c", "/* b */"}},
		{name: "comment marker inside string", lang: "python",
			src:  "s = 'a # b'  # c\n",
			keep: []string{"s = 'a # b'"},
			gone: []string{"# c"}},
		{name: "js url string", lang: "javascript",
			src:  "const u = \"http://x\"; // c\n",
			keep: []string{"const u = \"http://x\";"},
			gone: []string{"// c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkCase(t, MaskComments, tc) })
	}
}

func TestMaskFilePHP(t *testing.T) {
	src := "<html>don't</html>\n<?php $a = 'x'; ?>\n<b>t</b><?= $v ?>\n"
	checkCase(t, MaskFile, maskCase{name: "php file", lang: "php", src: src,
		keep: []string{"<?php $a = ", "; ?>", "<?= $v ?>"},
		gone: []string{"<html>don't</html>", "'x'", "<b>t</b>"}})
	// Mask starts in code mode: the same text before `<?php` is lexed as PHP.
	if got := Mask("php", "$a = 1;"); got != "$a = 1;" {
		t.Errorf("Mask php snippet = %q", got)
	}
	// MaskFile is Mask for other languages.
	if a, b := MaskFile("go", "x := `s`"), Mask("go", "x := `s`"); a != b {
		t.Errorf("MaskFile go %q != Mask %q", a, b)
	}
}

func TestMaskInactivePreprocessor(t *testing.T) {
	src := "#if 0\nint phantom();\n#if X\nint deeper();\n#endif\n#else\nint live();\n#endif\n" +
		"/* #if 0 */\nint after();\n#if (0) // off\nint off();\n#elif Y\nint on();\n#endif\n"
	got := MaskInactivePreprocessor("cpp", src)
	if len(got) != len(src) {
		t.Fatal("length changed")
	}
	for _, k := range []string{"#if 0\n", "#else\nint live();\n#endif", "int after();", "#if (0) // off", "#elif Y\nint on();"} {
		if !strings.Contains(got, k) {
			t.Errorf("want %q kept in\n%s", k, got)
		}
	}
	for _, g := range []string{"phantom", "deeper", "#if X", "int off();"} {
		if strings.Contains(got, g) {
			t.Errorf("want %q masked in\n%s", g, got)
		}
	}
	cs := "#if false\nclass Phantom {}\n#endif\nclass Real {}\n"
	if got := MaskInactivePreprocessor("csharp", cs); strings.Contains(got, "Phantom") || !strings.Contains(got, "class Real {}") {
		t.Errorf("csharp: %q", got)
	}
	if got := MaskInactivePreprocessor("go", "#if 0\nx\n"); got != "#if 0\nx\n" {
		t.Errorf("go changed: %q", got)
	}
}

func TestSupported(t *testing.T) {
	for _, l := range Languages {
		if !Supported(l) {
			t.Errorf("Supported(%q) = false", l)
		}
	}
	for _, l := range []string{"", "json", "ruby"} {
		if Supported(l) {
			t.Errorf("Supported(%q) = true", l)
		}
		if got := Mask(l, "# x 'y'"); got != "# x 'y'" {
			t.Errorf("Mask(%q) changed input: %q", l, got)
		}
	}
}

// TestMaskProperties: same length, same line breaks, idempotent, and every
// output byte is either the input byte or a space.
func TestMaskProperties(t *testing.T) {
	files, _ := filepath.Glob("testdata/*")
	inputs := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inputs[f] = string(b)
	}
	for _, tc := range maskCases {
		inputs[tc.name+" ("+tc.lang+")"] = tc.src
	}
	langFor := map[string]string{}
	for _, tc := range maskCases {
		langFor[tc.name+" ("+tc.lang+")"] = tc.lang
	}
	for name, src := range inputs {
		lang := langFor[name]
		if lang == "" {
			lang = langOfPath(name)
		}
		for _, fn := range []struct {
			name string
			f    func(string, string) string
		}{{"Mask", Mask}, {"MaskComments", MaskComments}, {"MaskFile", MaskFile}, {"MaskInactivePreprocessor", MaskInactivePreprocessor}} {
			got := fn.f(lang, src)
			if len(got) != len(src) {
				t.Errorf("%s %s: length %d want %d", fn.name, name, len(got), len(src))
				continue
			}
			for k := 0; k < len(src); k++ {
				if got[k] != src[k] && (got[k] != ' ' || src[k] == '\n' || src[k] == '\r') {
					t.Errorf("%s %s: byte %d %q -> %q", fn.name, name, k, src[k], got[k])
					break
				}
			}
			if fn.name != "MaskInactivePreprocessor" {
				if again := fn.f(lang, got); again != got {
					t.Errorf("%s %s: not idempotent", fn.name, name)
				}
			}
		}
	}
}

// TestMaskTruncated: every prefix of every testdata file masks without
// panicking (symbol snippets and unterminated constructs).
func TestMaskTruncated(t *testing.T) {
	files, _ := filepath.Glob("testdata/*")
	for _, f := range files {
		b, _ := os.ReadFile(f)
		src, lang := string(b), langOfPath(f)
		for n := 0; n <= len(src); n++ {
			if got := MaskFile(lang, src[:n]); len(got) != n {
				t.Fatalf("%s[:%d]: length %d", f, n, len(got))
			}
			if got := Mask(lang, src[n:]); len(got) != len(src)-n {
				t.Fatalf("%s[%d:]: length %d", f, n, len(got))
			}
		}
	}
}

func BenchmarkMask(b *testing.B) {
	files, _ := filepath.Glob("testdata/*")
	byLang := map[string]string{}
	for _, f := range files {
		data, _ := os.ReadFile(f)
		byLang[langOfPath(f)] += string(data) + "\n"
	}
	for _, lang := range []string{"go", "python", "javascript", "cpp", "csharp", "rust", "php"} {
		unit := byLang[lang]
		var sb strings.Builder
		for sb.Len() < 1<<20 {
			sb.WriteString(unit)
		}
		src := sb.String()
		b.Run(lang, func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			for i := 0; i < b.N; i++ {
				Mask(lang, src)
			}
		})
	}
}
