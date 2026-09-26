package strategies_test

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/provasign/astkit"
)

// jpProjected keys symbols by the qualified name grove projects:
// ParentName.Name when the extractor left QualifiedName bare.
func jpProjected(syms []astkit.Symbol) map[string][]astkit.Symbol {
	out := map[string][]astkit.Symbol{}
	for _, s := range syms {
		qn := s.QualifiedName
		if qn == s.Name && s.ParentName != "" {
			qn = s.ParentName + "." + s.Name
		}
		out[qn] = append(out[qn], s)
	}
	return out
}

// jpWant fails unless every name is extracted exactly once with the kind given.
func jpWant(t *testing.T, lang astkit.LanguageKey, src string, want map[string]astkit.SymbolKind) map[string][]astkit.Symbol {
	t.Helper()
	syms, _ := extract(t, lang, src)
	got := jpProjected(syms)
	for qn, kind := range want {
		ss := got[qn]
		if len(ss) != 1 {
			keys := make([]string, 0, len(got))
			for k, v := range got {
				keys = append(keys, k+"="+string(v[0].Kind))
			}
			sort.Strings(keys)
			t.Errorf("%s: %d symbols (want 1 %s); extracted: %s", qn, len(ss), kind, strings.Join(keys, " "))
			continue
		}
		if ss[0].Kind != kind {
			t.Errorf("%s indexed as %s, want %s", qn, ss[0].Kind, kind)
		}
	}
	return got
}

func jpOne(t *testing.T, got map[string][]astkit.Symbol, name string) astkit.Symbol {
	t.Helper()
	if len(got[name]) == 0 {
		t.Fatalf("%s not extracted", name)
	}
	return got[name][0]
}

func jpAbsent(t *testing.T, got map[string][]astkit.Symbol, names ...string) {
	t.Helper()
	for _, n := range names {
		if len(got[n]) > 0 {
			t.Errorf("%s should not be indexed: %+v", n, got[n][0])
		}
	}
}

func TestTSEnumMembers(t *testing.T) {
	got := jpWant(t, astkit.LangTypeScript, `export enum Mode { Fast, Slow = 2, "Quoted" = 3 }
const enum Flag { On = 1 }
namespace NS { export enum Inner { X } }
`, map[string]astkit.SymbolKind{
		"Mode": astkit.KindEnum, "Mode.Fast": astkit.KindConst, "Mode.Slow": astkit.KindConst,
		"Mode.Quoted": astkit.KindConst, "Flag.On": astkit.KindConst, "NS.Inner.X": astkit.KindConst,
	})
	if s := jpOne(t, got, "Mode.Fast"); !slices.Contains(s.Modifiers, "member-value") {
		t.Errorf("enum member not tagged member-value: %+v", s.Modifiers)
	}
	jpWant(t, astkit.LangTSX, `enum Size { Small, Large }`, map[string]astkit.SymbolKind{"Size.Small": astkit.KindConst})
}

func TestTSInterfaceMembers(t *testing.T) {
	got := jpWant(t, astkit.LangTypeScript, `export interface Getter<T> {
  get(k: string): T;
  timeout?: number;
  readonly id: string;
  onSave: (x: T) => void;
  [k: string]: any;
}
`, map[string]astkit.SymbolKind{
		"Getter.get": astkit.KindMethod, "Getter.timeout": astkit.KindField,
		"Getter.id": astkit.KindField, "Getter.onSave": astkit.KindField,
	})
	if s := jpOne(t, got, "Getter.timeout"); !slices.Contains(s.Modifiers, "member-value") {
		t.Errorf("interface property not tagged member-value: %+v", s.Modifiers)
	}
	if s := jpOne(t, got, "Getter.get"); !slices.Contains(s.Annotations, "declaration") {
		t.Errorf("interface method signature not annotated declaration: %+v", s.Annotations)
	}
	jpWant(t, astkit.LangTSX, `interface ButtonProps { label: string; onClick(): void }`, map[string]astkit.SymbolKind{
		"ButtonProps.label": astkit.KindField, "ButtonProps.onClick": astkit.KindMethod,
	})
}

func TestTSClassMethodOverloadSignaturesStayUnindexed(t *testing.T) {
	// method_signature in a class body is an overload declaration; the
	// implementation is the symbol. Only interface bodies get signatures.
	syms, _ := extract(t, astkit.LangTypeScript, `class A { f(a: string): void; f(a: any) {} }`)
	if n := len(jpProjected(syms)["A.f"]); n != 1 {
		t.Fatalf("A.f symbols = %d, want 1", n)
	}
}

