package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

// A function declared inside a function body is its own symbol, marked
// local and qualified by its enclosers; its calls are not its encloser's.
func TestJS_NestedFunctionDeclarationIsLocalSymbol(t *testing.T) {
	src := `export function encodePayload(packets, callback) {
  function encodeOne(packet, done) {
    encodePacket(packet, done);
  }
  map(packets, encodeOne, callback);
}
class Socket {
  onClose() {
    function cleanup() { clearTimeout(this.t); }
    cleanup();
  }
}`
	for _, lang := range []astkit.LanguageKey{astkit.LangTypeScript, astkit.LangJavaScript} {
		syms, _ := extract(t, lang, src)
		byQual := map[string]astkit.Symbol{}
		for _, s := range syms {
			byQual[s.QualifiedName] = s
		}
		one, ok := byQual["encodePayload.encodeOne"]
		if !ok || one.ParentName != "" || !contains(one.Modifiers, "local") {
			t.Fatalf("%s: encodeOne = %+v (symbols %v)", lang, one, byQual)
		}
		if len(one.CallSites) != 1 || one.CallSites[0].Callee != "encodePacket" {
			t.Errorf("%s: encodeOne calls %v", lang, one.CallSites)
		}
		for _, cs := range byQual["encodePayload"].CallSites {
			if cs.Callee == "encodePacket" {
				t.Errorf("%s: encodePayload must not own encodeOne's call", lang)
			}
		}
		if _, ok := byQual["Socket.onClose.cleanup"]; !ok {
			t.Errorf("%s: nested function in a method missing: %v", lang, byQual)
		}
	}
}
