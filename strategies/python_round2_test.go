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

// A call on a container element keeps its receiver: without the "[]" marker
// self._converters[k].to_url() read as a bare to_url() and bound no method.
func TestPythonSubscriptReceiverKeepsQualifier(t *testing.T) {
	source := `class Rule:
    def build(self, k, v):
        return self._converters[k].to_url(v)
`
	syms, _ := extract(t, astkit.LangPython, source)
	for _, sym := range syms {
		if sym.Name != "build" {
			continue
		}
		for _, cs := range sym.CallSites {
			if cs.Callee == "self._converters[].to_url" {
				return
			}
		}
		t.Fatalf("call sites = %+v", sym.CallSites)
	}
	t.Fatal("build not extracted")
}

// Decorators run when the definition is evaluated, so they are calls made by
// the ENCLOSING scope: the module, the class body, or the function that holds
// the definition (a dynamic trace attributes `@setupmethod` on a method to the
// class). click's `argument` showed 2 callers against 143 `@click.argument(`
// uses before decorators were call sites at all.
func TestPythonDecoratorsAreCallsOfTheEnclosingScope(t *testing.T) {
	source := `import click

@click.command()
@click.argument("name", type=click.Path())
@click.version_option(version="1.0")
def cli(name):
    click.echo(name)

class Repo:
    @property
    def path(self):
        return self._p

@dataclass
class Point:
    x: int = field(default=0)

def factory(f):
    @functools.wraps(f)
    def inner():
        return f()
    return inner
`
	syms, _ := extract(t, astkit.LangPython, source)
	got := map[string]map[string]astkit.CallSite{}
	for _, sym := range syms {
		got[sym.Name] = map[string]astkit.CallSite{}
		for _, cs := range sym.CallSites {
			got[sym.Name][cs.Callee] = cs
		}
	}
	for _, want := range []struct {
		sym, callee string
		line, argc  int
	}{
		{"<top-level>", "click.command", 3, 0},
		{"<top-level>", "click.argument", 4, 2},
		{"<top-level>", "click.Path", 4, 0}, // a call inside decorator arguments
		{"<top-level>", "click.version_option", 5, 1},
		{"<top-level>", "dataclass", 14, 1}, // bare class decorator: called with the class
		{"cli", "click.echo", 7, 1},
		{"Repo", "property", 10, 1}, // method decorators run in the class body
		{"Point", "field", 16, 1},   // so do other class-body statements
		{"factory", "functools.wraps", 19, 1},
		{"inner", "f", 21, 0},
	} {
		cs, ok := got[want.sym][want.callee]
		if !ok || cs.Line != want.line || cs.Argc != want.argc {
			t.Errorf("%s: call %q = %+v (found %v), want line %d argc %d; all = %+v",
				want.sym, want.callee, cs, ok, want.line, want.argc, got[want.sym])
		}
	}
	// The decorated symbols do not make their decorators' calls.
	for sym, callee := range map[string]string{"cli": "click.argument", "path": "property", "inner": "functools.wraps", "Point": "dataclass"} {
		if _, ok := got[sym][callee]; ok {
			t.Errorf("%s must not own its decorator call %q: %+v", sym, callee, got[sym])
		}
	}
}
