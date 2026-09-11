package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

func TestRustFullyQualifiedTraitCallKeepsConcreteType(t *testing.T) {
	src := `fn use(d: &Dog) { <Dog as Greet>::name(d); }`
	syms, _ := extract(t, astkit.LangRust, src)
	for _, sym := range syms {
		if sym.Name != "use" {
			continue
		}
		for _, site := range sym.CallSites {
			if site.Callee == "Dog.name" {
				return
			}
		}
		t.Fatalf("use call sites = %+v", sym.CallSites)
	}
	t.Fatal("missing use")
}

func TestRustInlineModulesQualifyFunctions(t *testing.T) {
	src := `mod first { pub fn shared() {} pub fn own() { shared(); } }
mod second { pub fn shared() {} }
`
	syms, _ := extract(t, astkit.LangRust, src)
	got := map[string]string{}
	for _, sym := range syms {
		got[sym.QualifiedName] = sym.ParentName
	}
	for qualified, parent := range map[string]string{
		"first": "", "first.shared": "first", "first.own": "first",
		"second": "", "second.shared": "second",
	} {
		if got[qualified] != parent {
			t.Errorf("%s parent = %q, want %q; all=%v", qualified, got[qualified], parent, got)
		}
	}
}

func TestRustNestedFunctionOwnsItsCalls(t *testing.T) {
	src := `fn helper() {}
fn outer() {
    fn inner() { helper(); }
}
`
	syms, _ := extract(t, astkit.LangRust, src)
	byQN := map[string]astkit.Symbol{}
	for _, sym := range syms {
		byQN[sym.QualifiedName] = sym
	}
	inner, ok := byQN["outer.inner"]
	if !ok || inner.Kind != astkit.KindFunction || inner.ParentName != "outer" {
		t.Fatalf("nested function = %+v; all=%+v", inner, syms)
	}
	if len(inner.CallSites) != 1 || inner.CallSites[0].Callee != "helper" {
		t.Fatalf("inner calls = %+v", inner.CallSites)
	}
	if outer := byQN["outer"]; len(outer.CallSites) != 0 {
		t.Fatalf("nested call leaked onto outer: %+v", outer.CallSites)
	}
}

func TestRustTupleStructFieldsAreNumbered(t *testing.T) {
	src := `pub struct Pair(pub String, i32);`
	syms, _ := extract(t, astkit.LangRust, src)
	fields := map[string]astkit.Symbol{}
	for _, sym := range syms {
		if sym.Kind == astkit.KindField {
			fields[sym.Name] = sym
		}
	}
	if len(fields) != 2 || fields["0"].ParentName != "Pair" || fields["1"].ParentName != "Pair" {
		t.Fatalf("tuple fields = %+v", fields)
	}
	if !fields["0"].Exported || fields["1"].Exported {
		t.Fatalf("tuple field visibility = 0:%v 1:%v", fields["0"].Exported, fields["1"].Exported)
	}
}
