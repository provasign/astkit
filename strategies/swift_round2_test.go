package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

func TestSwiftExtensionAddsProtocolConformanceAnnotation(t *testing.T) {
	src := `class Person {}

protocol Greeter {
    func greet()
}

extension Person: Greeter {
    func greet() {}
}
`
	syms, _ := extract(t, astkit.LangSwift, src)
	for _, sym := range syms {
		if sym.Name != "Person" || sym.Kind != astkit.KindClass {
			continue
		}
		for _, a := range sym.Annotations {
			if a == "implements:Greeter" {
				return
			}
		}
		t.Fatalf("Person annotations = %v, want implements:Greeter", sym.Annotations)
	}
	t.Fatal("missing Person symbol")
}

func TestSwiftExtensionMembersAttachToExtendedType(t *testing.T) {
	src := `class Person {}

extension Person {
    func birthday() {}
}
`
	syms, _ := extract(t, astkit.LangSwift, src)
	for _, sym := range syms {
		if sym.Name == "birthday" {
			if sym.Kind != astkit.KindMethod || sym.ParentName != "Person" {
				t.Fatalf("birthday = %+v, want method parented to Person", sym)
			}
			return
		}
	}
	t.Fatal("missing birthday")
}

func TestSwiftSelfCallKeepsExplicitQualifier(t *testing.T) {
	src := `class Person {
    func greet() {
        self.helper()
    }
    func helper() {}
}
`
	syms, _ := extract(t, astkit.LangSwift, src)
	for _, sym := range syms {
		if sym.Name != "greet" {
			continue
		}
		if len(sym.CallSites) != 1 || sym.CallSites[0].Callee != "self.helper" {
			t.Fatalf("greet call sites = %+v, want self.helper", sym.CallSites)
		}
		return
	}
	t.Fatal("missing greet")
}

func TestSwiftNestedTypeIsQualifiedByOuter(t *testing.T) {
	src := `class Outer {
    class Inner {
        func f() {}
    }
}
`
	syms, _ := extract(t, astkit.LangSwift, src)
	byName := map[string]astkit.Symbol{}
	for _, sym := range syms {
		byName[sym.Name] = sym
	}
	inner, ok := byName["Inner"]
	if !ok || inner.ParentName != "Outer" || inner.QualifiedName != "Outer.Inner" {
		t.Fatalf("Inner = %+v", inner)
	}
	f, ok := byName["f"]
	if !ok || f.ParentName != "Inner" {
		t.Fatalf("f = %+v, want parented to Inner", f)
	}
}

func TestSwiftStructEnumActorKinds(t *testing.T) {
	src := `struct Point { var x: Int }
enum Direction { case north, south }
actor Counter { func increment() {} }
`
	syms, _ := extract(t, astkit.LangSwift, src)
	byName := map[string]astkit.Symbol{}
	for _, sym := range syms {
		byName[sym.Name] = sym
	}
	if p, ok := byName["Point"]; !ok || p.Kind != astkit.KindStruct {
		t.Errorf("Point = %+v, want struct", p)
	}
	if d, ok := byName["Direction"]; !ok || d.Kind != astkit.KindEnum {
		t.Errorf("Direction = %+v, want enum", d)
	}
	if n, ok := byName["north"]; !ok || n.Kind != astkit.KindConst || n.ParentName != "Direction" {
		t.Errorf("north = %+v, want const parented to Direction", n)
	}
	c, ok := byName["Counter"]
	if !ok || c.Kind != astkit.KindClass {
		t.Fatalf("Counter = %+v, want class kind (actor is a modifier)", c)
	}
	found := false
	for _, m := range c.Modifiers {
		if m == "actor" {
			found = true
		}
	}
	if !found {
		t.Errorf("Counter modifiers = %v, want actor", c.Modifiers)
	}
}

func TestSwiftLocalLetIsNotEmittedAsField(t *testing.T) {
	src := `class Service {
    func run() {
        let result = compute()
        print(result)
    }
    func compute() -> Int { return 1 }
}
`
	syms, _ := extract(t, astkit.LangSwift, src)
	for _, sym := range syms {
		if sym.Name == "result" {
			t.Fatalf("local `let result` leaked as a symbol: %+v", sym)
		}
	}
}
