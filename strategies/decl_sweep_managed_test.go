package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

// Declaration-sweep gaps (2026-09-26) for C#, Objective-C, Java, Kotlin and
// Swift: declarations that were not indexed, or indexed under a wrong name
// or kind. Each test pins one gap.

// symKinds maps QualifiedName (joined with ParentName the way Grove projects
// it when the extractor left it bare) to every kind emitted under it.
func symKinds(syms []astkit.Symbol) map[string][]astkit.SymbolKind {
	out := map[string][]astkit.SymbolKind{}
	for _, s := range syms {
		qn := s.QualifiedName
		if s.ParentName != "" && qn == s.Name {
			qn = s.ParentName + "." + s.Name
		}
		out[qn] = append(out[qn], s.Kind)
	}
	return out
}

func wantKinds(t *testing.T, syms []astkit.Symbol, want map[string]astkit.SymbolKind) {
	t.Helper()
	got := symKinds(syms)
	for qn, kind := range want {
		kinds, ok := got[qn]
		if !ok {
			t.Errorf("%s not indexed (want %s); got %v", qn, kind, got)
			continue
		}
		found := false
		for _, k := range kinds {
			found = found || k == kind
		}
		if !found {
			t.Errorf("%s indexed as %v, want %s", qn, kinds, kind)
		}
	}
}

func wantAbsent(t *testing.T, syms []astkit.Symbol, qns ...string) {
	t.Helper()
	got := symKinds(syms)
	for _, qn := range qns {
		if kinds, ok := got[qn]; ok {
			t.Errorf("%s indexed as %v, want absent", qn, kinds)
		}
	}
}

func TestCSharpEnumMembers(t *testing.T) {
	syms, _ := extract(t, astkit.LangCSharp, `class Store { public enum Mode { Fast, Slow = 2 } }`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Store.Mode.Fast": astkit.KindConst, "Store.Mode.Slow": astkit.KindConst,
	})
}

func TestCSharpRecordPositionalProperties(t *testing.T) {
	syms, _ := extract(t, astkit.LangCSharp, `public record Person(string First, string Last);
public record struct Coord(int X, int Y) { public int Z => X; }
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Person.First": astkit.KindField, "Person.Last": astkit.KindField,
		"Coord.X": astkit.KindField, "Coord.Y": astkit.KindField, "Coord.Z": astkit.KindField,
	})
}

func TestCSharpEvents(t *testing.T) {
	syms, _ := extract(t, astkit.LangCSharp, `interface IRepo { event EventHandler Changed; }
class Store {
    public event EventHandler Changed;
    public event EventHandler OnNotify { add { } remove { } }
}
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"IRepo.Changed": astkit.KindField, "Store.Changed": astkit.KindField, "Store.OnNotify": astkit.KindField,
	})
	for _, s := range syms {
		if s.Name == "OnNotify" && s.Signature != "public event EventHandler OnNotify" {
			t.Errorf("OnNotify signature = %q", s.Signature)
		}
	}
}

func TestCSharpOperators(t *testing.T) {
	syms, _ := extract(t, astkit.LangCSharp, `class Store {
    public static Store operator +(Store a, Store b) => a;
    public static implicit operator int(Store s) => 0;
    public static explicit operator Store(int s) => null;
}
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Store.operator +":              astkit.KindMethod,
		"Store.implicit operator int":   astkit.KindMethod,
		"Store.explicit operator Store": astkit.KindMethod,
	})
}

func TestCSharpFinalizerIsNotTheConstructor(t *testing.T) {
	syms, _ := extract(t, astkit.LangCSharp, `class Store {
    public Store() { Init(); }
    ~Store() { Cleanup(); }
}
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Store.Store": astkit.KindConstructor, "Store.~Store": astkit.KindMethod,
	})
	if kinds := symKinds(syms)["Store.Store"]; len(kinds) != 1 {
		t.Errorf("Store.Store emitted %d times (%v), want only the constructor", len(kinds), kinds)
	}
}

func TestCSharpDelegates(t *testing.T) {
	syms, _ := extract(t, astkit.LangCSharp, `public delegate void Notify(string msg);
class Store { delegate int Inner(int x); }
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Notify": astkit.KindType, "Store.Inner": astkit.KindType,
	})
}

func TestObjCMacroEnums(t *testing.T) {
	src := `typedef NS_ENUM(NSInteger, Mode) {
    ModeFast,
    ModeSlow = 2
};
typedef NS_OPTIONS(NSUInteger, Flags) {
    FlagsNone = 0,
    FlagsA NS_SWIFT_NAME(a) = 1 << 0, // trailing, comment
    /* FlagsGhost, */
    FlagsB = (1 << 1),
};
`
	syms, _ := extract(t, astkit.LangObjC, src)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Mode": astkit.KindEnum, "Mode.ModeFast": astkit.KindConst, "Mode.ModeSlow": astkit.KindConst,
		"Flags": astkit.KindEnum, "Flags.FlagsNone": astkit.KindConst, "Flags.FlagsA": astkit.KindConst,
		"Flags.FlagsB": astkit.KindConst,
	})
	wantAbsent(t, syms, "ModeFast", "ModeSlow", "Flags.FlagsGhost", "Flags.NS_SWIFT_NAME")
	for _, s := range syms {
		if s.Name == "ModeSlow" && s.Span.Start != 3 {
			t.Errorf("ModeSlow line = %d, want 3", s.Span.Start)
		}
	}
}

func TestObjCProtocolMembers(t *testing.T) {
	src := `@protocol Fetching <NSObject>
@required
- (void)fetchWith:(NSString *)url completion:(id)done;
@optional
@property (nonatomic, readonly) NSInteger retries;
+ (instancetype)sharedFetcher;
@end
`
	syms, _ := extract(t, astkit.LangObjC, src)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Fetching.fetchWith:completion:": astkit.KindMethod,
		"Fetching.retries":               astkit.KindField,
		"Fetching.sharedFetcher":         astkit.KindMethod,
	})
}

func TestObjCBlockTypedefs(t *testing.T) {
	syms, _ := extract(t, astkit.LangObjC, `typedef void (^Handler)(int);
typedef id (^Maker)(void);
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{"Handler": astkit.KindType, "Maker": astkit.KindType})
}

