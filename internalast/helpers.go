// Package internalast contains helpers shared by every per-language strategy
// implementation. Kept in a sub-package so that experimentation in one
// strategy can not accidentally widen the helper API.
package internalast

import (
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/provasign/astkit"
)

// WalkChildren invokes fn on every immediate child of n.
func WalkChildren(n *sitter.Node, fn func(*sitter.Node)) {
	if n == nil {
		return
	}
	count := int(n.ChildCount())
	for i := 0; i < count; i++ {
		c := n.Child(i)
		if c != nil {
			fn(c)
		}
	}
}

// WalkTree invokes fn on n and every descendant, in document order. Use it
// for constructs that are legal at any nesting depth (Python imports under
// `if TYPE_CHECKING:` or inside functions, PHP require inside a body) where
// an immediate-children walk silently drops real statements.
func WalkTree(n *sitter.Node, fn func(*sitter.Node)) {
	if n == nil {
		return
	}
	fn(n)
	count := int(n.ChildCount())
	for i := 0; i < count; i++ {
		WalkTree(n.Child(i), fn)
	}
}

// FindChildByType returns the first immediate child of n whose Type() equals kind.
func FindChildByType(n *sitter.Node, kind string) *sitter.Node {
	if n == nil {
		return nil
	}
	count := int(n.ChildCount())
	for i := 0; i < count; i++ {
		c := n.Child(i)
		if c != nil && c.Type() == kind {
			return c
		}
	}
	return nil
}

// NodeText returns the source text covered by n, clamped to src bounds.
func NodeText(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	start, end := n.StartByte(), n.EndByte()
	if int(end) > len(src) {
		end = uint32(len(src))
	}
	if start > end {
		return ""
	}
	return string(src[start:end])
}

// NodeSpan returns the 1-indexed inclusive line range covered by n.
func NodeSpan(n *sitter.Node) astkit.LineRange {
	if n == nil {
		return astkit.LineRange{}
	}
	start := int(n.StartPoint().Row) + 1
	end := int(n.EndPoint().Row) + 1
	// Tree-sitter's EndPoint is the position AFTER the node's last byte. When
	// a grammar makes a node consume its trailing newline, EndPoint sits at
	// column 0 of the NEXT line, and +1 marks the node as ending one line too
	// far. Downstream this is not cosmetic: prism's lossless-delta rendering
	// substitutes span line ranges for unchanged bodies, so an over-long span
	// can swallow a changed line into a "[prism:cached]" pointer. Its
	// overlap guard only fails closed when the next symbol starts on that
	// exact line — a blank line between symbols defeats it.
	if end > start && n.EndPoint().Column == 0 {
		end--
	}
	return astkit.LineRange{Start: start, End: end}
}