func TestTSConstructorParameterProperties(t *testing.T) {
	got := jpWant(t, astkit.LangTypeScript, `class Store {
  constructor(public id: string, private readonly db: Db, plain: number, readonly tag?: string, @Inject() protected x?: X) {}
}
`, map[string]astkit.SymbolKind{
		"Store.id": astkit.KindField, "Store.db": astkit.KindField,
		"Store.tag": astkit.KindField, "Store.x": astkit.KindField,
	})
	jpAbsent(t, got, "Store.plain")
	if s := jpOne(t, got, "Store.db"); !slices.Contains(s.Modifiers, "private") || !slices.Contains(s.Modifiers, "readonly") {
		t.Errorf("Store.db modifiers = %v", s.Modifiers)
	}
}

func TestTSAmbientModuleAndGlobal(t *testing.T) {
	jpWant(t, astkit.LangTypeScript, `declare module "express" {
  export function helper(): void;
  interface Request { user: string }
}
declare global {
  interface Window { myGlobal: string }
}
declare function standalone(x: number): string;
declare module Legacy { function old(): void; }
`, map[string]astkit.SymbolKind{
		"helper": astkit.KindFunction, "Request": astkit.KindInterface, "Request.user": astkit.KindField,
		"Window": astkit.KindInterface, "Window.myGlobal": astkit.KindField,
		"standalone": astkit.KindFunction, "Legacy": astkit.KindNamespace, "Legacy.old": astkit.KindMethod,
	})
}

func TestJSCommonJSExports(t *testing.T) {
	got := jpWant(t, astkit.LangJavaScript, `module.exports = { a() { one(); }, b: () => two(), C: 5, alias, other: someFn };
module.exports = function build() { three(); };
module.exports = class X { run() {} };
module.exports.Y = class { go() {} };
exports.VERSION = "1";
exports.Router = Router;
exports.static = require('serve-static');
exports.helper = function () {};
`, map[string]astkit.SymbolKind{
		"a": astkit.KindFunction, "b": astkit.KindFunction, "C": astkit.KindVariable,
		"build": astkit.KindFunction, "X": astkit.KindClass, "X.run": astkit.KindMethod,
		"Y": astkit.KindClass, "Y.go": astkit.KindMethod, "VERSION": astkit.KindVariable,
		"helper": astkit.KindFunction,
	})
	jpAbsent(t, got, "exports", "Router", "static", "alias", "other")
	for _, n := range []string{"a", "build", "X", "Y", "VERSION", "C"} {
		if !jpOne(t, got, n).Exported {
			t.Errorf("%s not exported", n)
		}
	}
	if len(jpOne(t, got, "a").CallSites) != 1 || len(jpOne(t, got, "build").CallSites) != 1 {
		t.Errorf("call sites lost: a=%v build=%v", jpOne(t, got, "a").CallSites, jpOne(t, got, "build").CallSites)
	}
}

func TestJSExportDefaultObject(t *testing.T) {
	src := `export default {
  name: 'Comp',
  props: { size: { type: Number, default() { return 1 } } },
  data() { return {} },
  methods: { save() { persist() } },
};
`
	for _, lang := range []astkit.LanguageKey{astkit.LangJavaScript, astkit.LangTypeScript} {
		got := jpWant(t, lang, src, map[string]astkit.SymbolKind{
			"data": astkit.KindFunction, "methods": astkit.KindVariable, "methods.save": astkit.KindMethod,
		})
		jpAbsent(t, got, "name", "props", "props.size", "default")
	}
}

func TestJSClassExpressionBinding(t *testing.T) {
	jpWant(t, astkit.LangJavaScript, `const Anon = class { run() {} };
const Named = class Inner { go() {} };
`, map[string]astkit.SymbolKind{
		"Anon": astkit.KindClass, "Anon.run": astkit.KindMethod,
		"Named": astkit.KindClass, "Named.go": astkit.KindMethod,
	})
}

func TestJSThisAssignedFields(t *testing.T) {
	got := jpWant(t, astkit.LangJavaScript, `class Store {
  size = 1;
  constructor(cap) {
    this.cap = cap;
    this.size = 2;
    this.handle = this.handle.bind(this);
    if (cap) { this.cap = 3; this.onDone = () => { this.done = true; }; }
    items.forEach(function () { this.notMine = 1; });
  }
  handle() {}
  other() { this.late = 1; }
}
app.init = function () { this.settings = {}; this.cache = Object.create(null); };
app.handle = function () {};
Widget.prototype.setup = function () { this.ready = true; };
`, map[string]astkit.SymbolKind{
		"Store.cap": astkit.KindField, "Store.size": astkit.KindField, "Store.done": astkit.KindField,
		"Store.onDone": astkit.KindField, "Store.handle": astkit.KindMethod,
		"app.settings": astkit.KindField, "app.cache": astkit.KindField, "Widget.ready": astkit.KindField,
	})
	jpAbsent(t, got, "Store.notMine", "Store.late", "notMine")
	if s := jpOne(t, got, "Store.cap"); !slices.Contains(s.Modifiers, "member-value") {
		t.Errorf("this-field not tagged member-value: %v", s.Modifiers)
	}
}

