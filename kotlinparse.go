package astkit

import (
	"context"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/provasign/astkit/textmask"
)

// parseKotlinWithoutComments parses Kotlin source as if it had no comments.
//
// The vendored Kotlin grammar fails on a good deal of valid modern Kotlin
// (`object : X by y`, `f(x).p = v`, a primary constructor on the line after
// the class name), and how it recovers depends on the comments around the
// failure: comment tokens are extras the recovery can attach anywhere, and
// tree-sitter prices each candidate recovery by the bytes and lines it
// skips, comment lines included. Adding a comment line inside a class could
// turn the class into an ERROR node and drop its members, or surface a class
// from a commented-out block. A comment cannot change what a program means,
// so the parser never sees one:
//
//   - every comment is blanked (textmask, offsets kept);
//   - every line that held only comment text is cut out, so the parser's
//     input, and every recovery cost, is the same with or without them;
//   - the cut lines are put back with tree edits, which move node positions
//     without re-parsing, so the tree addresses src's bytes, rows and
//     columns as a normal parse does.
//
// The tree has no comment nodes: callers reading text between nodes must
// mask it themselves (textmask.MaskComments).
func parseKotlinWithoutComments(ctx context.Context, p *sitter.Parser, src []byte) (*sitter.Tree, error) {
	masked := textmask.MaskComments(string(LangKotlin), string(src))
	cuts := kotlinCommentOnlyLines(src, masked)
	if len(cuts) == 0 {
		return p.ParseCtx(ctx, nil, []byte(masked))
	}
	compact := make([]byte, 0, len(masked))
	prev := 0
	for _, c := range cuts {
		compact = append(compact, masked[prev:c[0]]...)
		prev = c[1]
	}
	compact = append(compact, masked[prev:]...)
	tree, err := p.ParseCtx(ctx, nil, compact)
	if err != nil || tree == nil {
		return tree, err
	}
	// Put the cut lines back front to back: everything before a cut is
	// already back in place, so its src offset and point are also its
	// position in the edited tree.
	var at points
	for _, c := range cuts {
		start := at.advance(src, c[0])
		if !treeEdit(tree, sitter.EditInput{
			StartIndex: uint32(c[0]), OldEndIndex: uint32(c[0]), NewEndIndex: uint32(c[1]),
			StartPoint: start, OldEndPoint: start, NewEndPoint: at.advance(src, c[1]),
		}) {
			// Unreachable while go-tree-sitter keeps its layout (a test
			// checks it); positions would be wrong, so parse in place.
			tree.Close()
			return p.ParseCtx(ctx, nil, []byte(masked))
		}
	}
	return tree, nil
}

// kotlinCommentOnlyLines returns the byte ranges of src's lines (each with
// its newline) that hold comment text and nothing else, merged into
// maximal runs. masked is src with comments blanked.
func kotlinCommentOnlyLines(src []byte, masked string) [][2]int {
	var cuts [][2]int
	for ls := 0; ls < len(src); {
		le := ls
		for le < len(src) && src[le] != '\n' {
			le++
		}
		if le < len(src) {
			le++ // include the newline
		}
		if isBlank(masked[ls:le]) && !isBlank(string(src[ls:le])) {
			if n := len(cuts); n > 0 && cuts[n-1][1] == ls {
				cuts[n-1][1] = le
			} else {
				cuts = append(cuts, [2]int{ls, le})
			}
		}
		ls = le
	}
	return cuts
}

func isBlank(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\r', '\n', '\f', '\v':
		default:
			return false
		}
	}
	return true
}

// points converts increasing byte offsets of one source to tree-sitter
// row/column points (columns in bytes) in a single forward pass.
type points struct {
	off int
	pt  sitter.Point
}

func (p *points) advance(src []byte, off int) sitter.Point {
	for ; p.off < off; p.off++ {
		if src[p.off] == '\n' {
			p.pt.Row++
			p.pt.Column = 0
		} else {
			p.pt.Column++
		}
	}
	return p.pt
}
