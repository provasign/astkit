package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

// gprDeclIndex maps "Parent.Name" (or "Name" when unparented) to the symbol.
func gprDeclIndex(syms []astkit.Symbol) map[string]astkit.Symbol {
	out := map[string]astkit.Symbol{}
	for _, s := range syms {
		key := s.QualifiedName
		if s.ParentName != "" && s.QualifiedName == s.Name {
			key = s.ParentName + "." + s.Name
		}
		if _, dup := out[key]; !dup {
			out[key] = s
		}
	}
	return out
}

func gprWantDecls(t *testing.T, syms []astkit.Symbol, want map[string]astkit.SymbolKind) {
	t.Helper()
	got := gprDeclIndex(syms)
	for key, kind := range want {
		s, ok := got[key]
		if !ok {
			t.Errorf("%s not indexed; got %v", key, keysOf(got))
			continue
		}
		if s.Kind != kind {
			t.Errorf("%s kind = %s, want %s", key, s.Kind, kind)
		}
	}
}

func TestGoGroupedVarBlockIsIndexed(t *testing.T) {
	src := `package p

var (
	DefaultTimeout = 30
	ErrClosed      = errors.New("closed")
	registry       map[string]int
)
`
	syms, _ := extract(t, astkit.LangGo, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{
		"DefaultTimeout": astkit.KindVariable, "ErrClosed": astkit.KindVariable, "registry": astkit.KindVariable,
	})
}

func TestGoMultiNameVarAndConstIndexEveryName(t *testing.T) {
	src := `package p

var C, D = 1, 2

const E, F = 3, 4

const (
	G, H = 5, 6
)

var (
	I, J int
)
`
	syms, _ := extract(t, astkit.LangGo, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{
		"C": astkit.KindVariable, "D": astkit.KindVariable,
		"E": astkit.KindConst, "F": astkit.KindConst, "G": astkit.KindConst, "H": astkit.KindConst,
		"I": astkit.KindVariable, "J": astkit.KindVariable,
	})
}

func TestGoInterfaceMethodsAreIndexed(t *testing.T) {
	src := `package p

type Reader interface {
	Read(p []byte) (n int, err error)
	io.Closer
	close() error
}

type Number interface {
	~int | ~float64
}
`
	syms, _ := extract(t, astkit.LangGo, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{
		"Reader": astkit.KindInterface, "Reader.Read": astkit.KindMethod, "Reader.close": astkit.KindMethod,
	})
	got := gprDeclIndex(syms)
	if r := got["Reader.Read"]; r.Signature != "Read(p []byte) (n int, err error)" || !r.Exported {
		t.Errorf("Reader.Read = %+v", r)
	}
	if got["Reader.close"].Exported {
		t.Errorf("Reader.close should not be exported")
	}
	for _, s := range syms {
		if s.ParentName == "Number" || s.Name == "Closer" {
			t.Errorf("type-set/embedded element indexed as a member: %+v", s)
		}
	}
}

func TestGoTypeAliasIsIndexed(t *testing.T) {
	src := `package p

type ID = string

type (
	Key   = ID
	Other int
)
`
	syms, _ := extract(t, astkit.LangGo, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{
		"ID": astkit.KindType, "Key": astkit.KindType, "Other": astkit.KindType,
	})
}

func TestGoGenericInterfaceMethodCarriesTypeParameters(t *testing.T) {
	src := `package p

type Store[T any] interface {
	Get(key string) T
}
`
	syms, _ := extract(t, astkit.LangGo, src)
	got := gprDeclIndex(syms)
	get, ok := got["Store.Get"]
	if !ok || len(get.TypeParameters) != 1 || get.TypeParameters[0] != "T" {
		t.Fatalf("Store.Get = %+v", get)
	}
}
