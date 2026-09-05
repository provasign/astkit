package strategies_test

import (
	"strings"
	"testing"

	"github.com/provasign/astkit"
)

// These tests exercise the per-language metadata edge paths (decorators,
// type-params, field declarations, C declarations, etc.) that the basic
// fixture in strategies_test.go doesn't reach.

func TestGo_TypeParameters(t *testing.T) {
	src := `package x
func Map[T any, U comparable](in []T) []U { return nil }
`
	syms, _ := extract(t, astkit.LangGo, src)
	got := map[string][]string{}
	for _, s := range syms {
		got[s.QualifiedName] = s.TypeParameters
	}
	if len(got["Map"]) != 2 || got["Map"][0] != "T" || got["Map"][1] != "U" {
		t.Errorf("Map TypeParams=%v want [T U]", got["Map"])
	}
}

func TestGo_MultipleConstVarSpec(t *testing.T) {
	src := `package x
const (
  A = 1
  B = 2
)
var c = 3
var d = 4
`
	syms, _ := extract(t, astkit.LangGo, src)
	names := map[string]bool{}
	for _, s := range syms {
		names[s.QualifiedName] = true
	}
	for _, w := range []string{"A", "B", "c", "d"} {
		if !names[w] {
			t.Errorf("missing %q in %v", w, names)
		}
	}
}

func TestGo_PointerReceiver(t *testing.T) {
	src := `package x
type T struct{}
func (t *T) M() {}
func (T) N() {}
`
	syms, _ := extract(t, astkit.LangGo, src)
	parents := map[string]string{}
	for _, s := range syms {
		parents[s.Name] = s.ParentName
	}
	if parents["M"] != "T" || parents["N"] != "T" {
		t.Errorf("parents=%v", parents)
	}
}

func TestJS_ClassFieldsAndDecorators(t *testing.T) {
	src := `class A {
  static count = 0;
  name = "x";
  @log
  greet() { return this.name; }
}
function plain() { return 1; }
const arrow = () => 2;
async function aFn() { return 3; }
`
	syms, _ := extract(t, astkit.LangJavaScript, src)
	names := map[string]astkit.Symbol{}
	for _, s := range syms {
		names[s.Name] = s
	}
	for _, w := range []string{"A", "plain", "arrow", "aFn"} {
		if _, ok := names[w]; !ok {
			t.Errorf("missing %q in %v", w, mapKeys(names))
		}
	}
}

func TestJS_ExportPatterns(t *testing.T) {
	src := `export default function defFn() {}
export class Exported {}
export const X = 1;
export { Exported as Renamed };
`
	syms, _ := extract(t, astkit.LangJavaScript, src)
	if len(syms) == 0 {
		t.Fatal("expected symbols for export patterns")
	}
}

func TestTS_Decorators(t *testing.T) {
	src := `function log(target: any, key: string) {}

class Service {
  @log
  greet(): string { return "hi"; }
}
`
	syms, _ := extract(t, astkit.LangTypeScript, src)
	if len(syms) < 2 {
		t.Errorf("expected >=2 symbols, got %v", names(syms))
	}
}

func TestPython_Decorators(t *testing.T) {
	src := `def cache(fn): return fn

class A:
    @cache
    @staticmethod
    def m(): return 1

@cache
def f(): return 1
`
	syms, _ := extract(t, astkit.LangPython, src)
	for _, s := range syms {
		if s.Name == "f" || s.Name == "m" {
			if len(s.Annotations) == 0 {
				t.Errorf("%s missing decorators", s.Name)
			}
		}
	}
}

func TestJava_AnnotationsAndTypeParams(t *testing.T) {
	src := `package x;

@Deprecated
public class Box<T extends Number> {
  @Override
  public String toString() { return ""; }
}
`
	syms, _ := extract(t, astkit.LangJava, src)
	for _, s := range syms {
		if s.Name == "Box" {
			if len(s.TypeParameters) != 1 || s.TypeParameters[0] != "T" {
				t.Errorf("Box TypeParams=%v", s.TypeParameters)
			}
		}
	}
}

