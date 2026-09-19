package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

func TestKotlinInterfaceKind(t *testing.T) {
	src := `interface Greeter {
    fun greet(): String
}
`
	syms, _ := extract(t, astkit.LangKotlin, src)
	for _, sym := range syms {
		if sym.Name == "Greeter" {
			if sym.Kind != astkit.KindInterface {
				t.Fatalf("Greeter kind = %v, want interface", sym.Kind)
			}
			return
		}
	}
	t.Fatal("missing Greeter")
}

func TestKotlinSuperclassSignatureCarriesFullBaseList(t *testing.T) {
	// Grove's own edges.go parses this Signature text to split the
	// constructor-call superclass (Base()) from plain interface names.
	src := `open class Base
interface IThing
class Foo : Base(), IThing {}
`
	syms, _ := extract(t, astkit.LangKotlin, src)
	for _, sym := range syms {
		if sym.Name != "Foo" {
			continue
		}
		if sym.Signature != "class Foo : Base(), IThing" {
			t.Fatalf("Foo signature = %q", sym.Signature)
		}
		return
	}
	t.Fatal("missing Foo")
}

func TestKotlinCompanionObjectMembersAttachToEnclosingClass(t *testing.T) {
	src := `class Person {
    companion object {
        fun create(): Person = Person()
    }
}
`
	syms, _ := extract(t, astkit.LangKotlin, src)
	for _, sym := range syms {
		if sym.Name == "create" {
			if sym.ParentName != "Person" {
				t.Fatalf("create.ParentName = %q, want Person", sym.ParentName)
			}
			return
		}
	}
	t.Fatal("missing create")
}

func TestKotlinDataClassPrimaryConstructorFields(t *testing.T) {
	src := `data class Point(val x: Int, val y: Int)`
	syms, _ := extract(t, astkit.LangKotlin, src)
	fields := map[string]astkit.Symbol{}
	for _, sym := range syms {
		if sym.Kind == astkit.KindField {
			fields[sym.Name] = sym
		}
	}
	if len(fields) != 2 || fields["x"].ParentName != "Point" || fields["y"].ParentName != "Point" {
		t.Fatalf("fields = %+v", fields)
	}
}

func TestKotlinNestedClassIsQualifiedByOuter(t *testing.T) {
	src := `class Outer {
    class Inner {
        fun f() {}
    }
}
`
	syms, _ := extract(t, astkit.LangKotlin, src)
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

func TestKotlinObjectSingletonCallSiteQualifier(t *testing.T) {
	src := `object Singleton {
    fun doThing() {
        Singleton.helper()
    }
    fun helper() {}
}
`
	syms, _ := extract(t, astkit.LangKotlin, src)
	for _, sym := range syms {
		if sym.Name != "doThing" {
			continue
		}
		if len(sym.CallSites) != 1 || sym.CallSites[0].Callee != "Singleton.helper" {
			t.Fatalf("doThing call sites = %+v", sym.CallSites)
		}
		return
	}
	t.Fatal("missing doThing")
}

func TestKotlinLocalValIsNotEmittedAsField(t *testing.T) {
	src := `class Service {
    fun run() {
        val result = compute()
        println(result)
    }
    fun compute(): Int = 1
}
`
	syms, _ := extract(t, astkit.LangKotlin, src)
	for _, sym := range syms {
		if sym.Name == "result" {
			t.Fatalf("local `val result` leaked as a symbol: %+v", sym)
		}
	}
}

func TestKotlinPrivateMemberNotExported(t *testing.T) {
	src := `class Service {
    private fun helper() {}
    fun run() {}
}
`
	syms, _ := extract(t, astkit.LangKotlin, src)
	for _, sym := range syms {
		switch sym.Name {
		case "helper":
			if sym.Exported {
				t.Errorf("helper.Exported = true, want false")
			}
		case "run":
			if !sym.Exported {
				t.Errorf("run.Exported = false, want true (Kotlin default visibility is public)")
			}
		}
	}
}
