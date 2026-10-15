package astkit

import "testing"

// A recursive call on the declaration's own line takes the call's column,
// not the declaration name's ("walk" at 14).
func TestCallColumnSkipsTheDeclarationName(t *testing.T) {
	src := "class T {\n  public int walk(int n) { return n <= 0 ? 0 : walk(n - 1); }\n}\n"
	syms := []Symbol{{Name: "walk", Span: LineRange{Start: 2, End: 2},
		CallSites: []CallSite{{Callee: "walk", Line: 2}}}}
	fillCallColumns("java", syms, []byte(src))
	if got := syms[0].CallSites[0].Col; got != 48 {
		t.Fatalf("col %d, want 48", got)
	}
}
