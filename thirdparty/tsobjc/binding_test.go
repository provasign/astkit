package tsobjc

import (
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
)

func TestGetLanguage(t *testing.T) {
	p := sitter.NewParser()
	p.SetLanguage(GetLanguage())
	tree := p.Parse(nil, []byte("@interface Foo : NSObject\n@end\n"))
	if tree.RootNode().HasError() {
		t.Fatalf("unexpected parse error:\n%s", tree.RootNode().String())
	}
}