func TestRust_GenericsAndAttributes(t *testing.T) {
	src := `#[derive(Clone, Debug)]
pub struct Container<T: Clone> { v: T }

impl<T: Clone> Container<T> {
    pub fn new(v: T) -> Self { Self { v } }
}

pub trait Drawable { fn draw(&self); }
pub enum Shape { Circle, Square }
`
	syms, _ := extract(t, astkit.LangRust, src)
	names := map[string]astkit.Symbol{}
	for _, s := range syms {
		names[s.Name] = s
	}
	for _, w := range []string{"Container", "Drawable", "Shape"} {
		if _, ok := names[w]; !ok {
			t.Errorf("missing %q", w)
		}
	}
	if c := names["Container"]; len(c.TypeParameters) != 1 {
		t.Errorf("Container TypeParams=%v", c.TypeParameters)
	}
}

func TestC_StructTypedefAndDecl(t *testing.T) {
	src := `#include <stddef.h>
struct Point { int x; int y; };
typedef struct { float r; } Color;
typedef int Counter;
int globalCount = 0;
static int helper(int a) { return a; }
`
	syms, _ := extract(t, astkit.LangC, src)
	names := map[string]bool{}
	for _, s := range syms {
		names[s.Name] = true
	}
	for _, w := range []string{"Point", "Color", "Counter", "helper"} {
		if !names[w] {
			t.Errorf("missing C symbol %q in %v", w, names)
		}
	}
}

func TestCPP_Templates(t *testing.T) {
	src := `template<typename T>
class Box {
 public:
  Box(T v) : v_(v) {}
  T get() const { return v_; }
 private:
  T v_;
};

template<typename T>
T add(T a, T b) { return a + b; }
`
	syms, _ := extract(t, astkit.LangCPP, src)
	if len(syms) == 0 {
		t.Fatal("expected templates to extract")
	}
}

func TestCSharp_FieldsPropertiesGenerics(t *testing.T) {
	src := `namespace App {
  public class Container<T> where T : class {
    private T _v;
    public T Value { get; set; }
    public Container(T v) { _v = v; }
    public T Get() => _v;
  }
}
`
	syms, _ := extract(t, astkit.LangCSharp, src)
	for _, s := range syms {
		if s.Name == "Container" && s.Kind == astkit.KindClass {
			if len(s.TypeParameters) == 0 {
				t.Errorf("Container missing TypeParams: %+v", s)
			}
		}
	}
}

func TestJava_CallSitesWithNew(t *testing.T) {
	src := `class A { void f(){ new java.util.HashMap<>(); g(); } }`
	syms, _ := extract(t, astkit.LangJava, src)
	var total int
	for _, s := range syms {
		total += len(s.CallSites)
	}
	if total == 0 {
		t.Error("expected Java call sites")
	}
}

// Receivers qualifierName cannot name: a cast is typed by the cast, an array
// element by the array variable, `X.class` by Class. Each must arrive
// qualified, never bare (bare binds the caller's own same-named method).
func TestJava_CallSiteReceiverShapes(t *testing.T) {
	src := `class A {
  int f(CharSequence cs, Integer[] array, Class<?> type) {
    ((String) cs).indexOf("x");
    array[0].intValue();
    String.class.equals(type);
    return 0;
  }
}`
	syms, _ := extract(t, astkit.LangJava, src)
	got := map[string]bool{}
	for _, s := range syms {
		for _, cs := range s.CallSites {
			got[cs.Callee] = true
		}
	}
	for _, want := range []string{"String.indexOf", "array.intValue", "Class.equals"} {
		if !got[want] {
			t.Errorf("missing call site %q in %v", want, got)
		}
	}
}

// Wrapper constants and lambdas are typed argument evidence.
func TestJava_CallSiteArgMarkers(t *testing.T) {
	src := `class A {
  void f(int[] a, int v) {
    lastIndexOf(a, v, Integer.MAX_VALUE);
    run(() -> 1);
    run(A::g);
  }
}`
	syms, _ := extract(t, astkit.LangJava, src)
	var got [][]string
	for _, s := range syms {
		for _, cs := range s.CallSites {
			got = append(got, cs.Args)
		}
	}
	want := map[string]bool{"a,v,#int": true, "#lambda": true}
	for _, args := range got {
		delete(want, strings.Join(args, ","))
	}
	if len(want) != 0 {
		t.Errorf("missing arg markers %v in %v", want, got)
	}
}

