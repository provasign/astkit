package strategies

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tabladrum/grove-suite/astkit"
	"github.com/tabladrum/grove-suite/astkit/internalast"
)

// Python strategy: indent-based, classes contain methods.
type pythonStrategy struct{}

func NewPython() *pythonStrategy { return &pythonStrategy{} }

func (p *pythonStrategy) Language() astkit.LanguageKey { return astkit.LangPython }
func (p *pythonStrategy) Extensions() []string       { return []string{".py"} }
func (p *pythonStrategy) Extract(tree *sitter.Tree, src []byte) ([]astkit.Symbol, error) {
	if tree == nil {
		return nil, nil
	}
	root := tree.RootNode()
	var out []astkit.Symbol
	internalast.WalkChildren(root, func(n *sitter.Node) {
		switch n.Type() {
		case "function_definition":
			out = append(out, pyFunc(n, src, ""))
		case "decorated_definition":
			if def := findDecoratedTarget(n); def != nil {
				switch def.Type() {
				case "function_definition":
					out = append(out, pyFunc(n, src, ""))
				case "class_definition":
					out = append(out, pyClass(n, src)...)
				}
			}
		case "class_definition":
			out = append(out, pyClass(n, src)...)
		case "expression_statement":
			// module-level assignments (constants)
			if assign := internalast.FindChildByType(n, "assignment"); assign != nil {
				if left := assign.ChildByFieldName("left"); left != nil && left.Type() == "identifier" {
					name := internalast.NodeText(left, src)
					out = append(out, astkit.Symbol{
						QualifiedName: name,
						Kind:      "variable",
						Name:      name,
						Signature: internalast.FirstLine(internalast.NodeText(n, src)),
						Body:      internalast.NodeText(n, src),
						Span:      internalast.NodeSpan(n),
						Exported:  !strings.HasPrefix(name, "_"),
					})
				}
			}
		}
	})
	return out, nil
}

// findDecoratedTarget returns the underlying function/class inside a
// decorated_definition node.
func findDecoratedTarget(n *sitter.Node) *sitter.Node {
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "function_definition" || c.Type() == "class_definition" {
			return c
		}
	}
	return nil
}

// pyFunc extracts a function (possibly wrapped by decorated_definition).
// parent is the enclosing class name (empty for module-level functions).
func pyFunc(n *sitter.Node, src []byte, parent string) astkit.Symbol {
	def := n
	if n.Type() == "decorated_definition" {
		if t := findDecoratedTarget(n); t != nil && t.Type() == "function_definition" {
			def = t
		}
	}
	nameNode := def.ChildByFieldName("name")
	name := internalast.NodeText(nameNode, src)
	key := name
	kind := "function"
	if parent != "" {
		key = parent + "." + name
		kind = "method"
	}
	return astkit.Symbol{
		QualifiedName: key,
		Kind:      astkit.SymbolKind(kind),
		Name:      name,
		ParentName: parent,
		Signature: pyFuncSignature(def, src),
		Body:      internalast.NodeText(n, src),
		Span:      internalast.NodeSpan(n),
		Exported:  !strings.HasPrefix(name, "_"),
	}
}

// pyFuncSignature returns `def name(params) -> return:` minus the body.
func pyFuncSignature(n *sitter.Node, src []byte) string {
	body := n.ChildByFieldName("body")
	if body == nil {
		return internalast.FirstLine(internalast.NodeText(n, src))
	}
	end := body.StartByte()
	if int(end) > len(src) {
		end = uint32(len(src))
	}
	return strings.TrimSpace(string(src[n.StartByte():end]))
}

// pyClass returns the class itself plus one SymbolData per method inside it.
func pyClass(n *sitter.Node, src []byte) []astkit.Symbol {
	def := n
	if n.Type() == "decorated_definition" {
		if t := findDecoratedTarget(n); t != nil && t.Type() == "class_definition" {
			def = t
		}
	}
	nameNode := def.ChildByFieldName("name")
	name := internalast.NodeText(nameNode, src)
	classBody := def.ChildByFieldName("body")
	out := []astkit.Symbol{{
		QualifiedName: name,
		Kind:      "class",
		Name:      name,
		Signature: pyFuncSignature(def, src),
		Body:      internalast.NodeText(n, src),
		Span:      internalast.NodeSpan(n),
		Exported:  !strings.HasPrefix(name, "_"),
	}}
	if classBody == nil {
		return out
	}
	for i := 0; i < int(classBody.ChildCount()); i++ {
		c := classBody.Child(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "function_definition":
			out = append(out, pyFunc(c, src, name))
		case "decorated_definition":
			if t := findDecoratedTarget(c); t != nil && t.Type() == "function_definition" {
				out = append(out, pyFunc(c, src, name))
			}
		}
	}
	return out
}

func (p *pythonStrategy) ExtractImports(tree *sitter.Tree, src []byte) ([]astkit.ImportStatement, error) {
	if tree == nil {
		return nil, nil
	}
	root := tree.RootNode()
	var imps []astkit.ImportStatement
	internalast.WalkChildren(root, func(n *sitter.Node) {
		switch n.Type() {
		case "import_statement":
			// import X, Y, Z
			for i := 0; i < int(n.ChildCount()); i++ {
				c := n.Child(i)
				if c == nil {
					continue
				}
				if c.Type() == "dotted_name" || c.Type() == "aliased_import" {
					path := internalast.NodeText(c, src)
					imps = append(imps, astkit.ImportStatement{
						Raw: internalast.NodeText(n, src), Path: path, Line: int(n.StartPoint().Row) + 1,
					})
				}
			}
		case "import_from_statement":
			modNode := n.ChildByFieldName("module_name")
			mod := internalast.NodeText(modNode, src)
			imps = append(imps, astkit.ImportStatement{
				Raw: internalast.NodeText(n, src), Path: mod, Line: int(n.StartPoint().Row) + 1,
			})
		}
	})
	return imps, nil
}

func (p *pythonStrategy) ExtractExports(tree *sitter.Tree, src []byte) ([]astkit.Export, error) {
	syms, err := p.Extract(tree, src)
	if err != nil {
		return nil, err
	}
	var out []astkit.Export
	for _, s := range syms {
		if s.Exported && s.ParentName == "" {
			out = append(out, astkit.Export{Name: s.Name, Kind: s.Kind})
		}
	}
	return out, nil
}
