package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

// Each call site carries the 1-based column of the called name: an edit
// list can then name one token, even with two same-named calls on a line.
// A name inside a string is not a call.
func TestCallSitesCarryTheNameColumn(t *testing.T) {
	for _, c := range []struct {
		lang astkit.LanguageKey
		src  string
		fn   string
		want map[string][]int // callee -> columns
	}{
		{astkit.LangPython, "def f(w):\n    w.value(\"value(x)\"); w.value(2)\n", "f", map[string][]int{"w.value": {7, 28}}},
		{astkit.LangGo, "package p\n\nfunc f(w W) {\n\tw.Value(1)\n}\n", "f", map[string][]int{"w.Value": {4}}},
		{astkit.LangJava, "class C {\n  void f(W w) { w.value(\"a\"); w.value(2L); }\n}\n", "C.f", map[string][]int{"w.value": {19, 33}}},
		{astkit.LangRust, "fn f() {\n    println!(\"x\");\n    helper(1);\n}\n", "f", map[string][]int{"helper": {5}}},
	} {
		syms, _ := extract(t, c.lang, c.src)
		var got map[string][]int
		for _, s := range syms {
			if s.QualifiedName == c.fn || s.Name == c.fn {
				got = map[string][]int{}
				for _, cs := range s.CallSites {
					got[cs.Callee] = append(got[cs.Callee], cs.Col)
				}
			}
		}
		for callee, cols := range c.want {
			if len(got[callee]) != len(cols) {
				t.Errorf("%s %s: columns %v, want %v (all %v)", c.lang, callee, got[callee], cols, got)
				continue
			}
			for i := range cols {
				if got[callee][i] != cols[i] {
					t.Errorf("%s %s: columns %v, want %v", c.lang, callee, got[callee], cols)
				}
			}
		}
	}
}

// A TS receiver behind a non-null assertion or parentheses is kept, and a
// receiver no name describes is "<expr>": neither is a bare call.
func TestTSCallReceiversThroughNonNullAndSubscript(t *testing.T) {
	src := "class S {\n  f() {\n    this.m.get(1)!.createChild(2);\n    (this.x).run();\n    this.clients[3].close(true);\n  }\n}\n"
	syms, _ := extract(t, astkit.LangTypeScript, src)
	got := map[string]bool{}
	for _, s := range syms {
		if s.QualifiedName == "S.f" {
			for _, cs := range s.CallSites {
				got[cs.Callee] = true
			}
		}
	}
	for _, want := range []string{"get().createChild", "x.run", "<expr>.close"} {
		if !got[want] {
			t.Errorf("missing call site %q in %v", want, got)
		}
	}
}

// C#: x!.M() keeps its receiver; a?.M() is a call with receiver a.
func TestCSharpNullForgivingAndConditionalAccess(t *testing.T) {
	src := "class S {\n  void F() {\n    TraceWriter!.Trace(1);\n    ((JObject?)Parent)?.Changed(this);\n    item?.Run();\n  }\n}\n"
	syms, _ := extract(t, astkit.LangCSharp, src)
	got := map[string]bool{}
	for _, s := range syms {
		if s.Name == "F" {
			for _, cs := range s.CallSites {
				got[cs.Callee] = true
			}
		}
	}
	for _, want := range []string{"TraceWriter.Trace", "JObject.Changed", "item.Run"} {
		if !got[want] {
			t.Errorf("missing call site %q in %v", want, got)
		}
	}
}
