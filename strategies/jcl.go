package strategies

import (
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/provasign/astkit"
)

// JCL symbol kinds.
const (
	kindJob     astkit.SymbolKind = "job"
	kindStep    astkit.SymbolKind = "step"
	kindJCLProc astkit.SymbolKind = "jcl-procedure"
	kindDataset astkit.SymbolKind = "dataset"
)

// jclStrategy extracts job control: jobs, steps (with the program or
// procedure each executes as a call site), in-stream procedures, and
// dataset bindings (DD statements), which it reports as imports with
// Group "dataset" — Path is the dataset name, Alias the DD name. Job
// control is line-structured; statements continue when the operand field
// ends with a comma.
type jclStrategy struct{}

func NewJCL() *jclStrategy                          { return &jclStrategy{} }
func (j *jclStrategy) Language() astkit.LanguageKey { return astkit.LangJCL }
func (j *jclStrategy) Extensions() []string         { return []string{".jcl", ".prc"} }
func (j *jclStrategy) ExtractsFromText() bool       { return true }

var (
	reJCLStmt = regexp.MustCompile(`^//([A-Za-z0-9$#@]{0,8})\s+(JOB|EXEC|DD|PROC|PEND|SET|INCLUDE|JCLLIB|OUTPUT)\b\s*(.*)$`)
	rePGM     = regexp.MustCompile(`(?i)\bPGM=([A-Za-z0-9$#@]+)`)
	reProcRef = regexp.MustCompile(`(?i)\bPROC=([A-Za-z0-9$#@]+)`)
	reDSN     = regexp.MustCompile(`(?i)\bDSN(?:AME)?=([A-Za-z0-9$#@.&()+-]+)`)
	reBareOp  = regexp.MustCompile(`^([A-Za-z0-9$#@]+)`)
)

// joinJCL merges continued statements: a statement whose operand field ends
// with a comma continues on the next `// ` line. Comments (//*) are dropped.
func joinJCL(src []byte) []srcLine {
	raw := strings.Split(string(src), "\n")
	var out []srcLine
	cont := false
	for i, line := range raw {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "//*") || strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > 72 {
			line = line[:72] // sequence area 73-80 is not code here either
		}
		if cont && strings.HasPrefix(line, "//") {
			out[len(out)-1].text += " " + strings.TrimSpace(strings.TrimPrefix(line, "//"))
			cont = strings.HasSuffix(strings.TrimRight(out[len(out)-1].text, " "), ",")
			continue
		}
		if !strings.HasPrefix(line, "//") {
			continue // in-stream data (after DD *) — not code
		}
		out = append(out, srcLine{text: line, orig: i + 1})
		cont = strings.HasSuffix(strings.TrimRight(line, " "), ",")
	}
	return out
}

func (j *jclStrategy) Extract(tree *sitter.Tree, src []byte) ([]astkit.Symbol, error) {
	_ = tree
	var syms []astkit.Symbol
	var jobName, procName string
	var stepCount int
	var lastStep, lastDD string

	for _, ln := range joinJCL(src) {
		m := reJCLStmt.FindStringSubmatch(ln.text)
		if m == nil {
			continue
		}
		name, op, operands := m[1], strings.ToUpper(m[2]), m[3]
		switch op {
		case "JOB":
			jobName = name
			syms = append(syms, astkit.Symbol{
				Kind: kindJob, Name: name, QualifiedName: name,
				Signature: strings.TrimSpace(ln.text),
				Span:      astkit.LineRange{Start: ln.orig, End: ln.orig},
				Exported:  true,
			})
		case "PROC":
			procName = name
			syms = append(syms, astkit.Symbol{
				Kind: kindJCLProc, Name: name, QualifiedName: name,
				Signature: strings.TrimSpace(ln.text),
				Span:      astkit.LineRange{Start: ln.orig, End: ln.orig},
				Exported:  true,
			})
		case "PEND":
			procName = ""
		case "DD":
			if name != "" {
				lastDD = name // concatenated DDs ("//  DD") reuse the name
			}
			if dm := reDSN.FindStringSubmatch(operands); dm != nil {
				dsn := strings.ToUpper(dm[1])
				parent := lastStep
				syms = append(syms, astkit.Symbol{
					Kind: kindDataset, Name: dsn, QualifiedName: dsn,
					ParentName: parent,
					Signature:  strings.TrimSpace(ln.text),
					Span:       astkit.LineRange{Start: ln.orig, End: ln.orig},
					Modifiers:  []string{"dd:" + strings.ToUpper(lastDD)},
				})
			}
		case "EXEC":
			stepCount++
			parent := jobName
			if procName != "" {
				parent = procName
			}
			if name == "" {
				name = "STEP" // unnamed EXEC; keep it visible
			}
			qn := name
			if parent != "" {
				qn = parent + "." + name
			}
			s := astkit.Symbol{
				Kind: kindStep, Name: name, QualifiedName: qn, ParentName: parent,
				Signature: strings.TrimSpace(ln.text),
				Span:      astkit.LineRange{Start: ln.orig, End: ln.orig},
			}
			// What the step runs: PGM=X, PROC=X, or bare `EXEC PROCNAME`.
			if pm := rePGM.FindStringSubmatch(operands); pm != nil {
				s.CallSites = append(s.CallSites, astkit.CallSite{Callee: strings.ToUpper(pm[1]), Line: ln.orig})
			} else if pm := reProcRef.FindStringSubmatch(operands); pm != nil {
				s.CallSites = append(s.CallSites, astkit.CallSite{Callee: strings.ToUpper(pm[1]), Line: ln.orig})
			} else if pm := reBareOp.FindStringSubmatch(strings.TrimSpace(operands)); pm != nil {
				s.CallSites = append(s.CallSites, astkit.CallSite{Callee: strings.ToUpper(pm[1]), Line: ln.orig})
			}
			syms = append(syms, s)
			lastStep = name
		}
	}
	return syms, nil
}

func (j *jclStrategy) ExtractImports(tree *sitter.Tree, src []byte) ([]astkit.ImportStatement, error) {
	_ = tree
	var imports []astkit.ImportStatement
	var ddName string
	for _, ln := range joinJCL(src) {
		m := reJCLStmt.FindStringSubmatch(ln.text)
		if m == nil {
			continue
		}
		if strings.ToUpper(m[2]) != "DD" {
			continue
		}
		if m[1] != "" {
			ddName = m[1] // concatenated DDs ("//  DD") reuse the previous name
		}
		if dm := reDSN.FindStringSubmatch(m[3]); dm != nil {
			imports = append(imports, astkit.ImportStatement{
				Raw:   strings.TrimSpace(ln.text),
				Path:  strings.ToUpper(dm[1]),
				Alias: strings.ToUpper(ddName),
				Group: "dataset",
				Line:  ln.orig,
			})
		}
	}
	return imports, nil
}
