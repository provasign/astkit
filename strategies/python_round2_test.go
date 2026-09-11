package strategies_test

import (
	"strings"
	"testing"

	"github.com/provasign/astkit"
)

func TestPythonAttributeSitesMarkWrites(t *testing.T) {
	source := `def read(p):
    return p.value
def write(p):
    p.value = 1
`
	syms, _ := extract(t, astkit.LangPython, source)
	for _, sym := range syms {
		if len(sym.AttrSites) != 1 {
			t.Fatalf("%s attr sites = %+v", sym.Name, sym.AttrSites)
		}
		if sym.Name == "read" && sym.AttrSites[0].Write {
			t.Fatal("property read marked as write")
		}
		if sym.Name == "write" && !sym.AttrSites[0].Write {
			t.Fatal("property assignment not marked as write")
		}
	}
}

func TestPythonNestedDefinitionsOwnTheirCalls(t *testing.T) {
	source := `def outer():
    before()
    def inner():
        inside()
    class Local:
        def method(self):
            in_method()
    after()
`
	syms, _ := extract(t, astkit.LangPython, source)
	byQualified := map[string]astkit.Symbol{}
	for _, sym := range syms {
		byQualified[sym.QualifiedName] = sym
	}
	for _, name := range []string{"outer", "outer.inner", "outer.Local", "outer.Local.method"} {
		if _, ok := byQualified[name]; !ok {
			t.Fatalf("missing nested symbol %q in %+v", name, syms)
		}
	}
	hasCall := func(sym astkit.Symbol, name string) bool {
		for _, call := range sym.CallSites {
			if call.Callee == name {
				return true
			}
		}
		return false
	}
	outer := byQualified["outer"]
	if !hasCall(outer, "before") || !hasCall(outer, "after") || hasCall(outer, "inside") || hasCall(outer, "in_method") {
		t.Fatalf("outer calls = %+v", outer.CallSites)
	}
	if !hasCall(byQualified["outer.inner"], "inside") {
		t.Fatalf("inner calls = %+v", byQualified["outer.inner"].CallSites)
	}
	if !hasCall(byQualified["outer.Local.method"], "in_method") {
		t.Fatalf("local method calls = %+v", byQualified["outer.Local.method"].CallSites)
	}
}

func TestPythonTopLevelCallsBecomeSyntheticSymbol(t *testing.T) {
	source := `app = Flask(__name__)
app.register(make_handler())

def declared():
    nested_only()
`
	syms, _ := extract(t, astkit.LangPython, source)
	for _, sym := range syms {
		if sym.Name != "<top-level>" {
			continue
		}
		got := map[string]bool{}
		for _, call := range sym.CallSites {
			got[call.Callee] = true
		}
		if !got["Flask"] || !got["app.register"] || !got["make_handler"] || got["nested_only"] {
			t.Fatalf("top-level calls = %+v", sym.CallSites)
		}
		if strings.Contains(sym.Body, "nested_only") || !strings.Contains(sym.Body, "register") || sym.Span.Start != 1 {
			t.Fatalf("top-level masked body/span = %q %+v", sym.Body, sym.Span)
		}
		return
	}
	t.Fatalf("missing Python <top-level> symbol in %+v", syms)
}

func TestPythonCallSitePreservesFullModuleQualifier(t *testing.T) {
	syms, _ := extract(t, astkit.LangPython, "import lib.engine\ndef make():\n    return lib.engine.Engine()\n")
	for _, sym := range syms {
		if sym.Name == "make" {
			if len(sym.CallSites) != 1 || sym.CallSites[0].Callee != "lib.engine.Engine" {
				t.Fatalf("qualified call sites = %+v", sym.CallSites)
			}
			return
		}
	}
	t.Fatal("missing make symbol")
}
