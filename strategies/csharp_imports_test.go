package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

// using directives under a file-level #if (and inside namespace blocks)
// are imports: newtonsoft wraps whole test files in `#if !(NET20 || ...)`.
func TestCSharpImportsUnderPreprocessorRegion(t *testing.T) {
	src := "#if !(NET20 || NET35)\nusing System;\nusing Newtonsoft.Json.Bson;\nnamespace T {\n using System.IO;\n class A { }\n}\n#endif\n"
	_, imps := extract(t, astkit.LangCSharp, src)
	got := map[string]bool{}
	for _, i := range imps {
		got[i.Path] = true
	}
	for _, want := range []string{"System", "Newtonsoft.Json.Bson", "System.IO"} {
		if !got[want] {
			t.Fatalf("missing import %q in %v", want, imps)
		}
	}
}