func TestJavaInterfaceConstants(t *testing.T) {
	syms, _ := extract(t, astkit.LangJava, `interface Shape { double PI_APPROX = 3.14, E = 2.7; double area(); }`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Shape.PI_APPROX": astkit.KindField, "Shape.E": astkit.KindField,
	})
}

func TestJavaAnnotationElements(t *testing.T) {
	syms, _ := extract(t, astkit.LangJava, `@interface Audited { String value(); int level() default 1; int MAX = 3; }`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Audited.value": astkit.KindMethod, "Audited.level": astkit.KindMethod, "Audited.MAX": astkit.KindField,
	})
}

func TestJavaRecordCompactConstructor(t *testing.T) {
	syms, _ := extract(t, astkit.LangJava, `record Point(int x, int y) {
    Point { if (x < 0) throw new IllegalArgumentException(); check(x); }
}
`)
	var ctor *astkit.Symbol
	for i := range syms {
		if syms[i].Kind == astkit.KindConstructor && syms[i].QualifiedName == "Point.Point" {
			ctor = &syms[i]
		}
	}
	if ctor == nil {
		t.Fatalf("compact constructor not indexed; got %v", symKinds(syms))
	}
	if ctor.Signature != "Point(int x, int y)" {
		t.Errorf("compact constructor signature = %q, want the record header's parameters", ctor.Signature)
	}
	if len(ctor.CallSites) == 0 {
		t.Errorf("compact constructor has no call sites")
	}
}

func TestKotlinTypeAlias(t *testing.T) {
	syms, _ := extract(t, astkit.LangKotlin, `typealias Callback = (String) -> Unit
private typealias Pair2<K> = Map<K, K>
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{"Callback": astkit.KindType, "Pair2": astkit.KindType})
}

func TestKotlinFunInterface(t *testing.T) {
	src := `fun interface Handler { fun handle(x: Int): Int }
public fun interface Pred<T> : Base {
    fun test(t: T): Boolean
}
fun after() = 1
`
	syms, _ := extract(t, astkit.LangKotlin, src)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Handler": astkit.KindInterface, "Handler.handle": astkit.KindMethod,
		"Pred": astkit.KindInterface, "Pred.test": astkit.KindMethod, "after": astkit.KindFunction,
	})
	wantAbsent(t, syms, "handle", "test")
	for _, s := range syms {
		if s.Name == "Pred" && s.Signature != "public fun interface Pred<T> : Base" {
			t.Errorf("Pred signature = %q (node text must come from the original source)", s.Signature)
		}
		if s.Kind == astkit.KindInterface && !contains(s.Modifiers, "fun") {
			t.Errorf("%s modifiers = %v, want fun", s.Name, s.Modifiers)
		}
	}
}

func TestKotlinOneLineBodyProperties(t *testing.T) {
	syms, _ := extract(t, astkit.LangKotlin, `class A { val s = 1 }
object O { val s = 1 }
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{"A.s": astkit.KindField, "O.s": astkit.KindField})
}

func TestKotlinTopLevelPropertyKinds(t *testing.T) {
	syms, _ := extract(t, astkit.LangKotlin, `const val TOP = 1
val topVal = 2
var topVar: Int = 3
object R { const val MAX = 4 }
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"TOP": astkit.KindConst, "topVal": astkit.KindVariable, "topVar": astkit.KindVariable,
		"R.MAX": astkit.KindField,
	})
}

func TestSwiftInitAndDeinitNames(t *testing.T) {
	syms, _ := extract(t, astkit.LangSwift, `class Cache {
    init() {}
    convenience init(capacity: Int) { self.init() }
    deinit { flush() }
}
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{"Cache.init": astkit.KindConstructor, "Cache.deinit": astkit.KindMethod})
	wantAbsent(t, syms, "Cache.Cache")
	for _, s := range syms {
		// The Name stays the type name: `Cache(capacity: 1)` is how callers
		// construct, and Grove resolves calls by Name.
		if s.Kind == astkit.KindConstructor && s.Name != "Cache" {
			t.Errorf("initializer Name = %q, want Cache", s.Name)
		}
	}
}

func TestSwiftAssociatedTypes(t *testing.T) {
	syms, _ := extract(t, astkit.LangSwift, `protocol Repository {
    associatedtype Item: Equatable
    associatedtype Key = String
    func find() -> Item
}
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"Repository.Item": astkit.KindType, "Repository.Key": astkit.KindType, "Repository.find": astkit.KindMethod,
	})
}

func TestSwiftTopLevelBindingKinds(t *testing.T) {
	syms, _ := extract(t, astkit.LangSwift, `let globalLimit = 10
var globalCounter = 0
struct P { let x: Int }
`)
	wantKinds(t, syms, map[string]astkit.SymbolKind{
		"globalLimit": astkit.KindConst, "globalCounter": astkit.KindVariable, "P.x": astkit.KindField,
	})
}