// IsCapitalized reports whether name begins with an uppercase ASCII letter.
// Useful for Go's export rule.
func IsCapitalized(name string) bool {
	if name == "" {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

// HasModifier reports whether modifiers contains m.
func HasModifier(modifiers []string, m string) bool {
	for _, x := range modifiers {
		if x == m {
			return true
		}
	}
	return false
}

// FirstLine returns the first line of s, useful as a signature fallback.
func FirstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// SignatureBeforeBody returns the declaration header of n — everything from
// the node start up to (not including) its "body" child — with annotation
// nodes, attribute sections and comments excised and whitespace collapsed to
// single spaces. Unlike FirstLine, this survives declarations that wrap
// `extends`/`implements` clauses or parameter lists onto continuation lines,
// and never returns a leading `@Override` line as the signature. Falls back
// to the whole node text when there is no body (e.g. abstract or interface
// methods).
//
// Comments are cut from the tree (they are tree-sitter extras), so a
// trailing line comment is never glued into the collapsed header: jackson's
//
//	public final class ManagedReferenceProperty  // Changed in 2.9
//	    extends SettableBeanProperty.Delegating
//
// stays "… ManagedReferenceProperty extends …", and a Python
// `class A(Base):  # was Fake` keeps no Fake.
func SignatureBeforeBody(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	start := n.StartByte()
	end := n.EndByte()
	if body := n.ChildByFieldName("body"); body != nil {
		end = body.StartByte()
	}
	if int(end) > len(src) {
		end = uint32(len(src))
	}
	if start >= end {
		return ""
	}
	// Annotations live inside the "modifiers" child; excise their byte ranges
	// so they never masquerade as (or pollute) the signature. They remain
	// available on the symbol's Annotations field.
	cuts := headerCuts(n, start, end, IsAttributeSection)
	if mods := FindChildByType(n, "modifiers"); mods != nil {
		count := int(mods.ChildCount())
		for i := 0; i < count; i++ {
			c := mods.Child(i)
			if c == nil {
				continue
			}
			if t := c.Type(); t == "marker_annotation" || t == "annotation" {
				cuts = append(cuts, [2]uint32{c.StartByte(), c.EndByte()})
			}
		}
	}
	return strings.Join(strings.Fields(blankRanges(src, start, end, cuts)), " ")
}

// IsComment reports whether n is a comment node in any astkit grammar.
func IsComment(n *sitter.Node) bool {
	switch n.Type() {
	case "comment", "line_comment", "block_comment", "multiline_comment":
		return true
	}
	return false
}

// IsAttributeSection reports whether n is a C# or PHP attribute section
// (`[Obsolete("x")]`, `#[Route("/x")]`). Like Java annotations, these are
// not part of a signature; C# keeps them on the symbol's Annotations.
func IsAttributeSection(n *sitter.Node) bool {
	return n.Type() == "attribute_list"
}

// headerCuts returns the byte ranges, clipped to [start, end), of the
// comment nodes under n and of n's direct children for which drop reports
// true. Only children overlapping the range are descended, so a header
// costs a walk of the header's nodes, not the whole declaration.
func headerCuts(n *sitter.Node, start, end uint32, drop func(*sitter.Node) bool) [][2]uint32 {
	var cuts [][2]uint32
	add := func(c *sitter.Node) {
		cuts = append(cuts, [2]uint32{max(c.StartByte(), start), min(c.EndByte(), end)})
	}
	var walk func(p *sitter.Node, direct bool)
	walk = func(p *sitter.Node, direct bool) {
		count := int(p.ChildCount())
		for i := 0; i < count; i++ {
			c := p.Child(i)
			if c == nil || c.EndByte() <= start {
				continue
			}
			if c.StartByte() >= end {
				break
			}
			switch {
			case IsComment(c), direct && drop != nil && drop(c):
				add(c)
			case c.ChildCount() > 0:
				walk(c, false)
			}
		}
	}
	walk(n, true)
	return cuts
}

// blankRanges returns src[start:end] with the cut ranges replaced by spaces
// (newlines kept), so tokens on either side of a cut stay separated.
func blankRanges(src []byte, start, end uint32, cuts [][2]uint32) string {
	if len(cuts) == 0 {
		return string(src[start:end])
	}
	b := []byte(string(src[start:end]))
	for _, c := range cuts {
		for i := max(c[0], start); i < min(c[1], end); i++ {
			if ch := b[i-start]; ch != '\n' && ch != '\r' {
				b[i-start] = ' '
			}
		}
	}
	return string(b)
}

// HeaderText returns src[start:end] (a declaration header inside n) without
// the comments under n and the direct children of n for which drop (may be
// nil) reports true. Text with nothing to remove is returned unchanged. When
// something was removed, the blanks around each cut on its line collapse to
// one space (none before `,;)]>` or after `([<`), trailing blanks are
// trimmed from each line and lines left empty are dropped, so
// `f(a, // note` reads `f(a,` and `f(app /* T */, b)` reads `f(app, b)`.
func HeaderText(n *sitter.Node, src []byte, start, end uint32, drop func(*sitter.Node) bool) string {
	return HeaderTextCut(n, src, start, end, drop, nil)
}

// HeaderTextCut is HeaderText with extra byte ranges of src removed as
// comments are: for a tree parsed without its comments (astkit parses
// Kotlin that way), the caller finds them in the text.
func HeaderTextCut(n *sitter.Node, src []byte, start, end uint32, drop func(*sitter.Node) bool, extra [][2]uint32) string {
	if int(end) > len(src) {
		end = uint32(len(src))
	}
	if n == nil || start >= end {
		return ""
	}
	cuts := headerCuts(n, start, end, drop)
	for _, c := range extra {
		if c[1] > start && c[0] < end {
			cuts = append(cuts, [2]uint32{max(c[0], start), min(c[1], end)})
		}
	}
	if len(cuts) == 0 {
		return string(src[start:end])
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i][0] < cuts[j][0] })
	var out []byte
	cut := false // a cut sits between out and the next segment
	appendSeg := func(seg []byte) {
		if cut {
			left := len(out)
			for left > 0 && (out[left-1] == ' ' || out[left-1] == '\t') {
				left--
			}
			out = out[:left]
			for len(seg) > 0 && (seg[0] == ' ' || seg[0] == '\t') {
				seg = seg[1:]
			}
			if left > 0 && len(seg) > 0 && out[left-1] != '\n' && !strings.ContainsRune("([<", rune(out[left-1])) &&
				!strings.ContainsRune(",;)]>\r\n", rune(seg[0])) {
				out = append(out, ' ')
			}
			cut = false
		}
		out = append(out, seg...)
	}
	pos := start
	for _, c := range cuts {
		if c[1] <= pos {
			continue
		}
		if c[0] > pos {
			appendSeg(src[pos:c[0]])
		}
		// Keep the newlines a cut spans: a block comment over several
		// lines still ends each of them.
		for i := max(c[0], pos); i < c[1]; i++ {
			if src[i] == '\n' {
				appendSeg([]byte{'\n'})
			}
		}
		cut = true
		pos = c[1]
	}
	if pos < end {
		appendSeg(src[pos:end])
	}
	return tidyLines(string(out))
}

func tidyLines(s string) string {
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if l = strings.TrimRight(l, " \t\r"); strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// FirstLineSig is FirstLine(n.Content(src)) without comments and without
// the direct children of n for which drop (may be nil) reports true: the
// first line that still has code once they are removed. With nothing to
// remove it equals FirstLine(n.Content(src)).
func FirstLineSig(n *sitter.Node, src []byte, drop func(*sitter.Node) bool) string {
	return FirstLineSigCut(n, src, drop, nil)
}

// FirstLineSigCut is FirstLineSig with extra byte ranges of src removed as
// comments are (see HeaderTextCut).
func FirstLineSigCut(n *sitter.Node, src []byte, drop func(*sitter.Node) bool, extra [][2]uint32) string {
	if n == nil {
		return ""
	}
	start, end := n.StartByte(), n.EndByte()
	if int(end) > len(src) {
		end = uint32(len(src))
	}
	for start < end {
		lineEnd := end
		if i := strings.IndexByte(string(src[start:end]), '\n'); i >= 0 {
			lineEnd = start + uint32(i)
		}
		if lineEnd == n.StartByte() {
			return "" // the node starts with a line break, as FirstLine reads it
		}
		if line := HeaderTextCut(n, src, start, lineEnd, drop, extra); line != "" {
			if start != n.StartByte() {
				line = strings.TrimSpace(line) // a later line's indentation
			}
			return line
		}
		start = lineEnd + 1
	}
	return ""
}
