package strategies

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tabladrum/grove-suite/astkit"
	"github.com/tabladrum/grove-suite/astkit/internalast"
)

// jsLike is a base strategy shared by TypeScript, TSX, and JavaScript.
type jsLike struct {
	lang astkit.LanguageKey
	exts []string
}

func NewTypeScript(tsx bool) *jsLike {
	if tsx {
		return &jsLike{lang: astkit.LangTSX, exts: []string{".tsx"}}
	}
	return &jsLike{lang: astkit.LangTypeScript, exts: []string{".ts"}}
}

func NewJavaScript() *jsLike {
	return &jsLike{lang: astkit.LangJavaScript, exts: []string{".js", ".jsx", ".mjs", ".cjs"}}
}

func (j *jsLike) Language() astkit.LanguageKey { return j.lang }
func (j *jsLike) Extensions() []string       { return j.exts }
// jsNamedSymbol describes the top-level statement kinds we surface.
func (j *jsLike) Extract(tree *sitter.Tree, src []byte) ([]astkit.Symbol, error) {
	if tree == nil {
		return nil, nil
	}
	root := tree.RootNode()
	var out []astkit.Symbol
	internalast.WalkChildren(root, func(n *sitter.Node) {
		// export wraps an inner declaration we still want to surface.
		exported := false
		inner := n
		if n.Type() == "export_statement" {
			exported = true
			if d := findFirstChildIn(n, []string{
				"function_declaration", "class_declaration", "interface_declaration",
				"type_alias_declaration", "enum_declaration", "lexical_declaration",
				"variable_declaration",
			}); d != nil {
				inner = d
			}
		}
		switch inner.Type() {
		case "function_declaration":
			out = append(out, jsFunc(inner, src, exported))
		case "class_declaration":
			out = append(out, jsClass(inner, src, exported)...)
		case "interface_declaration":
			out = append(out, jsNamed(inner, src, "interface", exported))
		case "type_alias_declaration":
			out = append(out, jsNamed(inner, src, "type", exported))
		case "enum_declaration":
			out = append(out, jsNamed(inner, src, "enum", exported))
		case "lexical_declaration", "variable_declaration":
			out = append(out, jsVar(inner, src, exported)...)
		}
	})
	return out, nil
}

func findFirstChildIn(n *sitter.Node, kinds []string) *sitter.Node {
	want := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c != nil && want[c.Type()] {
			return c
		}
	}
	return nil
}

func jsFunc(n *sitter.Node, src []byte, exported bool) astkit.Symbol {
	name := internalast.NodeText(n.ChildByFieldName("name"), src)
	body := n.ChildByFieldName("body")
	sig := internalast.NodeText(n, src)
	if body != nil {
		end := body.StartByte()
		if int(end) > len(src) {
			end = uint32(len(src))
		}
		sig = strings.TrimSpace(string(src[n.StartByte():end]))
	}
	return astkit.Symbol{
		QualifiedName: name,
		Kind:      "function",
		Name:      name,
		Signature: sig,
		Body:      internalast.NodeText(n, src),
		Span:      internalast.NodeSpan(n),
		Exported:  exported,
	}
}

func jsNamed(n *sitter.Node, src []byte, kind string, exported bool) astkit.Symbol {
	name := internalast.NodeText(n.ChildByFieldName("name"), src)
	return astkit.Symbol{
		QualifiedName: name,
		Kind:      astkit.SymbolKind(kind),
		Name:      name,
		Signature: internalast.FirstLine(internalast.NodeText(n, src)),
		Body:      internalast.NodeText(n, src),
		Span:      internalast.NodeSpan(n),
		Exported:  exported,
	}
}

func jsClass(n *sitter.Node, src []byte, exported bool) []astkit.Symbol {
	name := internalast.NodeText(n.ChildByFieldName("name"), src)
	out := []astkit.Symbol{{
		QualifiedName: name,
		Kind:      "class",
		Name:      name,
		Signature: internalast.FirstLine(internalast.NodeText(n, src)),
		Body:      internalast.NodeText(n, src),
		Span:      internalast.NodeSpan(n),
		Exported:  exported,
	}}
	body := n.ChildByFieldName("body")
	if body == nil {
		return out
	}
	for i := 0; i < int(body.ChildCount()); i++ {
		c := body.Child(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "method_definition":
			methodName := internalast.NodeText(c.ChildByFieldName("name"), src)
			key := name + "." + methodName
			out = append(out, astkit.Symbol{
				QualifiedName: key,
				Kind:      "method",
				Name:      methodName,
				ParentName: name,
				Signature: internalast.FirstLine(internalast.NodeText(c, src)),
				Body:      internalast.NodeText(c, src),
				Span:      internalast.NodeSpan(c),
				Exported:  exported,
			})
		case "field_definition", "public_field_definition":
			fieldName := internalast.NodeText(c.ChildByFieldName("name"), src)
			if fieldName == "" {
				continue
			}
			out = append(out, astkit.Symbol{
				QualifiedName: name + "." + fieldName,
				Kind:      "field",
				Name:      fieldName,
				ParentName: name,
				Signature: internalast.FirstLine(internalast.NodeText(c, src)),
				Body:      internalast.NodeText(c, src),
				Span:      internalast.NodeSpan(c),
				Exported:  exported,
			})
		}
	}
	return out
}

func jsVar(n *sitter.Node, src []byte, exported bool) []astkit.Symbol {
	var out []astkit.Symbol
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil || c.Type() != "variable_declarator" {
			continue
		}
		nameNode := c.ChildByFieldName("name")
		if nameNode == nil || nameNode.Type() != "identifier" {
			continue
		}
		name := internalast.NodeText(nameNode, src)
		kind := "const"
		out = append(out, astkit.Symbol{
			QualifiedName: name,
			Kind:      astkit.SymbolKind(kind),
			Name:      name,
			Signature: internalast.FirstLine(internalast.NodeText(c, src)),
			Body:      internalast.NodeText(c, src),
			Span:      internalast.NodeSpan(c),
			Exported:  exported,
		})
	}
	return out
}

func (j *jsLike) ExtractImports(tree *sitter.Tree, src []byte) ([]astkit.ImportStatement, error) {
	if tree == nil {
		return nil, nil
	}
	root := tree.RootNode()
	var imps []astkit.ImportStatement
	internalast.WalkChildren(root, func(n *sitter.Node) {
		if n.Type() != "import_statement" {
			return
		}
		sourceNode := n.ChildByFieldName("source")
		path := strings.Trim(internalast.NodeText(sourceNode, src), `"'`+"`")
		imps = append(imps, astkit.ImportStatement{
			Raw:  internalast.NodeText(n, src),
			Path: path,
			Line: int(n.StartPoint().Row) + 1,
		})
	})
	return imps, nil
}

func (j *jsLike) ExtractExports(tree *sitter.Tree, src []byte) ([]astkit.Export, error) {
	syms, err := j.Extract(tree, src)
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
