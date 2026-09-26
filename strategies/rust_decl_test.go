package strategies_test

import (
	"testing"

	"github.com/provasign/astkit"
)

func TestRustEnumVariantsAreMembers(t *testing.T) {
	src := `pub enum Shape { Circle(f64), Rect { w: u32, h: u32 }, Empty }
`
	syms, _ := extract(t, astkit.LangRust, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{
		"Shape": astkit.KindEnum, "Shape.Circle": astkit.KindField,
		"Shape.Rect": astkit.KindField, "Shape.Empty": astkit.KindField,
	})
	for _, s := range syms {
		if s.Name == "w" || s.Name == "h" {
			t.Errorf("variant field indexed: %+v", s)
		}
		if s.Name == "Circle" && (!s.Exported || s.Signature != "Circle(f64)") {
			t.Errorf("Circle = %+v", s)
		}
	}
}

func TestRustMacroRulesIsIndexed(t *testing.T) {
	src := `macro_rules! my_vec {
    () => { Vec::new() };
}
`
	syms, _ := extract(t, astkit.LangRust, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{"my_vec": astkit.KindMacro})
}

func TestRustModuleMembersKeepOwnerType(t *testing.T) {
	src := `mod net {
    pub const PORT: u16 = 80;
    pub struct Conn { addr: String }
    impl Conn { pub fn open() -> Self { todo!() } }
    mod tls { pub struct Session { id: u8 } }
}
fn outer() {
    struct Local { x: u8 }
}
`
	syms, _ := extract(t, astkit.LangRust, src)
	got := map[string]astkit.Symbol{}
	for _, s := range syms {
		got[s.QualifiedName] = s
	}
	for qn, parent := range map[string]string{
		"net.PORT": "", "net.Conn": "", "net.Conn.addr": "Conn", "net.Conn.open": "Conn",
		"net.tls.Session": "", "net.tls.Session.id": "Session", "outer.Local.x": "Local",
	} {
		s, ok := got[qn]
		if !ok {
			t.Errorf("%s not indexed; got %v", qn, keysOf(got))
			continue
		}
		if s.ParentName != parent {
			t.Errorf("%s parent = %q, want %q", qn, s.ParentName, parent)
		}
	}
	for _, bad := range []string{"net.addr", "net.open", "net.tls.id", "outer.x"} {
		if _, ok := got[bad]; ok {
			t.Errorf("%s: owner type lost", bad)
		}
	}
}

func TestRustAssociatedConstsAndTypesAreParented(t *testing.T) {
	src := `pub const LIMIT: usize = 10;
static COUNT: u32 = 0;
trait Storage {
    const CAPACITY: usize;
    type Key;
    fn get(&self) -> Self::Key;
}
struct Mem;
impl Storage for Mem {
    const CAPACITY: usize = 8;
    type Key = String;
    fn get(&self) -> String { String::new() }
}
impl Mem { const UNIT: u8 = 1; }
`
	syms, _ := extract(t, astkit.LangRust, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{
		"LIMIT": astkit.KindConst, "COUNT": astkit.KindVariable,
		"Storage.CAPACITY": astkit.KindConst, "Storage.Key": astkit.KindType,
		"Mem.CAPACITY": astkit.KindConst, "Mem.Key": astkit.KindType, "Mem.UNIT": astkit.KindConst,
	})
	for _, s := range syms {
		if (s.Name == "CAPACITY" || s.Name == "Key" || s.Name == "UNIT") && s.ParentName == "" {
			t.Errorf("associated item left unparented: %+v", s)
		}
	}
}

func TestRustUnionIsIndexedWithFields(t *testing.T) {
	src := `pub union Bits { pub i: u32, f: f32 }
`
	syms, _ := extract(t, astkit.LangRust, src)
	gprWantDecls(t, syms, map[string]astkit.SymbolKind{
		"Bits": astkit.KindStruct, "Bits.i": astkit.KindField, "Bits.f": astkit.KindField,
	})
}
