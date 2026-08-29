package strategies

import (
	"strings"
	"testing"
)

// benchCOBOL is a ~2000-line fixed-format program: a large DATA DIVISION
// (the dominant shape in real estates) plus a PROCEDURE DIVISION with
// PERFORM/CALL sites. Guards extraction throughput — data divisions
// dominate symbol counts per file (spec R-7.2).
var benchCOBOL = func() []byte {
	var b strings.Builder
	w := func(code string) {
		b.WriteString("000000 ")
		b.WriteString(code)
		if n := 65 - len(code); n > 0 {
			b.WriteString(strings.Repeat(" ", n))
		}
		b.WriteString("SEQAREA0\n")
	}
	w(" IDENTIFICATION DIVISION.")
	w(" PROGRAM-ID. BENCHPGM.")
	w(" DATA DIVISION.")
	w(" WORKING-STORAGE SECTION.")
	for g := 0; g < 100; g++ {
		w(" 01  GROUP-" + itoa(g) + ".")
		for f := 0; f < 15; f++ {
			w("     05  FIELD-" + itoa(g) + "-" + itoa(f) + "  PIC X(10).")
		}
	}
	w(" PROCEDURE DIVISION.")
	for p := 0; p < 50; p++ {
		w(" PARA-" + itoa(p) + ".")
		w("     PERFORM PARA-" + itoa((p+1)%50) + ".")
		w("     CALL 'SUBPGM" + itoa(p%10) + "' USING GROUP-0.")
	}
	return []byte(b.String())
}()

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func BenchmarkCOBOL_Extract_2kLines(b *testing.B) {
	s := NewCOBOL()
	b.SetBytes(int64(len(benchCOBOL)))
	for i := 0; i < b.N; i++ {
		if _, err := s.Extract(nil, benchCOBOL); err != nil {
			b.Fatal(err)
		}
	}
}

var benchJCL = func() []byte {
	var b strings.Builder
	b.WriteString("//BIGJOB   JOB (ACCT),'BENCH',CLASS=A\n")
	for s := 0; s < 200; s++ {
		b.WriteString("//STEP" + itoa(s) + "  EXEC PGM=PGM" + itoa(s%20) + "\n")
		b.WriteString("//IN" + itoa(s) + "     DD DSN=PROD.DS" + itoa(s) + ",DISP=SHR\n")
		b.WriteString("//OUT" + itoa(s) + "    DD DSN=PROD.DS" + itoa(s) + ".NEW,\n")
		b.WriteString("//             DISP=(NEW,CATLG,DELETE)\n")
	}
	return []byte(b.String())
}()

func BenchmarkJCL_Extract_200Steps(b *testing.B) {
	s := NewJCL()
	b.SetBytes(int64(len(benchJCL)))
	for i := 0; i < b.N; i++ {
		if _, err := s.Extract(nil, benchJCL); err != nil {
			b.Fatal(err)
		}
		if _, err := s.ExtractImports(nil, benchJCL); err != nil {
			b.Fatal(err)
		}
	}
}
