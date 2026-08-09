package strategies_test

import (
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
