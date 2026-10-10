package strategies_test

import (
	"strings"
	"testing"

	"github.com/provasign/astkit"
	"github.com/provasign/astkit/strategies"
)

// Comments, string literals and disabled preprocessor branches are not code:
// no symbol, call site, signature, modifier or export flag may come from
// them (parsing audit 2026-10-10, items A1-A8).

func symByQN(syms []astkit.Symbol, qn string) (astkit.Symbol, bool) {
	for _, s := range syms {
		if s.QualifiedName == qn {
			return s, true
		}
	}
	return astkit.Symbol{}, false
}

func callees(s astkit.Symbol) string {
	var out []string
	for _, c := range s.CallSites {
		out = append(out, c.Callee)
	}
	return strings.Join(out, ",")
}

func TestRustMacroArgsIgnoreCommentsAndLiterals(t *testing.T) {
	syms, _ := extract(t, astkit.LangRust, `fn f(c: char) {
    assert!(ok(), // note: phantom_one(2)
    );
    assert_eq!(c, '"', "see bar(1)", real(2));
}
`)
	f, _ := symByQN(syms, "f")
	got := "," + callees(f) + ","
	if !strings.Contains(got, ",ok,") || !strings.Contains(got, ",real,") || strings.Contains(got, "phantom_one") || strings.Contains(got, "bar") {
		t.Fatalf("f call sites = %s", got)
	}
}

func TestCMacroBodyIgnoresCommentsAndStrings(t *testing.T) {
	syms, _ := extract(t, astkit.LangC, `#define LOG(x) printf("value(%d) helper_fn(x)", x, "a,b")
#define LOG2(x) do { \
    printf("%d", (x)); /* other_fn(x) */ \
  } while (0) // other_fn(x)
`)
	log, ok := symByQN(syms, "LOG")
	if !ok || callees(log) != "printf" || strings.Join(log.CallSites[0].Args, ",") != "#String,x,#String" {
		t.Fatalf("LOG = %+v", log)
	}
	// A block comment mid-body makes the grammar emit an ERROR node; the
	// macro is still recovered.
	log2, ok := symByQN(syms, "LOG2")
	if !ok || callees(log2) != "printf" || log2.Signature != "#define LOG2(x)" || log2.Span.End != 4 {
		t.Fatalf("LOG2 = %+v", log2)
	}
}

func TestCIfZeroBranchIsNotIndexed(t *testing.T) {
	syms, _ := extract(t, astkit.LangC, "#if 0\nint dead(void);\n#elif 0\nint dead2(void);\n#else\nint live(void);\n#endif\n#if X\nint guarded(void);\n#endif\n")
	wantAbsent(t, syms, "dead", "dead2")
	for _, qn := range []string{"live", "guarded"} {
		if _, ok := symByQN(syms, qn); !ok {
			t.Errorf("%s missing: %+v", qn, syms)
		}
	}
}

func TestObjCMacroEnumMaskedAndDirectiveMembers(t *testing.T) {
	syms, _ := extract(t, astkit.LangObjC, `#import <Foundation/Foundation.h>
static NSString *k = @"typedef NS_ENUM(NSInteger, Ghost) { GhostA };";
#if 0
typedef NS_ENUM(NSInteger, Dead) { DeadA };
#endif
typedef NS_ENUM(NSInteger, Mode) {
  ModeA,
#if X
  ModeB,
#endif
  ModeC
};
`)
	wantAbsent(t, syms, "Ghost", "GhostA", "Ghost.GhostA", "Dead", "DeadA", "Dead.DeadA")
	for _, qn := range []string{"Mode", "ModeA", "ModeB", "ModeC"} {
		if _, ok := symByQN(syms, qn); !ok {
			t.Errorf("%s missing: %+v", qn, syms)
		}
	}
}

