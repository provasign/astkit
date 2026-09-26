package strategies_test

import (
	"strings"
	"testing"

	"github.com/provasign/astkit"
)

// cSym is one extracted symbol reduced to what these tests assert on.
type cSym struct {
	kind   astkit.SymbolKind
	parent string
	mods   string
}

// cSymsByPath keys symbols by parent+"|"+name (parent "" for top level).
func cSymsByPath(t *testing.T, lang astkit.LanguageKey, src string) map[string]cSym {
	t.Helper()
	syms, _ := extract(t, lang, src)
	out := map[string]cSym{}
	for _, s := range syms {
		out[s.ParentName+"|"+s.Name] = cSym{s.Kind, s.ParentName, strings.Join(s.Modifiers, ",")}
	}
	return out
}

func wantCSyms(t *testing.T, got map[string]cSym, want map[string]astkit.SymbolKind) {
	t.Helper()
	for key, kind := range want {
		s, ok := got[key]
		if !ok {
			t.Errorf("%s not extracted; got %v", key, got)
			continue
		}
		if s.kind != kind {
			t.Errorf("%s kind = %s, want %s", key, s.kind, kind)
		}
	}
}

// `typedef int (*compare_fn)(...)` was never indexed (the name sits below a
// function declarator) and the regex fallback reported it as a function
// named `int`. It is a type alias.
func TestCFunctionPointerTypedefIsAlias(t *testing.T) {
	got := cSymsByPath(t, astkit.LangC, "typedef int (*compare_fn)(const void *, const void *);\ntypedef char *names_t[4];\n")
	wantCSyms(t, got, map[string]astkit.SymbolKind{"|compare_fn": astkit.KindType, "|names_t": astkit.KindType})
	if !strings.Contains(got["|compare_fn"].mods, "type-alias") {
		t.Errorf("compare_fn modifiers = %q, want type-alias", got["|compare_fn"].mods)
	}
}

// Enum constants: parented to their enum (tag, else typedef name); an
// anonymous enum's constants are file-scope names.
func TestCEnumConstants(t *testing.T) {
	src := "enum color { RED, GREEN = 5 };\ntypedef enum { MODE_A } mode_t2;\ntypedef enum level { LOW } level_t;\nenum { ANON_MAX = 8 };\n"
	got := cSymsByPath(t, astkit.LangC, src)
	wantCSyms(t, got, map[string]astkit.SymbolKind{
		"color|RED": astkit.KindConst, "color|GREEN": astkit.KindConst,
		"mode_t2|MODE_A": astkit.KindConst, "level|LOW": astkit.KindConst, "|ANON_MAX": astkit.KindConst,
	})
	if !strings.Contains(got["color|RED"].mods, "enum-constant") {
		t.Errorf("RED modifiers = %q, want enum-constant", got["color|RED"].mods)
	}
}

// C11 anonymous members belong to the enclosing struct; a declarator names
// an intermediate owner. `typedef struct list {...} list_t;` indexes the
// members under both names.
func TestCNestedAnonymousMembersAndTaggedTypedef(t *testing.T) {
	src := "struct node {\n  int key;\n  struct { int a; int b; } inner;\n  union { int i; float f; };\n};\ntypedef struct list { struct node *head; } list_t;\n"
	got := cSymsByPath(t, astkit.LangC, src)
	wantCSyms(t, got, map[string]astkit.SymbolKind{
		"node|key": astkit.KindField, "node|inner": astkit.KindField, "node.inner|a": astkit.KindField,
		"node.inner|b": astkit.KindField, "node|i": astkit.KindField, "node|f": astkit.KindField,
		"|list": astkit.KindStruct, "|list_t": astkit.KindStruct, "list|head": astkit.KindField,
		"list_t|head": astkit.KindField,
	})
}

func TestCppEnumClassAndAliases(t *testing.T) {
	src := "enum class Status : int { Ok = 0, Err = 1 };\nenum Color { Red };\nusing IntVec = std::vector<int>;\ntemplate <typename T> using Ptr = T*;\n"
	got := cSymsByPath(t, astkit.LangCPP, src)
	wantCSyms(t, got, map[string]astkit.SymbolKind{
		"Status|Ok": astkit.KindConst, "Status|Err": astkit.KindConst, "Color|Red": astkit.KindConst,
		"|IntVec": astkit.KindType, "|Ptr": astkit.KindType,
	})
}

// Types nested in a class, with their members; class-scope aliases;
// conversion operators and reference-returning members.
func TestCppClassNestedTypesAliasesAndOperators(t *testing.T) {
	src := `class Box {
public:
    using value_type = int;
    typedef int* pointer;
    operator bool() const { return true; }
    explicit operator int() const;
    Box& operator=(const Box& o) { return *this; }
    static Box& instance();
    template <typename U> U convert() const { return U(); }
    struct Node { int val; Node* next; };
    enum class Kind { Small, Large };
    union { int i; float f; };
    friend bool operator==(const Box&, const Box&) { return true; }
};
`
	got := cSymsByPath(t, astkit.LangCPP, src)
	wantCSyms(t, got, map[string]astkit.SymbolKind{
		"Box|value_type": astkit.KindType, "Box|pointer": astkit.KindType,
		"Box|operator bool": astkit.KindMethod, "Box|operator int": astkit.KindMethod,
		"Box|operator=": astkit.KindMethod, "Box|instance": astkit.KindMethod, "Box|convert": astkit.KindMethod,
		"Box|Node": astkit.KindStruct, "Box::Node|val": astkit.KindField, "Box::Node|next": astkit.KindField,
		"Box|Kind": astkit.KindEnum, "Box::Kind|Small": astkit.KindConst,
		"Box|i": astkit.KindField, "Box|f": astkit.KindField,
		"|operator==": astkit.KindFunction,
	})
	if _, junk := got["Box|bool"]; junk {
		t.Error("operator bool indexed as a method named bool")
	}
}

// A specialization's name is the template's own; the argument list made
// names nothing could spell and leaked into member owners.
func TestCppSpecializationUsesTemplateName(t *testing.T) {
	got := cSymsByPath(t, astkit.LangCPP, "template <> struct hash<Foo> { int f(); };\n")
	wantCSyms(t, got, map[string]astkit.SymbolKind{"|hash": astkit.KindStruct, "hash|f": astkit.KindMethod})
}

// `extern "C" int f(...) { ... }` (no braces around the linkage block) is a
// definition; the fuzz harnesses' LLVMFuzzerTestOneInput was never indexed.
func TestCppExternCSingleDefinition(t *testing.T) {
	got := cSymsByPath(t, astkit.LangCPP, "extern \"C\" int LLVMFuzzerTestOneInput(const unsigned char *data, unsigned long size)\n{\n    return 0;\n}\n")
	wantCSyms(t, got, map[string]astkit.SymbolKind{"|LLVMFuzzerTestOneInput": astkit.KindFunction})
}
