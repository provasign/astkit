package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

func TestObjCKeywordSelectorJoinsAllParts(t *testing.T) {
	src := `@interface Person : NSObject
- (void)doThing:(int)x withOption:(BOOL)y;
- (NSString *)greet;
@end
`
	syms, _ := extract(t, astkit.LangObjC, src)
	names := map[string]astkit.Symbol{}
	for _, s := range syms {
		names[s.Name] = s
	}
	if _, ok := names["doThing:withOption:"]; !ok {
		t.Fatalf("keyword selector not joined; symbols: %v", names)
	}
	if _, ok := names["greet"]; !ok {
		t.Fatalf("unary selector missing; symbols: %v", names)
	}
}

func TestObjCClassMethodModifier(t *testing.T) {
	src := `@interface Person : NSObject
+ (instancetype)personWithName:(NSString *)name;
- (void)instanceMethod;
@end
`
	syms, _ := extract(t, astkit.LangObjC, src)
	for _, s := range syms {
		switch s.Name {
		case "personWithName:":
			found := false
			for _, m := range s.Modifiers {
				if m == "class" {
					found = true
				}
			}
			if !found {
				t.Errorf("personWithName: modifiers = %v, want class", s.Modifiers)
			}
		case "instanceMethod":
			for _, m := range s.Modifiers {
				if m == "class" {
					t.Errorf("instanceMethod must not be marked class")
				}
			}
		}
	}
}

func TestObjCSuperclassAndProtocolsInSignature(t *testing.T) {
	// Grove's own edges.go parses this Signature text to build extends
	// (superclass) and implements (protocol) edges.
	src := `@interface Person : NSObject <Greeter, NSCopying>
@end
`
	syms, _ := extract(t, astkit.LangObjC, src)
	for _, s := range syms {
		if s.Name != "Person" {
			continue
		}
		want := "@interface Person : NSObject <Greeter, NSCopying>"
		if s.Signature != want {
			t.Fatalf("Person signature = %q, want %q", s.Signature, want)
		}
		return
	}
	t.Fatal("missing Person")
}

func TestObjCCategoryDoesNotDeclareANewClass(t *testing.T) {
	src := `@interface Person (Additions)
- (void)extra;
@end
`
	syms, _ := extract(t, astkit.LangObjC, src)
	for _, s := range syms {
		if s.Kind == astkit.KindClass {
			t.Fatalf("a category must not emit a class symbol: %+v", s)
		}
	}
	found := false
	for _, s := range syms {
		if s.Name == "extra" && s.ParentName == "Person" {
			found = true
		}
	}
	if !found {
		t.Fatalf("extra must be parented to Person; symbols: %+v", syms)
	}
}

func TestObjCImplementationEmitsNoDuplicateClassSymbol(t *testing.T) {
	src := `@implementation Person
- (void)greet {
    [self helper];
}
- (void)helper {}
@end
`
	syms, _ := extract(t, astkit.LangObjC, src)
	for _, s := range syms {
		if s.Kind == astkit.KindClass {
			t.Fatalf("an @implementation must not emit its own class symbol: %+v", s)
		}
	}
}

func TestObjCSelfMessageSendKeepsExplicitQualifier(t *testing.T) {
	src := `@implementation Person
- (void)greet {
    [self helper];
}
- (void)helper {}
@end
`
	syms, _ := extract(t, astkit.LangObjC, src)
	for _, s := range syms {
		if s.Name != "greet" {
			continue
		}
		if len(s.CallSites) != 1 || s.CallSites[0].Callee != "self.helper" {
			t.Fatalf("greet call sites = %+v, want self.helper", s.CallSites)
		}
		return
	}
	t.Fatal("missing greet")
}

func TestObjCChainedAllocInitCallSites(t *testing.T) {
	src := `@implementation Person
+ (instancetype)personWithName:(NSString *)name {
    return [[Person alloc] initWithName:name];
}
@end
`
	syms, _ := extract(t, astkit.LangObjC, src)
	for _, s := range syms {
		if s.Name != "personWithName:" {
			continue
		}
		callees := map[string]bool{}
		for _, cs := range s.CallSites {
			callees[cs.Callee] = true
		}
		// The inner `[Person alloc]` yields a Person, so the outer send's
		// receiver is written `Person()` — Grove's call-result form.
		if !callees["Person.alloc"] || !callees["Person().initWithName:"] {
			t.Fatalf("call sites = %+v, want Person.alloc and Person().initWithName:", s.CallSites)
		}
		return
	}
	t.Fatal("missing personWithName:")
}

func TestObjCPlainCFunctionStillExtracted(t *testing.T) {
	// Objective-C is a strict C superset; a .m file's plain C-level
	// functions must still be extracted (reused from astkit's C strategy).
	src := `int add(int a, int b) {
    return a + b;
}

@interface Person : NSObject
@end
`
	syms, _ := extract(t, astkit.LangObjC, src)
	found := false
	for _, s := range syms {
		if s.Name == "add" && s.Kind == astkit.KindFunction {
			found = true
		}
	}
	if !found {
		t.Fatalf("plain C function 'add' not extracted; symbols: %+v", syms)
	}
}
