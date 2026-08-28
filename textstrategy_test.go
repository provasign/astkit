package astkit_test

import (
	"testing"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/provasign/astkit"
	"github.com/provasign/astkit/strategies"
)

// fakeTextStrategy extracts from src alone; Extract tolerates a nil tree.
type fakeTextStrategy struct{}

func (fakeTextStrategy) Language() astkit.LanguageKey { return astkit.LanguageKey("fake-text") }
func (fakeTextStrategy) Extensions() []string         { return []string{".ftx"} }
func (fakeTextStrategy) ExtractsFromText() bool       { return true }
func (fakeTextStrategy) Extract(tree *sitter.Tree, src []byte) ([]astkit.Symbol, error) {
	if tree != nil {
		panic("fakeTextStrategy must be dispatched without a tree")
	}
	return []astkit.Symbol{{Name: string(src)}}, nil
}
func (fakeTextStrategy) ExtractImports(tree *sitter.Tree, src []byte) ([]astkit.ImportStatement, error) {
	return nil, nil
}

func TestTextCapable_FakeTextStrategy(t *testing.T) {
	r := astkit.NewRegistry()
	r.Register(fakeTextStrategy{})
	if !r.TextCapable("fake-text") {
		t.Fatal("registered TextStrategy not reported TextCapable")
	}
	syms, err := r.Extract("fake-text", nil, []byte("hello"))
	if err != nil {
		t.Fatalf("Extract with nil tree: %v", err)
	}
	if len(syms) != 1 || syms[0].Name != "hello" {
		t.Fatalf("text extraction did not run: %+v", syms)
	}
}

func TestTextCapable_UnregisteredLanguage(t *testing.T) {
	if astkit.NewRegistry().TextCapable("nope") {
		t.Fatal("empty registry reported TextCapable")
	}
}

// The no-op guarantee: no language in the default registry is text-capable,
// so the tree==nil dispatch path is unreachable for all current languages.
func TestTextCapable_NoDefaultLanguageIsTextCapable(t *testing.T) {
	r := strategies.Default()
	for _, lang := range []astkit.LanguageKey{
		astkit.LangGo, astkit.LangPython, astkit.LangJava, astkit.LangRust,
		astkit.LangJavaScript, astkit.LangTypeScript, astkit.LangTSX,
		astkit.LangC, astkit.LangCPP, astkit.LangCSharp, astkit.LangPHP,
	} {
		if r.TextCapable(lang) {
			t.Fatalf("%s unexpectedly text-capable", lang)
		}
	}
}
