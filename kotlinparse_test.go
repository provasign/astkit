package astkit_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/kotlin"

	"github.com/provasign/astkit"
	"github.com/provasign/astkit/strategies"
	"github.com/provasign/astkit/textmask"
)

// namedNodes lists every named non-comment node with its position. A
// node's end is taken back over trailing blanks in masked (the source with
// comments blanked): a node the grammar ends with its newlines, such as
// import_list, may run on over blank or comment-only lines, which says
// nothing about where its code is.
func namedNodes(n *sitter.Node, masked string, out *[]string) {
	switch n.Type() {
	case "line_comment", "multiline_comment":
		return
	}
	if n.IsNamed() && n.Type() != "source_file" {
		end := int(n.EndByte())
		for end > int(n.StartByte()) && strings.ContainsRune(" \t\r\n", rune(masked[end-1])) {
			end--
		}
		row := strings.Count(masked[:end], "\n")
		col := end - (strings.LastIndexByte(masked[:end], '\n') + 1)
		*out = append(*out, fmt.Sprintf("%s %d-%d %v-%d:%d", n.Type(), n.StartByte(), end, n.StartPoint(), row, col))
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		namedNodes(n.Child(i), masked, out)
	}
}

// A Kotlin tree parsed without its comments addresses the source exactly as
// a parse of the comment-blanked source does: same nodes, same bytes, rows
// and columns. (A parse of the raw source is not the reference: comments
// change how the grammar groups `@Suppress("x")` and an import's extent.)
// It also checks treeEdit still reaches go-tree-sitter's native tree.
func TestKotlinParseWithoutCommentsKeepsPositions(t *testing.T) {
	src := []byte(`/*
 * License header.
 */
package demo

// Imports follow.
import kotlin.math.max // trailing

/** A greeter. */
@Suppress("x")
// between the annotation and the class
class Greeter(
  // the name
  private val name: String, /* inline */ val n: Int,
) : Base(), Iface {
  // first member
  val greeting = "hi /* not a comment */ $name" // trailing

  /*
   * A block.
   */
  fun greet(times: Int /* count */): String {
    // body comment
    return greeting.repeat(max(times, 1))
  }
}
// end of file`)
	p := sitter.NewParser()
	p.SetLanguage(kotlin.GetLanguage())
	masked := textmask.MaskComments("kotlin", string(src))
	plain, err := p.ParseCtx(context.Background(), nil, []byte(masked))
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if plain.RootNode().HasError() {
		t.Fatalf("fixture must parse cleanly: %s", plain.RootNode().String())
	}
	tree, err := astkit.NewEngine().Parse(context.Background(), astkit.LangKotlin, src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	var want, got []string
	namedNodes(plain.RootNode(), masked, &want)
	namedNodes(tree.RootNode(), masked, &got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("nodes differ:\n got %v\nwant %v", got, want)
	}
}

// Comment lines cannot change what is extracted from Kotlin the grammar
// cannot parse. Before comment lines were cut from the parser's input, a
// comment after a `{` moved the error recovery so a class became an ERROR
// node and lost its members, a class body became a lambda, or method
// locals became fields.
func TestKotlinCommentLinesDoNotMoveErrorRecovery(t *testing.T) {
	cases := map[string]string{
		// Cut down from okio's ByteString.kt: a primary constructor on the
		// line after `expect open class ByteString` (not in the grammar).
		"constructor-line": `internal constructor(data: ByteArray) : Comparable<ByteString> {
  fun hex(): String
`,
		// Cut down from okio's CommonRealBufferedSourceTest.kt, whose
		// `object : Source by buffer` the grammar cannot parse.
		"unclosed-body": `class CommonRealBufferedSourceTest {
    val bufferedSource = (
    assertEquals(6, buffer.size)
    assertEquals(-1, bufferedSource.indexOf('e'.code.toByte(), 0, 4))
    assertEquals(2, buffer.size)
  }
`,
		// Cut down from okio's ZipBuilder.kt.
		"unclosed-method": `class ZipBuilder(
) {
  fun build(): Path {
    val result = process.waitFor()
      .apply { environment()["TZ"] = "UTC" }
    require(exitCode == 0)
  }
`,
		// From moshi's LinkedHashTreeMap.kt: assigning through a call
		// result is not in the grammar.
		"call-receiver-assign": `package moshi

internal class Tree<K, V>(comparator: Comparator<Any?>? = null) : AbstractMutableMap<K, V>() {
  private var size = 0

  fun removeInternal(node: Node<K, V>, unlink: Boolean) {
    if (unlink) {
      knownNotNull(node.prev).next = node.next
      node.prev = null
    }
    var left = node.left
    if (left != null) {
      size--
    }
  }

  fun other(): Int {
    return size
  }
}
`,
	}
	for name, clean := range cases {
		t.Run(name, func(t *testing.T) {
			decoy := injectCommentLines(clean)
			if got, want := kotlinFacts(t, decoy), kotlinFacts(t, clean); got != want {
				t.Fatalf("comment lines changed the symbols:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// injectCommentLines adds a code-shaped comment line after every line
// ending in `{`, indented like the next line, and a block comment at the
// end, the way cmd/decoy does.
func injectCommentLines(src string) string {
	lines := strings.Split(src, "\n")
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(line + "\n")
		if !strings.HasSuffix(line, "{") {
			continue
		}
		indent := "    "
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) != "" {
				indent = next[:len(next)-len(strings.TrimLeft(next, " "))]
				break
			}
		}
		b.WriteString(indent + "// decoy: class Fake extends Base implements Iface, Other { private Bar client; void ghost() { helper(1); new Fake(); client.go(); } } use Loggable; impl Shape for Wrapper {} beta::normalize(x); Widget::draw(x); export default ghost; don't 'quote \"unterminated\n")
	}
	b.WriteString("/* decoy block: don't 'quote\n#include \"dead.h\"\nclass Fake2 extends Base implements Iface {\n    Fake helper = new Fake(); helper.run(); self.client = Bar()\nfunc Phantom() {} type Ghost struct{} fn ghost() {} def helper(x):\n*/\n")
	return b.String()
}

// kotlinFacts lists the extracted symbols without positions.
func kotlinFacts(t *testing.T, src string) string {
	t.Helper()
	tree, err := astkit.NewEngine().Parse(context.Background(), astkit.LangKotlin, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	syms, err := strategies.Default().Extract(astkit.LangKotlin, tree, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range syms {
		out = append(out, fmt.Sprintf("%s %s %q", s.Kind, s.QualifiedName, s.Signature))
	}
	return strings.Join(out, "\n")
}