func TestJSChainedAssignmentAndDefineGetter(t *testing.T) {
	got := jpWant(t, astkit.LangJavaScript, `req.get = req.header = function header(name) { lookup(name); };
defineGetter(req, 'protocol', function protocol() { return 1; });
Object.defineProperty(res, 'status', { get: function () { return 2; } });
Object.defineProperty(res, 'plain', { value: 3 });
fs.readFile(path, 'utf8', function () {});
`, map[string]astkit.SymbolKind{
		"req.get": astkit.KindMethod, "req.header": astkit.KindMethod,
		"req.protocol": astkit.KindMethod, "res.status": astkit.KindMethod,
	})
	jpAbsent(t, got, "path.utf8", "res.plain")
	if len(jpOne(t, got, "req.get").CallSites) != 1 {
		t.Errorf("req.get call sites = %v", jpOne(t, got, "req.get").CallSites)
	}
}

func TestPHPEnumCasesAndMembers(t *testing.T) {
	got := jpWant(t, astkit.LangPHP, `<?php
namespace App;
enum Suit: string {
    case Hearts = 'H';
    case Spades = 'S';
    const Wild = self::Spades;
    public function color(): string { return helper(); }
    public static function fromChar(string $c): self { return self::from($c); }
}
enum Status {
    const DEFAULT = 'a';
    case Active;
    case Inactive;
    public function label(): string { return 'x'; }
}
enum Plain { case One; public function two() {} }
function after() {}
class Later { public function m() {} }
`, map[string]astkit.SymbolKind{
		"Suit": astkit.KindEnum, "Suit.Hearts": astkit.KindConst, "Suit.Spades": astkit.KindConst,
		"Suit.Wild": astkit.KindConst, "Suit.color": astkit.KindMethod, "Suit.fromChar": astkit.KindMethod,
		"Status.DEFAULT": astkit.KindConst, "Status.Active": astkit.KindConst, "Status.Inactive": astkit.KindConst,
		"Status.label": astkit.KindMethod, "Plain.One": astkit.KindConst, "Plain.two": astkit.KindMethod,
		"after": astkit.KindFunction, "Later.m": astkit.KindMethod,
	})
	jpAbsent(t, got, "color", "label", "Wild", "DEFAULT", "Active")
	if s := jpOne(t, got, "Suit.color"); len(s.CallSites) != 1 {
		t.Errorf("Suit.color call sites = %v", s.CallSites)
	}
}

func TestPHPEnumSpillInBracedNamespace(t *testing.T) {
	got := jpWant(t, astkit.LangPHP, `<?php
namespace App {
enum Suit: string {
    case Hearts = 'H';
    const Wild = self::Hearts;
    case Clubs = 'C';
    public static function fromChar(string $c): self { return self::from($c); }
}
class Later { public function m() {} }
}
`, map[string]astkit.SymbolKind{
		"Suit.Hearts": astkit.KindConst, "Suit.Wild": astkit.KindConst, "Suit.Clubs": astkit.KindConst,
		"Suit.fromChar": astkit.KindMethod, "Later": astkit.KindClass, "Later.m": astkit.KindMethod,
	})
	if s := jpOne(t, got, "Suit.fromChar"); !slices.Contains(s.Modifiers, "static") {
		t.Errorf("Suit.fromChar modifiers = %v", s.Modifiers)
	}
}

func TestPHPPromotedConstructorProperties(t *testing.T) {
	got := jpWant(t, astkit.LangPHP, `<?php
class Model {
    public function __construct(private int $x, public readonly ?string $name = null, $plain = 1) {}
}
`, map[string]astkit.SymbolKind{"Model.x": astkit.KindField, "Model.name": astkit.KindField})
	jpAbsent(t, got, "Model.plain")
	if s := jpOne(t, got, "Model.name"); !slices.Contains(s.Modifiers, "readonly") || !slices.Contains(s.Modifiers, "public") {
		t.Errorf("Model.name modifiers = %v", s.Modifiers)
	}
}

func TestPHPDefineConstants(t *testing.T) {
	got := jpWant(t, astkit.LangPHP, `<?php
define('LEGACY_CONST', 1);
\define("NS_CONST", 2);
if (!defined('GUARDED')) { define('GUARDED', true); }
define($dynamic, 3);
function f() { define('IN_FUNC', 1); }
`, map[string]astkit.SymbolKind{
		"LEGACY_CONST": astkit.KindConst, "NS_CONST": astkit.KindConst, "GUARDED": astkit.KindConst,
	})
	jpAbsent(t, got, "IN_FUNC")
}
