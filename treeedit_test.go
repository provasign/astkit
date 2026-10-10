package astkit

import (
	"context"
	"testing"
)

// treeEdit reaches the native tree through go-tree-sitter's unexported
// layout; without it Kotlin falls back to a parse whose recovery comments
// can move.
func TestTreeEditReachesNativeTree(t *testing.T) {
	tree, err := NewEngine().Parse(context.Background(), LangGo, []byte("package p\n"))
	if err != nil || tree == nil {
		t.Fatalf("parse: %v", err)
	}
	defer tree.Close()
	if nativeTree(tree) == nil {
		t.Fatal("go-tree-sitter's Tree layout changed: nativeTree found no native tree")
	}
}
