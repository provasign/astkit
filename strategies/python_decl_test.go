package strategies_test

import (
	"slices"
	"testing"

	"github.com/provasign/astkit"
)

func TestPythonModuleAssignmentsAreIndexed(t *testing.T) {
	src := `import os.path
from typing import Optional as Opt
try:
    import json
except ImportError:
    json = None
    HAS_JSON = False
else:
    HAS_JSON = True

MAX_RETRIES = 3
__version__ = "1.0"
Alias = dict[str, int]
square = lambda x: x * x
a, b = 1, 2
x = y = 0
obj.attr = 5
items[0] = 1
_default_text_stdout = _make_cached_stream_func(lambda: sys.stdout)
MAX_RETRIES = 4
if CONDITION:
    CONDITIONAL = 1

def helper():
    local = 1
    return local

helper = wrap(helper)

class K:
    attr = 1
`
	syms, _ := extract(t, astkit.LangPython, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{
		"MAX_RETRIES": astkit.KindVariable, "__version__": astkit.KindVariable, "Alias": astkit.KindVariable,
		"square": astkit.KindFunction, "a": astkit.KindVariable, "b": astkit.KindVariable,
		"x": astkit.KindVariable, "y": astkit.KindVariable, "_default_text_stdout": astkit.KindVariable,
		"HAS_JSON": astkit.KindVariable, "CONDITIONAL": astkit.KindVariable,
		"helper": astkit.KindFunction, "K.attr": astkit.KindField,
	})
	count := map[string]int{}
	for _, s := range syms {
		count[s.QualifiedName]++
		switch s.QualifiedName {
		case "json", "local", "obj", "attr", "items", "obj.attr", "os", "Opt":
			t.Errorf("%s should not be indexed: %+v", s.QualifiedName, s)
		}
		if s.QualifiedName == "MAX_RETRIES" && s.Span.Start != 11 {
			t.Errorf("MAX_RETRIES should be its first binding, got line %d", s.Span.Start)
		}
		if s.QualifiedName == "a" && !slices.Contains(s.Modifiers, "module-value") {
			t.Errorf("a modifiers = %v", s.Modifiers)
		}
	}
	for _, qn := range []string{"MAX_RETRIES", "HAS_JSON", "helper"} {
		if count[qn] != 1 {
			t.Errorf("%s indexed %d times", qn, count[qn])
		}
	}
}

func TestPythonInitInstanceAttributesAreFields(t *testing.T) {
	src := `class Service:
    registry = {}

    def __init__(self, db, *args):
        self.db = db
        self._cache: dict = {}
        self.a, self.b = 1, 2
        if args:
            self.extra = args
        self.db = None
        self.registry = {}
        other.x = 1
        self.items[0] = 1

        def inner(self):
            self.leak = 1

    def run(self):
        self.later = 1

    class Meta:
        def __init__(this):
            this.ordering = []
`
	syms, _ := extract(t, astkit.LangPython, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{
		"Service.db": astkit.KindField, "Service._cache": astkit.KindField, "Service.a": astkit.KindField,
		"Service.b": astkit.KindField, "Service.extra": astkit.KindField, "Service.registry": astkit.KindField,
		"Service.Meta.ordering": astkit.KindField,
	})
	count := map[string]int{}
	for _, s := range syms {
		count[s.QualifiedName]++
		switch s.Name {
		case "leak", "later", "x", "items":
			t.Errorf("%s should not be indexed: %+v", s.QualifiedName, s)
		}
		if s.QualifiedName == "Service.db" && (s.ParentName != "Service" || s.Span.Start != 5) {
			t.Errorf("Service.db = %+v", s)
		}
		if s.QualifiedName == "Service._cache" && s.Signature != "_cache: dict" {
			t.Errorf("Service._cache signature = %q", s.Signature)
		}
		if s.QualifiedName == "Service.Meta.ordering" && s.ParentName != "Meta" {
			t.Errorf("ordering parent = %q", s.ParentName)
		}
	}
	for _, qn := range []string{"Service.db", "Service.registry"} {
		if count[qn] != 1 {
			t.Errorf("%s indexed %d times", qn, count[qn])
		}
	}
}
