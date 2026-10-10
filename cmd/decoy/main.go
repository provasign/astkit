// Command decoy injects code-shaped comments (and Python docstrings) into
// every source file under a directory, in place, for metamorphic testing of
// an indexer: a comment cannot change what a program means, so an index of
// the decoyed tree must equal an index of the original (compared by name,
// since inserted lines shift spans).
//
// Usage: decoy <dir>
//
// Decoys go only where they cannot change the program: after a line whose
// last character is a code `{` (the newline is then in code, not inside a
// literal or comment), after a one-line Python def/class header, and at the
// end of the file. Insertion points are found on textmask output, so a `{`
// inside a string, comment or JSX text never qualifies.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/provasign/astkit"
	"github.com/provasign/astkit/textmask"
)

// One line each; no `*/`. The apostrophes and the lone quote are on purpose:
// a lexer that opens a literal on them blanks the code that follows.
const cDecoy = `// decoy: class Fake extends Base implements Iface, Other { private Bar client; ` +
	`void ghost() { helper(1); new Fake(); client.go(); } } use Loggable; impl Shape for Wrapper {} ` +
	`beta::normalize(x); Widget::draw(x); export default ghost; don't 'quote "unterminated`

var cBlock = []string{
	`/* decoy block: don't 'quote`,
	`#include "dead.h"`,
	`class Fake2 extends Base implements Iface {`,
	`    Fake helper = new Fake(); helper.run(); self.client = Bar()`,
	`func Phantom() {} type Ghost struct{} fn ghost() {} def helper(x):`,
	`*/`,
}

const pyDecoy = `# decoy: class Fake(Base): def helper(x): self.client = Bar(); raise NotImplementedError; ` +
	`import dead; don't 'quote "unterminated`

var pyDoc = []string{
	`"""Decoy docstring. Don't 'quote.`,
	``,
	`Attributes`,
	`----------`,
	`client : Fake`,
	``,
	`>>> def helper(x):`,
	`...     raise NotImplementedError`,
	`>>> class Phantom(Base): pass`,
	`"""`,
}

var skipDirs = map[string]bool{".git": true, ".grove": true, "node_modules": true, "vendor": true,
	"third_party": true, "target": true, "build": true, "dist": true}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: decoy <dir>")
		os.Exit(2)
	}
	changed := 0
	err := filepath.WalkDir(os.Args[1], func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lang := string(astkit.DetectLanguage(path, string(src)))
		if !textmask.Supported(lang) || lang == "cobol" || lang == "jcl" {
			return nil
		}
		out := inject(lang, string(src))
		if out == string(src) {
			return nil
		}
		changed++
		return os.WriteFile(path, []byte(out), 0o644)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(changed)
}

func inject(lang, src string) string {
	if strings.Contains(src, "\r\n") {
		return src // keep line-ending handling out of it
	}
	lines := strings.Split(src, "\n")
	masked := strings.Split(textmask.MaskFile(lang, src), "\n")
	var out []string
	for i, line := range lines {
		out = append(out, line)
		if lang == "python" {
			out = append(out, pythonDecoys(lines, masked, i)...)
			continue
		}
		code := strings.TrimRight(masked[i], " \t")
		if strings.HasSuffix(code, "{") && strings.HasSuffix(strings.TrimRight(line, " \t"), "{") {
			out = append(out, indentAfter(lines, i)+cDecoy)
		}
	}
	tail := cBlock
	if lang == "python" {
		tail = []string{pyDecoy}
	}
	if lang == "php" && !strings.Contains(textmask.MaskFile(lang, src+"\n$x;"), "$x;") {
		return strings.Join(out, "\n") // file ends in inline HTML: a comment there would be page text
	}
	if len(out) > 0 && out[len(out)-1] == "" {
		out = append(out[:len(out)-1], append(append([]string{}, tail...), "")...)
	} else {
		out = append(out, tail...)
	}
	return strings.Join(out, "\n")
}

// pythonDecoys returns the lines to insert after line i: a comment, plus a
// docstring when line i is a one-line def/class header.
func pythonDecoys(lines, masked []string, i int) []string {
	code := strings.TrimSpace(masked[i])
	if !strings.HasSuffix(code, ":") || !strings.HasSuffix(strings.TrimRight(lines[i], " \t"), ":") {
		return nil
	}
	if !(strings.HasPrefix(code, "def ") || strings.HasPrefix(code, "async def ") || strings.HasPrefix(code, "class ")) {
		return nil
	}
	if strings.Count(code, "(") != strings.Count(code, ")") || strings.Count(code, "[") != strings.Count(code, "]") {
		return nil
	}
	j := i + 1
	for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
		j++
	}
	if j >= len(lines) {
		return nil
	}
	body := leading(lines[j])
	if len(body) <= len(leading(lines[i])) {
		return nil
	}
	out := []string{body + pyDecoy}
	for _, l := range pyDoc {
		if l == "" {
			out = append(out, "")
		} else {
			out = append(out, body+l)
		}
	}
	return out
}

func indentAfter(lines []string, i int) string {
	for j := i + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) != "" {
			if ind := leading(lines[j]); ind != "" {
				return ind
			}
			break
		}
	}
	return leading(lines[i]) + "    "
}

func leading(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}