func TestSignaturesDropComments(t *testing.T) {
	cases := []struct {
		lang      astkit.LanguageKey
		src, qn   string
		signature string
	}{
		{astkit.LangObjC, "@interface M : Sup /* <FakeProto> */ <RealProto>\n@end\n", "M", "@interface M : Sup <RealProto>"},
		{astkit.LangKotlin, "class Foo(\n    val a: Int, // : Thing\n) : Base()\n", "Foo#constructor", "Foo( val a: Int, )"},
		{astkit.LangKotlin, "fun g(\n    a: Int, // : Thing\n): Int = a\n", "g", "fun g(\n    a: Int,\n): Int"},
		{astkit.LangPython, "class A(Base):  # was Fake\n    pass\n", "A", "class A(Base):"},
		{astkit.LangPython, "def g(a,  # first, really (Fake)\n      b):\n    return a\n", "g", "def g(a, b):"},
		{astkit.LangPython, "def build(\n    size,\n) -> Gadget:\n    return Gadget()\n", "build", "def build( size, ) -> Gadget:"},
		{astkit.LangPython, "def one(x): return x\n", "one", "def one(x): return x"},
		{astkit.LangCSharp, "class A {\n  [Description(\"see Fake\")] public void M() {}\n}\n", "A.M", "public void M()"},
		{astkit.LangCSharp, "class A {\n  [Obsolete(\"x\")]\n  public int P { get; set; }\n}\n", "A.P", "public int P { get; set; }"},
		{astkit.LangTypeScript, "class S {\n  attach(app /*: T */, opts: number) {}\n}\n", "S.attach", "attach(app, opts: number)"},
		{astkit.LangJava, "class A {\n  public /* hot */ static void f() {}\n}\n", "A.f", "public static void f()"},
	}
	for _, c := range cases {
		syms, _ := extract(t, c.lang, c.src)
		qn, kind, _ := strings.Cut(c.qn, "#")
		var s astkit.Symbol
		ok := false
		for _, x := range syms {
			if x.QualifiedName == qn && (kind == "" || string(x.Kind) == kind) {
				s, ok = x, true
				break
			}
		}
		if !ok {
			t.Errorf("%s %s missing: %+v", c.lang, c.qn, syms)
			continue
		}
		if s.Signature != c.signature {
			t.Errorf("%s %s signature = %q, want %q", c.lang, c.qn, s.Signature, c.signature)
		}
		for _, m := range s.Modifiers {
			if strings.Contains(m, "/*") {
				t.Errorf("%s %s modifiers = %v", c.lang, c.qn, s.Modifiers)
			}
		}
	}
}

func TestKotlinFunInterfaceNotFromComment(t *testing.T) {
	syms, _ := extract(t, astkit.LangKotlin, "// just for fun\ninterface Plain {\n    fun x(): Int\n}\nfun interface Real {\n    fun y(): Int\n}\n")
	for qn, want := range map[string]bool{"Plain": false, "Real": true} {
		s, ok := symByQN(syms, qn)
		if !ok || contains(s.Modifiers, "fun") != want {
			t.Errorf("%s = %+v, want fun modifier %v", qn, s, want)
		}
	}
}

func TestJavaExportedFromModifierKeyword(t *testing.T) {
	syms, _ := extract(t, astkit.LangJava, "class A {\n  void publication() {}\n  String mode = \"public\";\n  public int open;\n}\n")
	for qn, want := range map[string]bool{"A.publication": false, "A.mode": false, "A.open": true} {
		if s, ok := symByQN(syms, qn); !ok || s.Exported != want {
			t.Errorf("%s = %+v, want exported %v", qn, s, want)
		}
	}
}

func TestLombokAccessorsFromTypeNode(t *testing.T) {
	syms, _ := extract(t, astkit.LangJava, `import lombok.Getter;
class A {
  @Getter String kind = "boolean x";
  @Getter boolean on;
  @Getter Boolean boxed;
}
@lombok.Getter
class B {
  String name;
}
`)
	for _, qn := range []string{"A.getKind", "A.isOn", "A.getBoxed", "B.getName"} {
		if _, ok := symByQN(syms, qn); !ok {
			t.Errorf("%s missing", qn)
		}
	}
	wantAbsent(t, syms, "A.isKind", "A.isBoxed")
}

func TestCOBOLVerbsIgnoreLiteralsAndInlineComments(t *testing.T) {
	src := `       IDENTIFICATION DIVISION.
       PROGRAM-ID. PROG1.
       PROCEDURE DIVISION.
       MAIN-PARA.
           DISPLAY 'PLEASE PERFORM BACKUP FIRST'.
           MOVE 1 TO X *> PERFORM OLD-PARA
           DISPLAY 'CALL SUPPORT'.
           PERFORM REAL-PARA.
           CALL 'SUBPROG' USING X.
           CALL WS-PROG.
           STOP RUN.
       REAL-PARA.
           DISPLAY 'R'.
`
	syms, err := strategies.NewCOBOL().Extract(nil, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	main, ok := symByQN(syms, "MAIN-PARA")
	if !ok {
		t.Fatalf("MAIN-PARA missing: %+v", syms)
	}
	if got := callees(main); got != "REAL-PARA,SUBPROG,WS-PROG" {
		t.Fatalf("MAIN-PARA call sites = %s", got)
	}
}