// C# typed constants, lambdas and nested object creations are argument
// evidence for overload resolution.
func TestCSharp_CallSiteArgMarkers(t *testing.T) {
	src := `class A {
  void F() {
    new JValue(int.MaxValue);
    new JArray(new JValue(1), new JValue(2));
    Run(x => x);
  }
}`
	syms, _ := extract(t, astkit.LangCSharp, src)
	got := map[string]bool{}
	for _, s := range syms {
		for _, cs := range s.CallSites {
			got[cs.Callee+":"+strings.Join(cs.Args, ",")] = true
		}
	}
	for _, want := range []string{"JValue:#int", "JArray:#JValue,#JValue", "Run:#lambda"} {
		if !got[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}

// A C prototype for a function defined in the same file is not a second
// function; a prototype without a definition (a header) stays.
func TestC_PrototypeCollapsesIntoDefinition(t *testing.T) {
	src := `static int do_dump(const json_t *json, int depth);
int external_thing(int x);
static int do_dump(const json_t *json, int depth) {
    return depth;
}
`
	syms, _ := extract(t, astkit.LangC, src)
	var names []string
	for _, s := range syms {
		if s.Kind == astkit.KindFunction {
			names = append(names, s.Name)
		}
	}
	if strings.Join(names, ",") != "external_thing,do_dump" {
		t.Errorf("functions = %v, want [external_thing do_dump]", names)
	}
}

func TestCSharp_CallSiteGenericAndArgs(t *testing.T) {
	// A generic invocation must set Generic and unwrap the C# `argument`
	// wrapper so the bare identifier/literal arg classifies (it returned ""
	// before, leaving Args empty for the whole language).
	src := `class A {
  void f(string json) {
    JsonConvert.DeserializeObject<bool>(json);
    JsonConvert.DeserializeObject(json);
  }
}`
	syms, _ := extract(t, astkit.LangCSharp, src)
	var gen, plain *astkit.CallSite
	for _, s := range syms {
		for i := range s.CallSites {
			cs := &s.CallSites[i]
			if cs.Callee != "JsonConvert.DeserializeObject" {
				continue
			}
			if cs.Generic {
				gen = cs
			} else {
				plain = cs
			}
		}
	}
	if gen == nil {
		t.Fatal("missing generic DeserializeObject<bool> call site")
	}
	if plain == nil || plain.Generic {
		t.Fatal("missing non-generic DeserializeObject call site (or wrongly marked generic)")
	}
	if len(gen.Args) != 1 || gen.Args[0] != "json" {
		t.Errorf("expected unwrapped identifier arg [json], got %#v", gen.Args)
	}
}

func TestRust_CallSitesWithMacroAndPath(t *testing.T) {
	src := `fn f() { println!("hi"); std::mem::take(&mut 0); g(); }`
	syms, _ := extract(t, astkit.LangRust, src)
	var total int
	for _, s := range syms {
		total += len(s.CallSites)
	}
	if total == 0 {
		t.Error("expected Rust call sites")
	}
}

func TestGo_IdentifierNamesFromAssignSpec(t *testing.T) {
	src := `package x
var a, b int = 1, 2
var (
  c int
  d int
)
`
	syms, _ := extract(t, astkit.LangGo, src)
	got := map[string]bool{}
	for _, s := range syms {
		got[s.QualifiedName] = true
	}
	for _, w := range []string{"a", "b", "c", "d"} {
		if !got[w] {
			t.Logf("note: %q missing (extractor may not split list)", w)
		}
	}
	if len(syms) == 0 {
		t.Error("expected at least one var symbol")
	}
}

func TestCallSites_AcrossLanguages(t *testing.T) {
	cases := map[astkit.LanguageKey]string{
		astkit.LangPython: "def f():\n    return g(h(1))\n",
		astkit.LangJava:   "class A { void f(){ g(h(1)); } }\n",
		astkit.LangRust:   "fn f() { g(h(1)); }\n",
	}
	for lang, src := range cases {
		syms, _ := extract(t, lang, src)
		var totalCalls int
		for _, s := range syms {
			totalCalls += len(s.CallSites)
		}
		if totalCalls == 0 {
			t.Errorf("%s: expected call sites, got 0 (syms=%v)", lang, names(syms))
		}
	}
}

func TestJS_CallSitesMemberAndNew(t *testing.T) {
	src := `function go() {
  obj.method(1);
  new Thing(2);
  helper(3);
}
`
	syms, _ := extract(t, astkit.LangJavaScript, src)
	for _, s := range syms {
		if s.Name == "go" {
			callees := map[string]bool{}
			for _, c := range s.CallSites {
				callees[c.Callee] = true
			}
			for _, want := range []string{"obj.method", "Thing", "helper"} {
				if !callees[want] {
					t.Errorf("JS callee %q missing in %v", want, callees)
				}
			}
		}
	}
}

func TestPython_CallSitesAttr(t *testing.T) {
	src := `def f():
    obj.method(1)
    helper(2)
`
	syms, _ := extract(t, astkit.LangPython, src)
	for _, s := range syms {
		if s.Name == "f" {
			callees := map[string]bool{}
			for _, c := range s.CallSites {
				callees[c.Callee] = true
			}
			if !callees["method"] && !callees["obj.method"] {
				t.Errorf("missing method callee in %v", callees)
			}
			if !callees["helper"] {
				t.Errorf("missing helper callee in %v", callees)
			}
		}
	}
}

func TestC_TopLevelDeclarations(t *testing.T) {
	src := `extern int errno;
const char *VERSION = "1.0";
static const int MAX = 100;
int counter;
int adder(int a) { return a + counter; }
`
	syms, _ := extract(t, astkit.LangC, src)
	// Exercise the declaration-handling path; require at least the function.
	got := map[string]bool{}
	for _, s := range syms {
		got[s.Name] = true
	}
	if !got["adder"] {
		t.Errorf("expected adder function, got %v", got)
	}
}

func TestJS_FieldDef(t *testing.T) {
	src := `class A {
  prefix = "x";
  static count = 0;
  #priv = 1;
}
`
	syms, _ := extract(t, astkit.LangJavaScript, src)
	// Just verify the class extraction completes with field paths exercised.
	found := false
	for _, s := range syms {
		if s.Name == "A" {
			found = true
		}
	}
	if !found {
		t.Error("class A not extracted")
	}
}

func mapKeys(m map[string]astkit.Symbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// CallSite.Callee must honor the documented "Receiver.callee" form so graph
// consumers can do receiver-aware call resolution.
func TestCallSites_QualifiersPreserved(t *testing.T) {
	src := `package x
func (r JSON) Render(w W) {
	r.WriteContentType(w)
	json.Marshal(obj)
	c.Writer.Flush()
	c.Writer.Header().Set("k", "v")
	bare()
}`
	syms, _ := extract(t, astkit.LangGo, src)
	got := map[string]bool{}
	for _, s := range syms {
		for _, cs := range s.CallSites {
			got[cs.Callee] = true
		}
	}
	for _, want := range []string{"r.WriteContentType", "json.Marshal", "Writer.Flush", "Header().Set", "bare"} {
		if !got[want] {
			t.Errorf("missing call site %q; got %v", want, got)
		}
	}
}

func TestCallSites_QualifiersPython(t *testing.T) {
	src := "class A:\n    def f(self):\n        self.save()\n        self.db.conn.commit()\n        g()\n"
	syms, _ := extract(t, astkit.LangPython, src)
	got := map[string]bool{}
	for _, s := range syms {
		for _, cs := range s.CallSites {
			got[cs.Callee] = true
		}
	}
	for _, want := range []string{"self.save", "conn.commit", "g"} {
		if !got[want] {
			t.Errorf("missing call site %q; got %v", want, got)
		}
	}
}

func TestCallSites_QualifiersJS(t *testing.T) {
	src := "class A { f() { this.save(); res.status(200).json(x); g(); } }"
	syms, _ := extract(t, astkit.LangJavaScript, src)
	got := map[string]bool{}
	for _, s := range syms {
		for _, cs := range s.CallSites {
			got[cs.Callee] = true
		}
	}
	for _, want := range []string{"this.save", "status().json", "g"} {
		if !got[want] {
			t.Errorf("missing call site %q; got %v", want, got)
		}
	}
}

func TestPython_AttrSites(t *testing.T) {
	src := "class A:\n    def f(self):\n        x = self.request.blueprints\n        self.save()\n        return request.blueprint\n"
	syms, _ := extract(t, astkit.LangPython, src)
	gotAttrs := map[string]bool{}
	gotCalls := map[string]bool{}
	for _, s := range syms {
		for _, a := range s.AttrSites {
			gotAttrs[a.Callee] = true
		}
		for _, c := range s.CallSites {
			gotCalls[c.Callee] = true
		}
	}
	for _, want := range []string{"request.blueprints", "request.blueprint", "self.request"} {
		if !gotAttrs[want] {
			t.Errorf("missing attr site %q; got %v", want, gotAttrs)
		}
	}
	if gotAttrs["self.save"] {
		t.Errorf("call expression must not appear as attr site; got %v", gotAttrs)
	}
	if !gotCalls["self.save"] {
		t.Errorf("self.save must remain a call site; got %v", gotCalls)
	}
}

func TestTS_AbstractMethodSignature(t *testing.T) {
	src := "abstract class Base {\n  protected abstract cleanup(): void;\n  run() { this.cleanup(); }\n}\n"
	syms, _ := extract(t, astkit.LangTypeScript, src)
	for _, s := range syms {
		if s.Name == "cleanup" && s.ParentName == "Base" {
			return
		}
	}
	t.Fatalf("abstract method signature must be extracted as a symbol; got %v", names(syms))
}

func TestJS_AssignmentFunctions(t *testing.T) {
	src := "var app = {};\napp.listen = function listen(port) { return bind(port); };\nexports.render = function render(v) { return v; };\nApp.prototype.handle = function(req) { return req; };\n"
	syms, _ := extract(t, astkit.LangJavaScript, src)
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.ParentName
	}
	if _, ok := got["listen"]; !ok {
		t.Errorf("missing app.listen assignment function; got %v", got)
	}
	if got["listen"] != "app" {
		t.Errorf("listen parent = %q, want app", got["listen"])
	}
	if _, ok := got["render"]; !ok {
		t.Errorf("missing exports.render; got %v", got)
	}
	if got["handle"] != "App" {
		t.Errorf("prototype method parent = %q, want App; got %v", got["handle"], got)
	}
}

func TestJS_SuperCallSites(t *testing.T) {
	src := "class B extends A {\n  constructor(x) { super(x); }\n  close() { super.close(); this.flush(); }\n}\n"
	syms, _ := extract(t, astkit.LangTypeScript, src)
	got := map[string]bool{}
	for _, s := range syms {
		for _, c := range s.CallSites {
			got[c.Callee] = true
		}
	}
	for _, want := range []string{"super()", "super.close", "this.flush"} {
		if !got[want] {
			t.Errorf("missing call site %q; got %v", want, got)
		}
	}
}

func TestCallSites_Argc(t *testing.T) {
	src := `class A { void f(){ g(1, 2, h(3)); zero(); } }`
	syms, _ := extract(t, astkit.LangJava, src)
	got := map[string]int{}
	for _, s := range syms {
		for _, c := range s.CallSites {
			got[c.Callee] = c.Argc
		}
	}
	if got["g"] != 3 {
		t.Errorf("g argc = %d, want 3 (nested call is one argument); got %v", got["g"], got)
	}
	if got["zero"] != 0 {
		t.Errorf("zero argc = %d, want 0", got["zero"])
	}
	if got["h"] != 1 {
		t.Errorf("h argc = %d, want 1", got["h"])
	}
}

func TestJava_GenericCtorAndLiteralArgs(t *testing.T) {
	src := "class A { void f(){ Range<Integer> r = new Range<>(lo, hi); g(\"x\", 0, 2L, true, name); } }"
	syms, _ := extract(t, astkit.LangJava, src)
	var rangeOK bool
	var gArgs []string
	for _, s := range syms {
		for _, c := range s.CallSites {
			if c.Callee == "Range" {
				rangeOK = true
			}
			if c.Callee == "g" {
				gArgs = c.Args
			}
		}
	}
	if !rangeOK {
		t.Error("generic instantiation must record bare type name 'Range'")
	}
	want := []string{"#String", "#int", "#long", "#boolean", "name"}
	if len(gArgs) != 5 {
		t.Fatalf("g args = %v, want 5 entries", gArgs)
	}
	for i, w := range want {
		if gArgs[i] != w {
			t.Errorf("g arg[%d] = %q, want %q", i, gArgs[i], w)
		}
	}
}

func TestJava_ExplicitConstructorInvocation(t *testing.T) {
	// super(...) / this(...) delegation must produce call sites (they're real
	// edges in the bytecode oracle); method references must NOT be emitted
	// (a Foo::bar reference is a lambda, not a call from the enclosing method).
	src := `class A {
  A() { this(1); }
  A(int x) { super(); }
  void f(java.util.List<String> xs) { xs.forEach(String::trim); }
}`
	syms, _ := extract(t, astkit.LangJava, src)
	var super_, this_, mref int
	for _, s := range syms {
		for _, cs := range s.CallSites {
			switch cs.Callee {
			case "super()":
				super_++
			case "this()":
				this_++
			case "String.trim", "trim":
				mref++
			}
		}
	}
	if super_ == 0 || this_ == 0 {
		t.Fatalf("expected super() and this() call sites, got super=%d this=%d", super_, this_)
	}
	if mref != 0 {
		t.Errorf("method reference String::trim must not be emitted as a call (got %d)", mref)
	}
}

// A trailing line comment on a wrapped declaration header must not be glued
// into the signature by whitespace collapsing — jackson's
// ManagedReferenceProperty buried its extends clause behind "// Changed to
// extends delegating base class in 2.9", corrupting every downstream
// consumer that parses the signature.
func TestJava_HeaderCommentDoesNotCorruptSignature(t *testing.T) {
	src := `package x;

public final class ManagedReferenceProperty  // Changed to extends delegating base class in 2.9
    extends SettableBeanProperty.Delegating
{
  public void set() { }
}
`
	syms, _ := extract(t, astkit.LangJava, src)
	found := false
	for _, s := range syms {
		if s.Name == "ManagedReferenceProperty" {
			found = true
			want := "public final class ManagedReferenceProperty extends SettableBeanProperty.Delegating"
			if s.Signature != want {
				t.Errorf("Signature = %q, want %q", s.Signature, want)
			}
		}
	}
	if !found {
		t.Fatal("class not extracted")
	}
}

// A wrapped extends/implements clause (standard Prettier style for TS) must
// survive into the signature — FirstLine truncated it, dropping the whole
// inheritance clause and every downstream extends/implements edge (29 real
// typeorm classes were affected).
func TestTS_WrappedInheritanceClauseInSignature(t *testing.T) {
	src := `export class AuroraMysqlQueryRunner
    extends BaseQueryRunner
    implements QueryRunner
{
  foo(): void {}
}
`
	syms, _ := extract(t, astkit.LangTypeScript, src)
	found := false
	for _, s := range syms {
		if s.Name == "AuroraMysqlQueryRunner" {
			found = true
			for _, want := range []string{"extends BaseQueryRunner", "implements QueryRunner"} {
				if !strings.Contains(s.Signature, want) {
					t.Errorf("signature %q missing %q", s.Signature, want)
				}
			}
		}
	}
	if !found {
		t.Fatal("class not extracted")
	}
}

// A block comment abutting tokens in a declaration header must not glue them
// together after whitespace collapsing.
func TestJava_BlockCommentDoesNotGlueTokens(t *testing.T) {
	src := "package x;\n\npublic class Foo/*note*/extends Bar {\n  void m() {}\n}\n"
	syms, _ := extract(t, astkit.LangJava, src)
	for _, s := range syms {
		if s.Name == "Foo" {
			if !strings.Contains(s.Signature, "extends Bar") || strings.Contains(s.Signature, "Fooextends") {
				t.Errorf("signature glued tokens: %q", s.Signature)
			}
		}
	}
}

// Two 2-level-nested types sharing (immediate-parent, own-name) must get
// DISTINCT QualifiedNames (full ancestor path) while keeping the immediate
// ParentName — else they collapse to one and a change-impact query returns
// both unrelated scopes. Verified for Java; the same threading covers
// JS/TS, C#, and Python.
func TestJava_NestedTypeQualifiedNamePath(t *testing.T) {
	src := `public class Outer1 {
    static class Detail {
        interface Builder { void build(); }
    }
}
public class Outer2 {
    static class Detail {
        interface Builder { void build(); }
    }
}
`
	syms, _ := extract(t, astkit.LangJava, src)
	builders := map[string]string{} // qualifiedName -> parentName
	for _, s := range syms {
		if s.Name == "Builder" {
			builders[s.QualifiedName] = s.ParentName
		}
	}
	if _, ok := builders["Outer1.Detail.Builder"]; !ok {
		t.Errorf("missing full-path QualifiedName Outer1.Detail.Builder; got %v", builders)
	}
	if _, ok := builders["Outer2.Detail.Builder"]; !ok {
		t.Errorf("missing full-path QualifiedName Outer2.Detail.Builder; got %v", builders)
	}
	// ParentName stays the immediate parent (grove's ParentSymbol matching
	// keys on the simple name), NOT the full path.
	for qn, pn := range builders {
		if pn != "Detail" {
			t.Errorf("%s: ParentName = %q, want immediate parent Detail", qn, pn)
		}
	}
}
