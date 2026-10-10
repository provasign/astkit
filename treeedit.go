package astkit

/*
#include <stdint.h>

// Layouts of tree-sitter's api.h TSPoint and TSInputEdit; ts_tree_edit is
// linked from go-tree-sitter's runtime.
typedef struct { uint32_t row; uint32_t column; } astkitPoint;
typedef struct {
	uint32_t start_byte, old_end_byte, new_end_byte;
	astkitPoint start_point, old_end_point, new_end_point;
} astkitInputEdit;

void ts_tree_edit(void *self, const astkitInputEdit *edit);

static void astkit_tree_edit(void *tree, astkitInputEdit edit) { ts_tree_edit(tree, &edit); }
*/
import "C"

import (
	"reflect"
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

// treeEdit applies e to tree like sitter.Tree.Edit, which passes
// OldEndPoint as the new end point (go-tree-sitter bug), so an edit that
// adds lines never moves a row. It reaches the native tree through
// go-tree-sitter's unexported BaseTree.c and reports false when that
// layout is not there.
func treeEdit(tree *sitter.Tree, e sitter.EditInput) bool {
	ptr := nativeTree(tree)
	if ptr == nil {
		return false
	}
	pt := func(p sitter.Point) C.astkitPoint {
		return C.astkitPoint{row: C.uint32_t(p.Row), column: C.uint32_t(p.Column)}
	}
	C.astkit_tree_edit(ptr, C.astkitInputEdit{
		start_byte:    C.uint32_t(e.StartIndex),
		old_end_byte:  C.uint32_t(e.OldEndIndex),
		new_end_byte:  C.uint32_t(e.NewEndIndex),
		start_point:   pt(e.StartPoint),
		old_end_point: pt(e.OldEndPoint),
		new_end_point: pt(e.NewEndPoint),
	})
	return true
}

func nativeTree(tree *sitter.Tree) unsafe.Pointer {
	if tree == nil || tree.BaseTree == nil {
		return nil
	}
	f := reflect.ValueOf(tree.BaseTree).Elem().FieldByName("c")
	if !f.IsValid() || f.Kind() != reflect.Pointer || f.IsNil() {
		return nil
	}
	return f.UnsafePointer()
}
