package strategies_test

import (
	"reflect"
	"testing"

	"github.com/provasign/astkit"
)

func swiftSymbol(t *testing.T, src, name string, line int) astkit.Symbol {
	t.Helper()
	syms, _ := extract(t, astkit.LangSwift, src)
	for _, s := range syms {
		if s.Name == name && (line == 0 || s.Span.Start == line) {
			return s
		}
	}
	t.Fatalf("missing %s (line %d) in %+v", name, line, syms)
	return astkit.Symbol{}
}

func TestSwiftConstructorIsNamedAfterItsType(t *testing.T) {
	src := `struct JSON {
    init(_ object: Any) {}
    init(parseJSON s: String) {
        self.init(s)
    }
}
`
	syms, _ := extract(t, astkit.LangSwift, src)
	var ctors int
	for _, s := range syms {
		if s.Kind == astkit.KindConstructor {
			ctors++
			if s.Name != "JSON" || s.ParentName != "JSON" {
				t.Fatalf("constructor = %+v, want Name/ParentName JSON", s)
			}
		}
	}
	if ctors != 2 {
		t.Fatalf("constructors = %d, want 2", ctors)
	}
}

func TestSwiftCallArgsCarryLabels(t *testing.T) {
	src := `struct P {
    init(data: Int, options o: Int = 0) {}
    init(_ v: Int) {}
    func f() {
        self.init(data: 1, options: x)
        self.init(2)
        let p = P(data: y)
    }
}
`
	f := swiftSymbol(t, src, "f", 0)
	var got [][]string
	for _, cs := range f.CallSites {
		got = append(got, append([]string{cs.Callee}, cs.Args...))
	}
	want := [][]string{
		{"self.init", "data:#int", "options:x"},
		{"self.init", "_:#int"},
		{"P", "data:y"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call sites = %v, want %v", got, want)
	}
}

func TestSwiftSubscriptAccessIsACallToSubscript(t *testing.T) {
	src := `struct J {
    subscript(path: [Int]) -> J {
        get { return self[key] }
        set { helper() }
    }
    func f() {
        let a = self[1]
        let b = other[key]
        let c = x.y[2]
    }
}
`
	f := swiftSymbol(t, src, "f", 0)
	var got []string
	for _, cs := range f.CallSites {
		got = append(got, cs.Callee)
	}
	if want := []string{"self.subscript", "other.subscript", "y.subscript"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("f call sites = %v, want %v", got, want)
	}
	// A subscript declaration's accessor bodies are callers too.
	sub := swiftSymbol(t, src, "subscript", 0)
	got = nil
	for _, cs := range sub.CallSites {
		got = append(got, cs.Callee)
	}
	if want := []string{"self.subscript", "helper"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("subscript call sites = %v, want %v", got, want)
	}
}
