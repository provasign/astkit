package strategies

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tabladrum/grove-suite/astkit"
	"github.com/tabladrum/grove-suite/astkit/internalast"
)

// Java strategy.
type javaStrategy struct{}

func NewJava() *javaStrategy { return &javaStrategy{} }

func (j *javaStrategy) Language() astkit.LanguageKey { return astkit.LangJava }
func (j *javaStrategy) Extensions() []string       { return []string{".java"} }
func (j *javaStrategy) Extract(tree *sitter.Tree, src []byte) ([]astkit.Symbol, error) {
	if tree == nil {
		return nil, nil
	}
	root := tree.RootNode()
	var out []astkit.Symbol
	internalast.WalkChildren(root, func(n *sitter.Node) {
		switch n.Type() {
		case "class_declaration", "interface_declaration", "enum_declaration", "record_declaration":
			out = append(out, javaType(n, src)...)
		}
	})
	return out, nil
}

func javaType(n *sitter.Node, src []byte) []astkit.Symbol {
	name := internalast.NodeText(n.ChildByFieldName("name"), src)
	kind := "class"
	switch n.Type() {
	case "interface_declaration":
		kind = "interface"
	case "enum_declaration":
		kind = "enum"
	case "record_declaration":
		kind = "type"
	}
	modifiers := javaModifiers(n, src)
	out := []astkit.Symbol{{
		QualifiedName: name,
		Kind:      astkit.SymbolKind(kind),
		Name:      name,
		Signature: internalast.FirstLine(internalast.NodeText(n, src)),
		Body:      internalast.NodeText(n, src),
		Span:      internalast.NodeSpan(n),
		Modifiers: modifiers,
		Exported:  internalast.HasModifier(modifiers, "public"),
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
		case "method_declaration":
			methodName := internalast.NodeText(c.ChildByFieldName("name"), src)
			mods := javaModifiers(c, src)
			out = append(out, astkit.Symbol{
				QualifiedName: name + "." + methodName,
				Kind:      "method",
				Name:      methodName,
				ParentName: name,
				Signature: internalast.FirstLine(internalast.NodeText(c, src)),
				Body:      internalast.NodeText(c, src),
				Span:      internalast.NodeSpan(c),
				Modifiers: mods,
				Exported:  internalast.HasModifier(mods, "public"),
			})
		case "field_declaration":
			// emit one symbol per declarator
			for k := 0; k < int(c.ChildCount()); k++ {
				vd := c.Child(k)
				if vd == nil || vd.Type() != "variable_declarator" {
					continue
				}
				fieldName := internalast.NodeText(vd.ChildByFieldName("name"), src)
				if fieldName == "" {
					continue
				}
				mods := javaModifiers(c, src)
				out = append(out, astkit.Symbol{
					QualifiedName: name + "." + fieldName,
					Kind:      "field",
					Name:      fieldName,
					ParentName: name,
					Signature: internalast.FirstLine(internalast.NodeText(c, src)),
					Body:      internalast.NodeText(c, src),
					Span:      internalast.NodeSpan(c),
					Modifiers: mods,
					Exported:  internalast.HasModifier(mods, "public"),
				})
			}
		}
	}
	return out
}

func javaModifiers(n *sitter.Node, src []byte) []string {
	mods := internalast.FindChildByType(n, "modifiers")
	if mods == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(mods.ChildCount()); i++ {
		c := mods.Child(i)
		if c == nil {
			continue
		}
		if c.IsNamed() || c.Type() == "public" || c.Type() == "private" ||
			c.Type() == "protected" || c.Type() == "static" || c.Type() == "final" ||
			c.Type() == "abstract" {
			out = append(out, c.Type())
		}
	}
	return out
}

func (j *javaStrategy) ExtractImports(tree *sitter.Tree, src []byte) ([]astkit.ImportStatement, error) {
	if tree == nil {
		return nil, nil
	}
	root := tree.RootNode()
	var imps []astkit.ImportStatement
	internalast.WalkChildren(root, func(n *sitter.Node) {
		if n.Type() != "import_declaration" {
			return
		}
		raw := strings.TrimSuffix(strings.TrimSpace(internalast.NodeText(n, src)), ";")
		raw = strings.TrimPrefix(raw, "import")
		raw = strings.TrimSpace(raw)
		raw = strings.TrimPrefix(raw, "static")
		raw = strings.TrimSpace(raw)
		imps = append(imps, astkit.ImportStatement{
			Raw:  internalast.NodeText(n, src),
			Path: raw,
			Line: int(n.StartPoint().Row) + 1,
		})
	})
	return imps, nil
}

func (j *javaStrategy) ExtractExports(tree *sitter.Tree, src []byte) ([]astkit.Export, error) {
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
