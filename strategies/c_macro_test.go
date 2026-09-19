package strategies_test

import (
	"strings"
	"testing"

	"github.com/provasign/astkit"
)

// #define symbols carry their body's calls (with parameter names as
// arguments), and an `extern "C" {` header block is descended into.
func TestCMacrosAndExternCBlock(t *testing.T) {
	src := "#ifdef __cplusplus\nextern \"C\"\n{\n#endif\n#define RUN_TEST(func, num) UnityDefaultTestRun(func, #func, num)\n#define json_object_foreach(object, key, value) \\\n    for(key = json_object_iter_key(json_object_iter(object)); \\\n        key; \\\n        key = json_object_iter_key(json_object_iter_next(object, json_object_key_to_iter(key))))\nint declared_inside(void);\n#ifdef __cplusplus\n}\n#endif\n"
	syms, _ := extract(t, astkit.LangC, src)
	by := map[string]astkit.Symbol{}
	for _, s := range syms {
		by[s.Name] = s
	}
	rt, ok := by["RUN_TEST"]
	if !ok || rt.Kind != astkit.KindMacro || rt.Signature != "#define RUN_TEST(func,num)" {
		t.Fatalf("RUN_TEST = %+v", rt)
	}
	if len(rt.CallSites) != 1 || rt.CallSites[0].Callee != "UnityDefaultTestRun" || strings.Join(rt.CallSites[0].Args, ",") != "func,#String,num" {
		t.Fatalf("RUN_TEST call sites = %+v", rt.CallSites)
	}
	fe, ok := by["json_object_foreach"]
	if !ok {
		t.Fatalf("multi-line macro missing: %v", syms)
	}
	var callees []string
	for _, cs := range fe.CallSites {
		callees = append(callees, cs.Callee)
	}
	for _, want := range []string{"json_object_iter_key", "json_object_iter", "json_object_iter_next", "json_object_key_to_iter"} {
		if !strings.Contains(strings.Join(callees, ","), want) {
			t.Fatalf("foreach body call %q missing in %v", want, callees)
		}
	}
	if _, ok := by["declared_inside"]; !ok {
		t.Fatalf("declaration inside extern \"C\" block not extracted")
	}
}

// A calling-convention macro between the return type and the name
// (`int CJSON_CDECL main(void)`) must not lose the function or its calls.
func TestCCallingConventionMacroFunction(t *testing.T) {
	src := "int CJSON_CDECL main(void)\n{\n    UNITY_BEGIN();\n    RUN_TEST(cjson_add_null_should_add_null);\n    return UNITY_END();\n}\n"
	syms, _ := extract(t, astkit.LangC, src)
	for _, s := range syms {
		if s.Name != "main" {
			continue
		}
		var callees []string
		for _, cs := range s.CallSites {
			callees = append(callees, cs.Callee)
		}
		if got := strings.Join(callees, ","); got != "UNITY_BEGIN,RUN_TEST,UNITY_END" {
			t.Fatalf("main call sites = %s", got)
		}
		return
	}
	t.Fatalf("main not extracted: %+v", syms)
}
