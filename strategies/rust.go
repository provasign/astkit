package strategies

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tabladrum/grove-suite/astkit"
	"github.com/tabladrum/grove-suite/astkit/internalast"
)

// Rust strategy.
type rustStrategy struct{}

func NewRust() *rustStrategy { return &rustStrategy{} }

func (r *rustStrategy) Language() astkit.LanguageKey { return astkit.LangRust }
func (r *rustStrategy) Extensions() []string       { return []string{".rs"} }
func (r *rustStrategy) Extract(tree *sitter.Tree, src []byte) ([]astkit.Symbol, error) {
	if tree == nil {
		return nil, nil
	}
	root := tree.RootNode()
	var out []astkit.Symbol
	internalast.WalkChildren(root, func(n *sitter.Node) {
		out = append(out, rustItem(n, src, "")...)
	})
	return out, nil
}

func rustItem(n *sitter.Node, src []byte, parent string) []astkit.Symbol {
	exported := isRustPub(n, src)
	switch n.Type() {
	case "function_item":
		name := internalast.NodeText(n.ChildByFieldName("name"), src)
		key := name
		if parent != "" {
			key = parent + "." + name
		}
		return []astkit.Symbol{{
			QualifiedName: key, Kind: "function", Name: name, ParentName: parent,
			Signature: rustFuncSignature(n, src),
			Body:      internalast.NodeText(n, src),
			Span:      internalast.NodeSpan(n),
			Exported:  exported,
		}}
	case "struct_item":
		name := internalast.NodeText(n.ChildByFieldName("name"), src)
		return []astkit.Symbol{{
			QualifiedName: name, Kind: "struct", Name: name,
			Signature: internalast.FirstLine(internalast.NodeText(n, src)),
			Body:      internalast.NodeText(n, src),
			Span:      internalast.NodeSpan(n),
			Exported:  exported,
		}}
	case "enum_item":
		name := internalast.NodeText(n.ChildByFieldName("name"), src)
		return []astkit.Symbol{{
			QualifiedName: name, Kind: "enum", Name: name,
			Signature: internalast.FirstLine(internalast.NodeText(n, src)),
			Body:      internalast.NodeText(n, src),
			Span:      internalast.NodeSpan(n),
			Exported:  exported,
		}}
	case "trait_item":
		name := internalast.NodeText(n.ChildByFieldName("name"), src)
		return []astkit.Symbol{{
			QualifiedName: name, Kind: "trait", Name: name,
			Signature: internalast.FirstLine(internalast.NodeText(n, src)),
			Body:      internalast.NodeText(n, src),
			Span:      internalast.NodeSpan(n),
			Exported:  exported,
		}}
	case "type_item":
		name := internalast.NodeText(n.ChildByFieldName("name"), src)
		return []astkit.Symbol{{
			QualifiedName: name, Kind: "type", Name: name,
			Signature: internalast.FirstLine(internalast.NodeText(n, src)),
			Body:      internalast.NodeText(n, src),
			Span:      internalast.NodeSpan(n),
			Exported:  exported,
		}}
	case "const_item", "static_item":
		name := internalast.NodeText(n.ChildByFieldName("name"), src)
		return []astkit.Symbol{{
			QualifiedName: name, Kind: "const", Name: name,
			Signature: internalast.FirstLine(internalast.NodeText(n, src)),
			Body:      internalast.NodeText(n, src),
			Span:      internalast.NodeSpan(n),
			Exported:  exported,
		}}
	case "impl_item":
		typeName := internalast.NodeText(n.ChildByFieldName("type"), src)
		var out []astkit.Symbol
		body := n.ChildByFieldName("body")
		if body == nil {
			return out
		}
		for i := 0; i < int(body.ChildCount()); i++ {
			c := body.Child(i)
			if c != nil {
				out = append(out, rustItem(c, src, typeName)...)
			}
		}
		return out
	}
	return nil
}

func isRustPub(n *sitter.Node, src []byte) bool {
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "visibility_modifier" {
			return strings.HasPrefix(strings.TrimSpace(internalast.NodeText(c, src)), "pub")
		}
	}
	return false
}

func rustFuncSignature(n *sitter.Node, src []byte) string {
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

func (r *rustStrategy) ExtractImports(tree *sitter.Tree, src []byte) ([]astkit.ImportStatement, error) {
	if tree == nil {
		return nil, nil
	}
	root := tree.RootNode()
	var imps []astkit.ImportStatement
	internalast.WalkChildren(root, func(n *sitter.Node) {
		if n.Type() != "use_declaration" {
			return
		}
		raw := strings.TrimSpace(internalast.NodeText(n, src))
		path := strings.TrimSuffix(strings.TrimPrefix(raw, "use "), ";")
		imps = append(imps, astkit.ImportStatement{
			Raw: raw, Path: strings.TrimSpace(path),
			Line: int(n.StartPoint().Row) + 1,
		})
	})
	return imps, nil
}

func (r *rustStrategy) ExtractExports(tree *sitter.Tree, src []byte) ([]astkit.Export, error) {
	syms, err := r.Extract(tree, src)
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
