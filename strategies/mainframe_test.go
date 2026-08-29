package strategies

import (
	"strings"
	"testing"

	"github.com/provasign/astkit"
)

// pad builds a fixed-format line: 6-char sequence area, indicator, code
// padded to col 72, then identification-area junk in 73-80 that must be
// ignored.
func pad(seq, ind, code string) string {
	for len(seq) < 6 {
		seq = "0" + seq
	}
	line := seq + ind + code
	for len(line) < 72 {
		line += " "
	}
	return line + "COPYRIGH"
}

var cobolSample = strings.Join([]string{
	pad("000100", " ", " IDENTIFICATION DIVISION."),
	pad("000200", " ", " PROGRAM-ID. CUSTUPD."),
	pad("000300", "*", " THIS IS A COMMENT WITH PIC X(10) INSIDE."),
	pad("000400", " ", " ENVIRONMENT DIVISION."),
	pad("000500", " ", " INPUT-OUTPUT SECTION."),
	pad("000600", " ", "     SELECT CUST-FILE ASSIGN TO CUSTIN."),
	pad("000700", " ", " DATA DIVISION."),
	pad("000800", " ", " FILE SECTION."),
	pad("000900", " ", " FD  CUST-FILE."),
	pad("001000", " ", " 01  CUST-REC."),
	pad("001100", " ", "     05  CUST-KEY."),
	pad("001200", " ", "         10  CUST-ID      PIC 9(8)."),
	pad("001300", " ", "         10  CUST-TYPE    PIC X."),
	pad("001400", " ", "     05  CUST-NAME       PIC X(30)."),
	pad("001500", " ", "     05  CUST-SSN        PIC 9(9)."),
	pad("001600", " ", "     05  CUST-ALT REDEFINES CUST-SSN PIC X(9)."),
	pad("001700", " ", "     05  CUST-ORDERS OCCURS 10 PIC 9(4)."),
	pad("001800", " ", " WORKING-STORAGE SECTION."),
	pad("001900", " ", " 01  WS-FLAGS."),
	pad("002000", " ", "     05  WS-EOF          PIC X VALUE 'N'."),
	pad("002100", " ", "         88  END-OF-FILE VALUE 'Y'."),
	pad("002200", " ", " 77  WS-PGM-NAME         PIC X(8) VALUE 'CUSTRPT '."),
	pad("002300", " ", " COPY CUSTREC."),
	pad("002400", " ", " COPY PAYREC REPLACING ==:TAG:== BY ==CUST==."),
	pad("002500", " ", " PROCEDURE DIVISION."),
	pad("002600", " ", " MAIN-PARA."),
	pad("002700", " ", "     PERFORM INIT-PARA THRU INIT-EXIT."),
	pad("002800", " ", "     CALL 'AUDITLOG' USING CUST-REC."),
	pad("002900", " ", "     CALL WS-PGM-NAME USING CUST-REC."),
	pad("003000", " ", "     STOP RUN."),
	pad("003100", " ", " INIT-PARA."),
	pad("003200", " ", "     MOVE 'N' TO WS-EOF."),
	pad("003300", " ", " INIT-EXIT."),
	pad("003400", " ", "     EXIT."),
}, "\n")

func extractCOBOL(t *testing.T, src string) []astkit.Symbol {
	t.Helper()
	syms, err := NewCOBOL().Extract(nil, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return syms
}

func find(t *testing.T, syms []astkit.Symbol, kind astkit.SymbolKind, name string) astkit.Symbol {
	t.Helper()
	for _, s := range syms {
		if s.Kind == kind && s.Name == name {
			return s
		}
	}
	t.Fatalf("no %s symbol named %s in %+v", kind, name, names(syms))
	return astkit.Symbol{}
}

func names(syms []astkit.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = string(s.Kind) + ":" + s.Name
	}
	return out
}

