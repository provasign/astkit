package strategies

import (
	"strings"
	"testing"
)

func TestCSEvalCondition(t *testing.T) {
	for expr, want := range map[string]bool{
		"NET20": false, "!HAVE_LINQ": true, "!(NET20 || NET35)": true,
		"!(PORTABLE || DNXCORE50) || NETSTANDARD2_0": true, "true && !X": true,
		"A == B": true, "A != B": false, "false": false,
	} {
		got, ok := csEvalCondition(expr)
		if !ok || got != want {
			t.Errorf("%q = %v,%v want %v", expr, got, ok, want)
		}
	}
	if _, ok := csEvalCondition("A &&"); ok {
		t.Errorf("malformed expression must not evaluate")
	}
}

// Only the branch the compiler takes with no symbols defined imports: the
// #else's System.Linq, not the NET20 LinqBridge polyfill. A lone #if keeps
// its using.
func TestCSharpImportsSkipLosingAlternateBranch(t *testing.T) {
	src := `#if NET20
using Newtonsoft.Json.Utilities.LinqBridge;
#else
using System.Linq;
#endif
#if !HAVE_LINQ
using Polyfill.A;
#else
using Polyfill.B;
#endif
#if !(NET20 || NET35)
using System.Numerics;
#endif
class C {}`
	inactive := csAlternateInactiveLines([]byte(src))
	var live []string
	for i, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "using ") && !inactive[i+1] {
			live = append(live, strings.TrimSuffix(strings.TrimPrefix(l, "using "), ";"))
		}
	}
	if got := strings.Join(live, ","); got != "System.Linq,Polyfill.A,System.Numerics" {
		t.Errorf("live usings = %s", got)
	}
}
