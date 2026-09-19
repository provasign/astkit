package strategies_test

import (
	"strings"
	"testing"

	"github.com/provasign/astkit"
)

// Kotlin constructors, `in` operator desugaring, and constructor-call
// argument tokens: the pieces Grove's overload narrowing leans on.
func TestExtract_Kotlin_ConstructorsAndInOperator(t *testing.T) {
	src := `package demo

class Arguments(val parts: List<String>) {
    operator fun contains(s: String): Boolean = s in parts
}

class Plain {
    constructor(x: Int) : this() {
    }
    constructor() {
    }
}

class Bare

interface Shape

class Shell(dir: File? = null, private val flag: Boolean = defaultFlag()) {
    private val files = FileCommands(this)
    val computed: Int get() = compute()
    init {
        warmUp()
    }
    fun run(exe: Executable, args: Arguments): Command = exe + args
}

fun run(args: Arguments, key: String): Boolean {
    val a = Arguments(listOf("x"))
    if (key !in args) return false
    return key in a
}
`
	syms := func() []astkit.Symbol { s, _ := extract(t, astkit.LangKotlin, src); return s }()
	byKindName := map[string][]astkit.Symbol{}
	for _, s := range syms {
		byKindName[string(s.Kind)+":"+s.Name] = append(byKindName[string(s.Kind)+":"+s.Name], s)
	}
	ctors := byKindName["constructor:Arguments"]
	if len(ctors) != 1 || !strings.HasPrefix(ctors[0].Signature, "Arguments(val parts") {
		t.Fatalf("Arguments constructor = %+v", ctors)
	}
	if got := byKindName["constructor:Plain"]; len(got) != 2 {
		t.Fatalf("Plain constructors = %d, want 2 (secondary only, no implicit)", len(got))
	} else {
		var sawDelegation bool
		for _, c := range got {
			for _, cs := range c.CallSites {
				if cs.Callee == "this()" {
					sawDelegation = true
				}
			}
		}
		if !sawDelegation {
			t.Fatalf("secondary constructor delegation not recorded: %+v", got)
		}
	}
	if got := byKindName["constructor:Bare"]; len(got) != 1 || got[0].Signature != "Bare()" {
		t.Fatalf("Bare implicit constructor = %+v", got)
	}
	if got := byKindName["constructor:Shape"]; len(got) != 0 {
		t.Fatalf("interfaces must not get constructors: %+v", got)
	}

	run := byKindName["function:run"]
	if len(run) != 1 {
		t.Fatalf("run = %+v", run)
	}
	var callees []string
	for _, cs := range run[0].CallSites {
		callees = append(callees, cs.Callee+"|"+strings.Join(cs.Args, ","))
	}
	joined := strings.Join(callees, ";")
	for _, want := range []string{"args.contains|key", "a.contains|key", "Arguments|call:listOf"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing call site %q in %s", want, joined)
		}
	}
	shell := byKindName["constructor:Shell"]
	if len(shell) != 1 {
		t.Fatalf("Shell constructor = %+v", shell)
	}
	var shellSites []string
	for _, cs := range shell[0].CallSites {
		shellSites = append(shellSites, cs.Callee)
	}
	joinedShell := strings.Join(shellSites, ";")
	for _, want := range []string{"defaultFlag", "FileCommands", "warmUp"} {
		if !strings.Contains(joinedShell, want) {
			t.Errorf("primary constructor misses %q: %s", want, joinedShell)
		}
	}
	if strings.Contains(joinedShell, "compute") {
		t.Errorf("getter body wrongly attributed to constructor: %s", joinedShell)
	}
	runM := byKindName["method:run"]
	if len(runM) != 1 || len(runM[0].CallSites) != 1 || runM[0].CallSites[0].Callee != "exe.plus" || runM[0].CallSites[0].Args[0] != "args" {
		t.Errorf("`exe + args` not desugared to exe.plus(args): %+v", runM)
	}
	contains := byKindName["method:contains"]
	if len(contains) != 1 {
		t.Fatalf("contains = %+v", contains)
	}
	var sawSelf bool
	for _, cs := range contains[0].CallSites {
		if cs.Callee == "parts.contains" {
			sawSelf = true
		}
	}
	if !sawSelf {
		t.Errorf("`s in parts` not desugared: %+v", contains[0].CallSites)
	}
}