func TestCOBOL_FixedFormat(t *testing.T) {
	syms := extractCOBOL(t, cobolSample)

	prog := find(t, syms, "program", "CUSTUPD")
	if !prog.Exported {
		t.Error("program not exported")
	}

	// Hierarchy: CUST-ID is under CUST-REC.CUST-KEY.
	id := find(t, syms, "data-item", "CUST-ID")
	if id.QualifiedName != "CUST-REC.CUST-KEY.CUST-ID" {
		t.Errorf("CUST-ID qualified = %q", id.QualifiedName)
	}
	if id.ParentName != "CUST-KEY" {
		t.Errorf("CUST-ID parent = %q", id.ParentName)
	}
	if len(id.Modifiers) == 0 || id.Modifiers[0] != "pic:9(8)" {
		t.Errorf("CUST-ID modifiers = %v", id.Modifiers)
	}

	// Sibling after nested group pops back to the right level.
	nm := find(t, syms, "data-item", "CUST-NAME")
	if nm.QualifiedName != "CUST-REC.CUST-NAME" {
		t.Errorf("CUST-NAME qualified = %q", nm.QualifiedName)
	}

	// REDEFINES and OCCURS captured as modifiers.
	alt := find(t, syms, "data-item", "CUST-ALT")
	if len(alt.Modifiers) < 2 || alt.Modifiers[1] != "redefines:CUST-SSN" {
		t.Errorf("CUST-ALT modifiers = %v", alt.Modifiers)
	}
	occ := find(t, syms, "data-item", "CUST-ORDERS")
	joined := strings.Join(occ.Modifiers, ",")
	if !strings.Contains(joined, "occurs:10") {
		t.Errorf("CUST-ORDERS modifiers = %v", occ.Modifiers)
	}

	// 88-level is a condition name bound to its parent.
	eof := find(t, syms, "condition-name", "END-OF-FILE")
	if eof.ParentName != "WS-EOF" {
		t.Errorf("END-OF-FILE parent = %q", eof.ParentName)
	}

	// The comment line's PIC X(10) must NOT have produced an item, and
	// identification-area text must not appear anywhere.
	for _, s := range syms {
		if strings.Contains(s.Signature, "COPYRIGH") {
			t.Errorf("identification area leaked into %s", s.Name)
		}
	}

	// Paragraphs with call sites: PERFORM THRU yields both endpoints,
	// literal and dynamic CALLs both present.
	main := find(t, syms, "paragraph", "MAIN-PARA")
	var callees []string
	for _, cs := range main.CallSites {
		callees = append(callees, cs.Callee)
	}
	for _, want := range []string{"INIT-PARA", "INIT-EXIT", "AUDITLOG", "WS-PGM-NAME"} {
		if !contains(callees, want) {
			t.Errorf("MAIN-PARA missing callee %s (have %v)", want, callees)
		}
	}

	lf := find(t, syms, "logical-file", "CUST-FILE")
	if lf.ParentName != "CUSTUPD" {
		t.Errorf("logical file parent = %q", lf.ParentName)
	}
}

func TestCOBOL_CopyStatements(t *testing.T) {
	imports, err := NewCOBOL().ExtractImports(nil, []byte(cobolSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 2 {
		t.Fatalf("want 2 COPY imports, got %+v", imports)
	}
	if imports[0].Path != "CUSTREC" || imports[0].Group != "copybook" {
		t.Errorf("first import = %+v", imports[0])
	}
	if imports[1].Path != "PAYREC" || !strings.Contains(imports[1].Raw, "REPLACING") {
		t.Errorf("second import = %+v", imports[1])
	}
}

func TestCOBOL_FreeFormatCopybook(t *testing.T) {
	src := strings.Join([]string{
		"      *> free-format copybook",
		"01 PAY-REC.",
		"   05 PAY-ID   PIC 9(6).",
		"   05 PAY-AMT  PIC S9(7)V99 COMP-3.",
	}, "\n")
	syms := extractCOBOL(t, src)
	amt := find(t, syms, "data-item", "PAY-AMT")
	if amt.QualifiedName != "PAY-REC.PAY-AMT" {
		t.Errorf("PAY-AMT qualified = %q", amt.QualifiedName)
	}
}

var jclSample = strings.Join([]string{
	"//NIGHTLY  JOB (ACCT),'NIGHTLY BATCH',CLASS=A,",
	"//             MSGCLASS=X",
	"//* run the customer update then the report",
	"//UPDATE   EXEC PGM=CUSTUPD",
	"//CUSTIN   DD DSN=PROD.CUST.MASTER,DISP=SHR",
	"//CUSTOUT  DD DSN=PROD.CUST.MASTER.NEW,",
	"//             DISP=(NEW,CATLG,DELETE)",
	"//REPORT   EXEC CUSTRPTP",
	"//SYSIN    DD *",
	"  SORT FIELDS=(1,8,CH,A)",
	"/*",
}, "\n")

func TestJCL_Extract(t *testing.T) {
	syms, err := NewJCL().Extract(nil, []byte(jclSample))
	if err != nil {
		t.Fatal(err)
	}
	job := find(t, syms, "job", "NIGHTLY")
	_ = job
	upd := find(t, syms, "step", "UPDATE")
	if upd.QualifiedName != "NIGHTLY.UPDATE" || upd.ParentName != "NIGHTLY" {
		t.Errorf("UPDATE step = %+v", upd)
	}
	if len(upd.CallSites) != 1 || upd.CallSites[0].Callee != "CUSTUPD" {
		t.Errorf("UPDATE callsites = %+v", upd.CallSites)
	}
	rpt := find(t, syms, "step", "REPORT")
	if len(rpt.CallSites) != 1 || rpt.CallSites[0].Callee != "CUSTRPTP" {
		t.Errorf("REPORT callsites = %+v", rpt.CallSites)
	}
}

func TestJCL_DatasetBindings(t *testing.T) {
	imports, err := NewJCL().ExtractImports(nil, []byte(jclSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 2 {
		t.Fatalf("want 2 dataset bindings, got %+v", imports)
	}
	if imports[0].Path != "PROD.CUST.MASTER" || imports[0].Alias != "CUSTIN" {
		t.Errorf("binding 0 = %+v", imports[0])
	}
	// Continued DD statement still yields its DSN.
	if imports[1].Path != "PROD.CUST.MASTER.NEW" || imports[1].Alias != "CUSTOUT" {
		t.Errorf("binding 1 = %+v", imports[1])
	}
}

func TestMainframe_TextCapableRegistered(t *testing.T) {
	r := Default()
	if !r.TextCapable(astkit.LangCOBOL) || !r.TextCapable(astkit.LangJCL) {
		t.Fatal("mainframe strategies not text-capable in Default registry")
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
