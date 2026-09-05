package strategies_test

import (
	"strings"
	"testing"

	"github.com/provasign/astkit"
)

func findImport(imps []astkit.ImportStatement, path string) *astkit.ImportStatement {
	for i := range imps {
		if imps[i].Path == path {
			return &imps[i]
		}
	}
	return nil
}

// Aliased imports must split path and alias — `import typing as t` used to
// store "typing as t" as the Path, which Grove then failed to resolve.
func TestPythonImports_AliasAndNesting(t *testing.T) {
	src := `import os
import typing as t
from collections import OrderedDict

if True:  # TYPE_CHECKING pattern
    import json

def f():
    import re
`
	_, imps := extract(t, astkit.LangPython, src)

	typ := findImport(imps, "typing")
	if typ == nil {
		t.Fatalf("import 'typing' missing (aliased import mis-parsed): %+v", imps)
	}
	if typ.Alias != "t" {
		t.Errorf("typing alias = %q, want t", typ.Alias)
	}
	if findImport(imps, "os") == nil {
		t.Errorf("plain import os missing: %+v", imps)
	}
	if findImport(imps, "collections") == nil {
		t.Errorf("from-import module missing: %+v", imps)
	}
	if findImport(imps, "json") == nil {
		t.Errorf("conditional (nested) import json missing: %+v", imps)
	}
	if findImport(imps, "re") == nil {
		t.Errorf("function-scoped import re missing: %+v", imps)
	}
}

// From-imports carry the bound member names: `from . import cli` binds a
// submodule the consumer can only resolve if it knows `cli` came from here.
func TestPythonImports_FromImportNames(t *testing.T) {
	src := `from . import cli, app as application
from .helpers import _CollectErrors
from os.path import join
`
	_, imps := extract(t, astkit.LangPython, src)
	want := map[string][]string{".": {"cli", "app"}, ".helpers": {"_CollectErrors"}, "os.path": {"join"}}
	for path, names := range want {
		imp := findImport(imps, path)
		if imp == nil {
			t.Fatalf("import %q missing: %+v", path, imps)
		}
		if strings.Join(imp.Names, ",") != strings.Join(names, ",") {
			t.Errorf("%s names = %v, want %v", path, imp.Names, names)
		}
	}
}

// Rust facade crates re-export through `pub extern crate x as y;` and hold
// no items: the re-export must be an import and the file must still
// yield a symbol.
func TestRustImports_ExternCrateAndFacade(t *testing.T) {
	src := `pub extern crate grep_cli as cli;
pub use grep_regex as regex;
pub mod hiargs;
`
	syms, imps := extract(t, astkit.LangRust, src)
	if findImport(imps, "pub use grep_cli as cli") == nil {
		t.Errorf("extern crate re-export not recorded as pub use: %+v", imps)
	}
	if findImport(imps, "pub use grep_regex as regex") == nil {
		t.Errorf("pub use missing: %+v", imps)
	}
	var mods []string
	for _, s := range syms {
		if s.Kind == astkit.KindModule {
			mods = append(mods, s.Name)
		}
	}
	if len(mods) != 1 || mods[0] != "hiargs" {
		t.Errorf("mod declaration symbol = %v, want [hiargs]", mods)
	}
	_, nested := extract(t, astkit.LangRust, "fn f() {}\n#[cfg(test)]\nmod tests {\n    use grep_regex::{RegexMatcher, RegexMatcherBuilder};\n    fn g() { use std::io; }\n}\n")
	if findImport(nested, "grep_regex::{RegexMatcher, RegexMatcherBuilder}") == nil || findImport(nested, "std::io") == nil {
		t.Errorf("nested use declarations missing: %+v", nested)
	}
	syms2, _ := extract(t, astkit.LangRust, "pub extern crate grep_cli as cli;\n")
	if len(syms2) != 1 || syms2[0].Kind != astkit.KindModule {
		t.Errorf("re-export-only file must yield one module symbol, got %+v", syms2)
	}
}

// PHP use/require must store a resolvable Path (qualified name or file), not
// the entire statement text.
func TestPHPImports_PathsAndAliases(t *testing.T) {
	src := `<?php
use App\Service\Mailer;
use Vendor\Long\ClassName as Short;
require_once 'bootstrap.php';
function f() {
    include "inner.php";
}
`
	_, imps := extract(t, astkit.LangPHP, src)

	if findImport(imps, `App\Service\Mailer`) == nil {
		t.Errorf("use clause path missing: %+v", imps)
	}
	aliased := findImport(imps, `Vendor\Long\ClassName`)
	if aliased == nil {
		t.Fatalf("aliased use clause path missing: %+v", imps)
	}
	if aliased.Alias != "Short" {
		t.Errorf("alias = %q, want Short", aliased.Alias)
	}
	if findImport(imps, "bootstrap.php") == nil {
		t.Errorf("require_once path missing: %+v", imps)
	}
	if findImport(imps, "inner.php") == nil {
		t.Errorf("nested include path missing: %+v", imps)
	}
}
