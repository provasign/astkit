package strategies

import (
	"regexp"
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/provasign/astkit"
	"github.com/provasign/astkit/internalast"
)

// ─── Go ───────────────────────────────────────────────────────────────────────

// extractGoNodes walks the top-level children of a Go source_file node.
// Only package-level declarations are extracted; symbols inside function
// bodies (local vars, closures) are intentionally skipped.
func extractGoNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	for i := 0; i < int(root.ChildCount()); i++ {
		n := root.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "function_declaration":
			if sym := goFuncSym(n, filePath, blobSHA, src, imports); sym != nil {
				out = append(out, *sym)
			}
		case "method_declaration":
			if sym := goMethodSym(n, filePath, blobSHA, src, imports); sym != nil {
				out = append(out, *sym)
			}
		case "type_declaration":
			out = append(out, goTypeDecl(n, filePath, blobSHA, src, imports)...)
		case "const_declaration":
			out = append(out, goConstDecl(n, filePath, blobSHA, src, imports)...)
		case "var_declaration":
			out = append(out, goVarDecl(n, filePath, blobSHA, src, imports)...)
		}
	}
	return out
}

func goFuncSym(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string) *astkit.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	body := n.ChildByFieldName("body")
	return &astkit.Symbol{
		Kind:           astkit.KindFunction,
		Name:           name,
		QualifiedName:  name,
		Signature:      funcSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       internalast.IsCapitalized(name),
		Body:           raw,
		TypeParameters: goTypeParameters(n, src),
		CallSites:      goCallSites(body, src),
	}
}

func goMethodSym(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string) *astkit.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	receiver := goReceiverTypeName(n, src)
	raw := n.Content(src)
	body := n.ChildByFieldName("body")
	return &astkit.Symbol{
		Kind:           astkit.KindMethod,
		Name:           name,
		QualifiedName:  name,
		Signature:      funcSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       internalast.IsCapitalized(name),
		Body:           raw,
		ParentName:     receiver,
		TypeParameters: goTypeParameters(n, src),
		CallSites:      goCallSites(body, src),
	}
}

// goReceiverTypeName extracts the bare type name from a method receiver.
// For `func (s *Service) Login(...)` it returns "Service".
func goReceiverTypeName(method *sitter.Node, src []byte) string {
	recv := method.ChildByFieldName("receiver")
	if recv == nil {
		return ""
	}
	for i := 0; i < int(recv.ChildCount()); i++ {
		param := recv.Child(i)
		if param == nil || param.Type() != "parameter_declaration" {
			continue
		}
		typeNode := param.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		if name := goReceiverNamedType(typeNode, src); name != "" {
			return name
		}
	}
	return ""
}

// goReceiverNamedType unwraps pointer and generic receiver types. The Go
// grammar nests Task[T] under generic_type (and *Task[T] under pointer_type),
// so looking only at the receiver type's direct children loses the method's
// parent entirely.
func goReceiverNamedType(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	if n.Type() == "type_identifier" {
		return n.Content(src)
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if name := goReceiverNamedType(n.Child(i), src); name != "" {
			return name
		}
	}
	return ""
}

// goTypeDecl handles `type X struct{}`, `type X interface{}`, `type X Y`
// and grouped `type (X ...; Y ...)` declarations.
func goTypeDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	for i := 0; i < int(n.ChildCount()); i++ {
		spec := n.Child(i)
		if spec == nil || spec.Type() != "type_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		kind := astkit.KindType
		typeNode := spec.ChildByFieldName("type")
		if typeNode != nil {
			switch typeNode.Type() {
			case "struct_type":
				kind = astkit.KindStruct
			case "interface_type":
				kind = astkit.KindInterface
			}
		}
		raw := goStandaloneDecl("type", spec.Content(src))
		out = append(out, astkit.Symbol{
			Kind:           kind,
			Name:           name,
			QualifiedName:  name,
			Signature:      internalast.FirstLine(raw),
			Span:           internalast.NodeSpan(spec),
			Exported:       internalast.IsCapitalized(name),
			Body:           raw,
			TypeParameters: goTypeParameters(spec, src),
		})
		if kind == astkit.KindStruct {
			out = append(out, goStructFields(typeNode, name, src)...)
		}
	}
	return out
}

// goStructFields emits one KindField per named field of a struct type, parented
// to the struct. Go was the only field-bearing language with none indexed, so
// `lookup Context.Errors` (a gin field) fell through to errorMsgs.Errors, another
// type's method (2026-09-25). One symbol per name in `A, B int`. Embedded fields
// have no name of their own and are skipped: naming them after their type would
// put a second symbol under every embedded type's name. The signature is
// "Name Type" without the struct tag, so tag text never reaches type matching.
func goStructFields(structType *sitter.Node, parent string, src []byte) []astkit.Symbol {
	list := internalast.FindChildByType(structType, "field_declaration_list")
	if list == nil {
		return nil
	}
	var out []astkit.Symbol
	for i := 0; i < int(list.NamedChildCount()); i++ {
		fd := list.NamedChild(i)
		if fd == nil || fd.Type() != "field_declaration" {
			continue
		}
		typeNode := fd.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		typ := typeNode.Content(src)
		for j := 0; j < int(fd.NamedChildCount()); j++ {
			nameNode := fd.NamedChild(j)
			if nameNode == nil || nameNode.Type() != "field_identifier" {
				continue
			}
			name := nameNode.Content(src)
			out = append(out, astkit.Symbol{
				Kind:          astkit.KindField,
				Name:          name,
				QualifiedName: name,
				ParentName:    parent,
				Signature:     name + " " + typ,
				Span:          internalast.NodeSpan(fd),
				Exported:      internalast.IsCapitalized(name),
				Body:          fd.Content(src),
			})
		}
	}
	return out
}

// goConstDecl handles `const X = ...` and grouped `const (X = ...; Y = ...)`.
func goConstDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	for i := 0; i < int(n.ChildCount()); i++ {
		spec := n.Child(i)
		if spec == nil || spec.Type() != "const_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		raw := goStandaloneDecl("const", spec.Content(src))
		out = append(out, astkit.Symbol{
			Kind:          astkit.KindConst,
			Name:          name,
			QualifiedName: name,
			Signature:     strings.TrimSpace(raw),
			Span:          internalast.NodeSpan(spec),
			Exported:      internalast.IsCapitalized(name),
			Body:          raw,
		})
	}
	return out
}

// goVarDecl handles `var X = ...` and grouped `var (X = ...; Y = ...)`.
// Only package-level var declarations reach this function (called from the root walk).
func goVarDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	for i := 0; i < int(n.ChildCount()); i++ {
		spec := n.Child(i)
		if spec == nil || spec.Type() != "var_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		raw := goStandaloneDecl("var", spec.Content(src))
		for _, name := range goIdentifierNames(nameNode, src) {
			out = append(out, astkit.Symbol{
				Kind:          astkit.KindVariable,
				Name:          name,
				QualifiedName: name,
				Signature:     internalast.FirstLine(raw),
				Span:          internalast.NodeSpan(spec),
				Exported:      internalast.IsCapitalized(name),
				Body:          raw,
			})
		}
	}
	return out
}

func goStandaloneDecl(keyword, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, keyword+" ") || strings.HasPrefix(trimmed, keyword+"(") {
		return raw
	}
	return keyword + " " + raw
}

// goIdentifierNames extracts one or more identifier names from a node that may
// be a bare "identifier" or a comma-separated list.
func goIdentifierNames(n *sitter.Node, src []byte) []string {
	if n.Type() == "identifier" {
		return []string{n.Content(src)}
	}
	var names []string
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c != nil && c.Type() == "identifier" {
			names = append(names, c.Content(src))
		}
	}
	return names
}

// ─── TypeScript / JavaScript (including TSX and JSX) ─────────────────────────
//
// JS/TS export detection: the `exported` parameter is set to true when the
// symbol is a direct child of an `export_statement` node. This correctly marks
// lowercase symbols like `export function login()` as exported.

func extractJSNodes(root *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	jsVisit(root, filePath, blobSHA, language, src, imports, "", false, &out)
	for name := range jsNamedExports(root, src) {
		for i := range out {
			if out[i].ParentName == "" && out[i].Name == name {
				out[i].Exported = true
			}
		}
	}
	return out
}

// jsNamedExports collects the local binding in `export { local as public }`.
// These clauses are separate statements, often after the declaration, so the
// declaration visitor cannot learn export status while constructing a symbol.
func jsNamedExports(root *sitter.Node, src []byte) map[string]bool {
	out := map[string]bool{}
	if root == nil {
		return out
	}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		stmt := root.NamedChild(i)
		if stmt == nil || stmt.Type() != "export_statement" || stmt.ChildByFieldName("source") != nil {
			continue
		}
		for j := 0; j < int(stmt.NamedChildCount()); j++ {
			clause := stmt.NamedChild(j)
			if clause == nil || clause.Type() != "export_clause" {
				continue
			}
			for k := 0; k < int(clause.NamedChildCount()); k++ {
				spec := clause.NamedChild(k)
				if spec == nil || spec.Type() != "export_specifier" {
					continue
				}
				if name := spec.ChildByFieldName("name"); name != nil {
					out[name.Content(src)] = true
				}
			}
		}
	}
	return out
}

func jsVisit(node *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string, parentClass string, exported bool, out *[]astkit.Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		jsVisitChild(node.Child(i), filePath, blobSHA, language, src, imports, parentClass, exported, out)
	}
}

func jsVisitChild(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string, parentClass string, exported bool, out *[]astkit.Symbol) {
	if n == nil {
		return
	}
	switch n.Type() {
	case "function_declaration", "generator_function_declaration":
		if sym := jsNamedSym(n, "name", filePath, blobSHA, language, src, imports, astkit.KindFunction, parentClass, exported); sym != nil {
			*out = append(*out, *sym)
		}
	case "class_declaration", "abstract_class_declaration":
		jsClassDecl(n, filePath, blobSHA, language, src, imports, parentClass, exported, out)
	case "interface_declaration": // TypeScript / TSX
		if sym := jsNamedSym(n, "name", filePath, blobSHA, language, src, imports, astkit.KindInterface, parentClass, exported); sym != nil {
			*out = append(*out, *sym)
		}
	case "type_alias_declaration": // TypeScript / TSX
		if sym := jsNamedSym(n, "name", filePath, blobSHA, language, src, imports, astkit.KindType, parentClass, exported); sym != nil {
			*out = append(*out, *sym)
		}
	case "enum_declaration": // TypeScript / TSX
		if sym := jsNamedSym(n, "name", filePath, blobSHA, language, src, imports, astkit.KindEnum, parentClass, exported); sym != nil {
			*out = append(*out, *sym)
		}
	case "internal_module", "module": // TS `namespace Foo {}` / `module Foo {}`
		if sym := jsNamedSym(n, "name", filePath, blobSHA, language, src, imports, astkit.KindNamespace, parentClass, exported); sym != nil {
			*out = append(*out, *sym)
			body := n.ChildByFieldName("body")
			if body != nil {
				jsVisit(body, filePath, blobSHA, language, src, imports, sym.QualifiedName, false, out)
			}
		}
	case "method_definition", "abstract_method_signature":
		// Class methods are never themselves exported even if the class is.
		// Abstract method signatures are real declarations: they're the
		// dispatch point subclass implementations override, so graph
		// consumers need them as symbols.
		jsMethodDef(n, filePath, blobSHA, language, src, imports, parentClass, out)
	case "public_field_definition", "field_definition":
		jsFieldDef(n, filePath, blobSHA, language, src, imports, parentClass, out)
	case "export_statement":
		// Unwrap export_statement and mark children as exported.
		jsUnwrapExport(n, filePath, blobSHA, language, src, imports, parentClass, out)
	case "lexical_declaration", "variable_declaration":
		// const Foo = () => ... / const Foo = function() { ... }
		jsArrowDecl(n, filePath, blobSHA, language, src, imports, parentClass, exported, out)
	case "expression_statement":
		// app.listen = function listen() {} / exports.render = () => {} —
		// the assignment-style declarations CommonJS codebases are built on.
		jsAssignFunc(n, filePath, blobSHA, language, src, imports, parentClass, out)
	}
}

// jsAssignFunc extracts `obj.prop = function...` / `obj.prop = (...) => ...`
// assignments as function symbols named by the property. The receiver chain's
// last identifier becomes ParentName ("app.listen = ..." → parent "app"),
// except module-export forms (exports/module.exports/prototype chains keep
// the prototype's class name).
func jsAssignFunc(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	for i := 0; i < int(n.ChildCount()); i++ {
		assign := n.Child(i)
		if assign == nil || assign.Type() != "assignment_expression" {
			continue
		}
		left := assign.ChildByFieldName("left")
		right := assign.ChildByFieldName("right")
		if left == nil || right == nil || left.Type() != "member_expression" {
			continue
		}
		switch right.Type() {
		case "arrow_function", "function", "function_expression":
		default:
			continue
		}
		prop := left.ChildByFieldName("property")
		if prop == nil {
			continue
		}
		name := prop.Content(src)
		parent := parentClass
		if obj := left.ChildByFieldName("object"); obj != nil && parent == "" {
			switch obj.Type() {
			case "identifier":
				if o := obj.Content(src); o != "exports" && o != "module" {
					parent = o
				}
			case "member_expression":
				// X.prototype.method = ... → parent X
				if inner := obj.ChildByFieldName("property"); inner != nil && inner.Content(src) == "prototype" {
					if base := obj.ChildByFieldName("object"); base != nil && base.Type() == "identifier" {
						parent = base.Content(src)
					}
				}
			}
		}
		raw := assign.Content(src)
		target := left.Content(src)
		exported := strings.HasPrefix(target, "exports.") || strings.HasPrefix(target, "module.exports.")
		k := astkit.KindFunction
		if parent != "" {
			k = astkit.KindMethod
		}
		body := right.ChildByFieldName("body")
		*out = append(*out, astkit.Symbol{
			Kind:          k,
			Name:          name,
			QualifiedName: name,
			Signature:     internalast.FirstLine(raw),
			Span:          internalast.NodeSpan(assign),
			Exported:      exported,
			Body:          raw,
			ParentName:    parent,
			CallSites:     jsCallSites(body, src),
		})
	}
}

func jsClassDecl(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string, parentClass string, exported bool, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	className := nameNode.Content(src)
	raw := n.Content(src)
	var decoratorCalls []astkit.CallSite
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child != nil && child.Type() == "decorator" {
			decoratorCalls = append(decoratorCalls, jsCallSites(child, src)...)
		}
	}
	*out = append(*out, astkit.Symbol{
		Kind:           astkit.KindClass,
		Name:           className,
		QualifiedName:  qualJoin(parentClass, className),
		Signature:      internalast.SignatureBeforeBody(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       exported,
		Body:           raw,
		ParentName:     qualLast(parentClass),
		Modifiers:      jsModifiers(n, src),
		TypeParameters: jsTypeParameters(n, src),
		Annotations:    jsDecorators(n, src),
		CallSites:      decoratorCalls,
	})
	// Visit class body for methods. Methods are never directly exported.
	body := n.ChildByFieldName("body")
	if body != nil {
		jsVisit(body, filePath, blobSHA, language, src, imports, qualJoin(parentClass, className), false, out)
	}
}

func jsMethodDef(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	kind := astkit.KindMethod
	if name == "constructor" {
		kind = astkit.KindConstructor
	}
	body := n.ChildByFieldName("body")
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  qualJoin(parentClass, name),
		Signature:      funcSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       false, // methods are accessed via their class, not exported directly
		Body:           raw,
		ParentName:     qualLast(parentClass),
		Modifiers:      jsModifiers(n, src),
		TypeParameters: jsTypeParameters(n, src),
		Annotations:    jsDecorators(n, src),
		CallSites:      jsCallSites(body, src),
	})
}

// jsFieldDef emits a Field symbol for a TypeScript/JS class field.
func jsFieldDef(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		nameNode = n.ChildByFieldName("property")
	}
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	var callSites []astkit.CallSite
	if value := n.ChildByFieldName("value"); value != nil {
		switch value.Type() {
		case "arrow_function", "function", "function_expression":
			callSites = jsCallSites(value.ChildByFieldName("body"), src)
		}
	}
	*out = append(*out, astkit.Symbol{
		Kind:          astkit.KindField,
		Name:          name,
		QualifiedName: qualJoin(parentClass, name),
		Signature:     internalast.FirstLine(raw),
		Span:          internalast.NodeSpan(n),
		Exported:      false,
		Body:          raw,
		ParentName:    qualLast(parentClass),
		Modifiers:     jsModifiers(n, src),
		Annotations:   jsDecorators(n, src),
		CallSites:     callSites,
	})
}

func jsNamedSym(n *sitter.Node, field, filePath, blobSHA, language string, src []byte, imports []string, kind astkit.SymbolKind, parentClass string, exported bool) *astkit.Symbol {
	nameNode := n.ChildByFieldName(field)
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	k := kind
	if parentClass != "" && kind == astkit.KindFunction {
		k = astkit.KindMethod
	}
	body := n.ChildByFieldName("body")
	var callSites []astkit.CallSite
	if k == astkit.KindFunction || k == astkit.KindMethod {
		callSites = jsCallSites(body, src)
	}
	return &astkit.Symbol{
		Kind:           k,
		Name:           name,
		QualifiedName:  qualJoin(parentClass, name),
		Signature:      funcSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       exported,
		Body:           raw,
		ParentName:     qualLast(parentClass),
		Modifiers:      jsModifiers(n, src),
		TypeParameters: jsTypeParameters(n, src),
		Annotations:    jsDecorators(n, src),
		CallSites:      callSites,
	}
}

// jsUnwrapExport unwraps an export_statement and visits its children with
// exported=true, so that `export function login()` correctly sets Exports=true.
func jsUnwrapExport(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	decl := n.ChildByFieldName("declaration")
	if decl != nil {
		jsVisitChild(decl, filePath, blobSHA, language, src, imports, parentClass, true, out)
		return
	}
	// export default <expr> — iterate direct children for known declaration types
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "function_declaration", "class_declaration",
			"abstract_class_declaration",
			"interface_declaration", "type_alias_declaration",
			"lexical_declaration", "variable_declaration",
			"enum_declaration", "generator_function_declaration":
			jsVisitChild(c, filePath, blobSHA, language, src, imports, parentClass, true, out)
		}
	}
}

func jsArrowDecl(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string, parentClass string, exported bool, out *[]astkit.Symbol) {
	for i := 0; i < int(n.ChildCount()); i++ {
		decl := n.Child(i)
		if decl == nil || decl.Type() != "variable_declarator" {
			continue
		}
		nameNode := decl.ChildByFieldName("name")
		valueNode := decl.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil {
			continue
		}
		switch valueNode.Type() {
		case "arrow_function", "function", "function_expression":
			name := nameNode.Content(src)
			raw := decl.Content(src)
			k := astkit.KindFunction
			if parentClass != "" {
				k = astkit.KindMethod
			}
			body := valueNode.ChildByFieldName("body")
			*out = append(*out, astkit.Symbol{
				Kind:           k,
				Name:           name,
				QualifiedName:  qualJoin(parentClass, name),
				Signature:      internalast.FirstLine(raw),
				Span:           internalast.NodeSpan(decl),
				Exported:       exported,
				Body:           raw,
				ParentName:     qualLast(parentClass),
				TypeParameters: jsTypeParameters(valueNode, src),
				CallSites:      jsCallSites(body, src),
			})
		case "object":
			objectName := nameNode.Content(src)
			*out = append(*out, astkit.Symbol{
				Kind: astkit.KindVariable, Name: objectName,
				QualifiedName: qualJoin(parentClass, objectName),
				Signature:     internalast.FirstLine(decl.Content(src)),
				Span:          internalast.NodeSpan(decl), Exported: exported,
				Body: decl.Content(src), ParentName: qualLast(parentClass),
			})
			objectParent := qualJoin(parentClass, objectName)
			for j := 0; j < int(valueNode.NamedChildCount()); j++ {
				member := valueNode.NamedChild(j)
				if member == nil {
					continue
				}
				if member.Type() == "method_definition" {
					jsMethodDef(member, filePath, blobSHA, language, src, imports, objectParent, out)
					continue
				}
				if member.Type() != "pair" {
					continue
				}
				key, value := member.ChildByFieldName("key"), member.ChildByFieldName("value")
				if key == nil || value == nil {
					continue
				}
				switch value.Type() {
				case "arrow_function", "function", "function_expression":
				default:
					continue
				}
				memberName := strings.Trim(key.Content(src), `"'`)
				*out = append(*out, astkit.Symbol{
					Kind: astkit.KindMethod, Name: memberName,
					QualifiedName: qualJoin(objectParent, memberName),
					Signature:     internalast.FirstLine(member.Content(src)),
					Span:          internalast.NodeSpan(member), Body: member.Content(src),
					ParentName: qualLast(objectParent),
					CallSites:  jsCallSites(value.ChildByFieldName("body"), src),
				})
			}
		}
	}
}

// ─── Python ──────────────────────────────────────────────────────────────────

func extractPythonNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	pythonVisit(root, filePath, blobSHA, src, imports, "", "", false, &out)
	if top := pythonTopLevelSymbol(root, src); top != nil {
		out = append(out, *top)
	}
	return out
}

func pythonVisit(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass, qualifier string, inFunction bool, out *[]astkit.Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		n := node.Child(i)
		if n == nil {
			continue
		}
		pythonVisitDefinition(n, filePath, blobSHA, src, imports, parentClass, qualifier, inFunction, nil, out)
	}
}

func pythonVisitDefinition(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass, qualifier string, inFunction bool, decorators []string, out *[]astkit.Symbol) {
	switch n.Type() {
	case "function_definition":
		nameNode := n.ChildByFieldName("name")
		if nameNode == nil {
			return
		}
		name := nameNode.Content(src)
		kind := astkit.KindFunction
		if parentClass != "" && !inFunction {
			kind = astkit.KindMethod
			if name == "__init__" {
				kind = astkit.KindConstructor
			}
		}
		raw := n.Content(src)
		body := n.ChildByFieldName("body")
		qualified := qualJoin(qualifier, name)
		*out = append(*out, astkit.Symbol{
			Kind:          kind,
			Name:          name,
			QualifiedName: qualified,
			Signature:     internalast.FirstLine(raw),
			Span:          internalast.NodeSpan(n),
			Exported:      !strings.HasPrefix(name, "_"),
			Body:          raw,
			ParentName:    qualLast(qualifier),
			Modifiers:     pythonModifiers(name),
			Annotations:   decorators,
			CallSites:     pythonCallSites(body, src),
			AttrSites:     pythonAttrSites(body, src),
		})
		pythonVisit(body, filePath, blobSHA, src, imports, parentClass, qualified, true, out)
	case "class_definition":
		nameNode := n.ChildByFieldName("name")
		if nameNode == nil {
			return
		}
		className := nameNode.Content(src)
		raw := n.Content(src)
		sig := internalast.SignatureBeforeBody(n, src)
		kind := astkit.KindClass
		if open, close := strings.IndexByte(sig, '('), strings.LastIndexByte(sig, ')'); open >= 0 && close > open {
			for _, base := range strings.Split(sig[open+1:close], ",") {
				base = strings.TrimSpace(base)
				if i := strings.IndexByte(base, '['); i >= 0 {
					base = base[:i]
				}
				if i := strings.LastIndexByte(base, '.'); i >= 0 {
					base = base[i+1:]
				}
				if strings.TrimSpace(base) == "Protocol" {
					kind = astkit.KindInterface
					break
				}
			}
		}
		*out = append(*out, astkit.Symbol{
			Kind:          kind,
			Name:          className,
			QualifiedName: qualJoin(qualifier, className),
			Signature:     sig,
			Span:          internalast.NodeSpan(n),
			Exported:      !strings.HasPrefix(className, "_"),
			Body:          raw,
			ParentName:    qualLast(qualifier),
			Modifiers:     pythonModifiers(className),
			Annotations:   decorators,
		})
		body := n.ChildByFieldName("body")
		if body != nil {
			classQualified := qualJoin(qualifier, className)
			pythonVisit(body, filePath, blobSHA, src, imports, classQualified, classQualified, false, out)
		}
	case "decorated_definition":
		decos := pythonDecorators(n, src)
		for j := 0; j < int(n.ChildCount()); j++ {
			inner := n.Child(j)
			if inner != nil && (inner.Type() == "function_definition" || inner.Type() == "class_definition") {
				pythonVisitDefinition(inner, filePath, blobSHA, src, imports, parentClass, qualifier, inFunction, decos, out)
				return
			}
		}
	case "if_statement", "try_statement", "with_statement",
		"while_statement", "for_statement", "match_statement":
		// Conditionally-defined symbols still exist: a class under
		// `if TYPE_CHECKING:` (Flask's LocalProxy stub classes), a
		// def in a try/except import fallback, or a platform-specific
		// class in an `if sys.platform ...` branch. Recurse into every
		// nested block so these are indexed rather than silently dropped.
		pythonVisitBlocks(n, filePath, blobSHA, src, imports, parentClass, qualifier, inFunction, out)
	case "expression_statement":
		if inFunction {
			return // function locals are not declarations in the repository graph
		}
		// Module scope: annotated assignment ("g: _AppCtxGlobalsProxy = ...")
		// becomes a KindVariable — module globals are otherwise invisible,
		// blocking call resolution through proxy globals (Flask's `g`).
		//
		// Class body: EVERY attribute becomes a KindField (measured
		// 2026-08-30: Python classes previously indexed ZERO attributes of
		// any shape — plain `x = 5`, annotated `x: int = 5`, bare
		// `x: int` dataclass/pydantic fields, SQLAlchemy
		// `x: Mapped[str] = mapped_column(...)` — making field-level
		// change-impact impossible for Python and leaving the Jinja
		// template-binding edges with no field targets). The earlier
		// rationale ("grove's class-attr inference recovers the types")
		// conflated type inference with symbol existence: inference feeds
		// call RESOLUTION, but a symbol that is never indexed cannot be
		// queried, searched, or bound to. Function-local annotations remain
		// unindexed — pythonVisit only descends class bodies, not function
		// bodies, so locals never reach here.
		for j := 0; j < int(n.ChildCount()); j++ {
			a := n.Child(j)
			if a == nil || a.Type() != "assignment" {
				continue
			}
			left := a.ChildByFieldName("left")
			typeNode := a.ChildByFieldName("type")
			if left == nil || left.Type() != "identifier" {
				continue
			}
			if parentClass == "" && typeNode == nil {
				// Module scope keeps its original contract: only ANNOTATED
				// globals are indexed (a plain `x = 5` at module scope is
				// config/constant noise at scale).
				continue
			}
			name := left.Content(src)
			raw := n.Content(src)
			sig := name
			if typeNode != nil {
				sig = name + ": " + typeNode.Content(src)
			} else if right := a.ChildByFieldName("right"); right != nil {
				sig = internalast.FirstLine(raw)
			}
			kind := astkit.KindVariable
			qn := name
			if parentClass != "" {
				kind = astkit.KindField
				qn = qualJoin(parentClass, name)
			}
			*out = append(*out, astkit.Symbol{
				Kind:          kind,
				Name:          name,
				QualifiedName: qn,
				Signature:     sig,
				Span:          internalast.NodeSpan(n),
				Exported:      !strings.HasPrefix(name, "_"),
				Body:          raw,
				ParentName:    qualLast(parentClass),
				Modifiers:     pythonModifiers(name),
			})
		}
	}
}

// pythonVisitBlocks recurses through the block/clause children of a compound
// statement (if/for/while/try/with/match), running pythonVisitDefinition on
// each contained statement so conditionally-defined classes, functions, and
// module-level annotated variables are still indexed. Condition and iterable
// expressions are skipped — only block-bearing children are descended.
func pythonVisitBlocks(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass, qualifier string, inFunction bool, out *[]astkit.Symbol) {
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch {
		case c.Type() == "block":
			for j := 0; j < int(c.ChildCount()); j++ {
				if inner := c.Child(j); inner != nil {
					pythonVisitDefinition(inner, filePath, blobSHA, src, imports, parentClass, qualifier, inFunction, nil, out)
				}
			}
		case strings.HasSuffix(c.Type(), "_clause"):
			// elif/else/except/finally/case clauses each wrap a block.
			pythonVisitBlocks(c, filePath, blobSHA, src, imports, parentClass, qualifier, inFunction, out)
		}
	}
}

func pythonTopLevelSymbol(root *sitter.Node, src []byte) *astkit.Symbol {
	calls := pythonCallSites(root, src)
	attrs := pythonAttrSites(root, src)
	if len(calls) == 0 && len(attrs) == 0 {
		return nil
	}
	masked := append([]byte(nil), src...)
	var maskDefinitions func(*sitter.Node)
	maskDefinitions = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n != root && pythonNestedScope(n.Type()) {
			start, end := int(n.StartByte()), int(n.EndByte())
			if start < 0 {
				start = 0
			}
			if end > len(masked) {
				end = len(masked)
			}
			for i := start; i < end; i++ {
				if masked[i] != '\n' && masked[i] != '\r' {
					masked[i] = ' '
				}
			}
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			maskDefinitions(n.Child(i))
		}
	}
	maskDefinitions(root)
	body := string(masked)
	return &astkit.Symbol{
		Kind: astkit.KindFunction, Name: "<top-level>", QualifiedName: "<top-level>",
		Signature: "<top-level>", Span: astkit.LineRange{Start: 1, End: strings.Count(body, "\n") + 1},
		Body: body, CallSites: calls, AttrSites: attrs,
	}
}

// ─── Java ─────────────────────────────────────────────────────────────────────

// qualJoin builds a dotted qualified path ("Outer.Inner" from "Outer" + "Inner");
// an empty prefix yields the bare name. qualLast returns the innermost segment
// of a dotted path ("Outer.Inner" → "Inner"), used as ParentName so grove's
// ParentSymbol matching (which keys on the immediate parent's simple name)
// keeps working while QualifiedName carries the full path — the two together
// disambiguate 2+level nested types that share (immediate-parent, own-name).
func qualJoin(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func qualLast(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func extractJavaNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	javaVisit(root, filePath, blobSHA, src, imports, "", &out)
	return out
}

func javaVisit(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		n := node.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "class_declaration", "record_declaration":
			javaTypeDecl(n, astkit.KindClass, filePath, blobSHA, src, imports, parentClass, out)
		case "interface_declaration":
			javaTypeDecl(n, astkit.KindInterface, filePath, blobSHA, src, imports, parentClass, out)
		case "enum_declaration":
			javaTypeDecl(n, astkit.KindEnum, filePath, blobSHA, src, imports, parentClass, out)
		case "annotation_type_declaration":
			javaTypeDecl(n, astkit.KindAnnotation, filePath, blobSHA, src, imports, parentClass, out)
		case "method_declaration":
			if parentClass == "" {
				continue
			}
			javaMethodDecl(n, astkit.KindMethod, filePath, blobSHA, src, imports, parentClass, out)
		case "constructor_declaration":
			if parentClass == "" {
				continue
			}
			javaMethodDecl(n, astkit.KindConstructor, filePath, blobSHA, src, imports, parentClass, out)
		case "field_declaration":
			if parentClass == "" {
				continue
			}
			javaFieldDecl(n, filePath, blobSHA, src, imports, parentClass, out)
		case "enum_body_declarations":
			javaVisit(n, filePath, blobSHA, src, imports, parentClass, out)
		case "enum_constant":
			if parentClass == "" {
				continue
			}
			javaEnumConstantDecl(n, src, parentClass, out)
			if body := n.ChildByFieldName("body"); body != nil {
				javaVisit(body, filePath, blobSHA, src, imports, parentClass, out)
			}
		}
	}
}

func javaTypeDecl(n *sitter.Node, kind astkit.SymbolKind, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	className := nameNode.Content(src)
	raw := n.Content(src)
	// Full header (not FirstLine): Java wraps extends/implements clauses onto
	// continuation lines, and a leading annotation is not a signature.
	sig := internalast.SignatureBeforeBody(n, src)
	modifiers := javaModifiers(n, src)
	exports := strings.Contains(sig, "public")
	for _, m := range modifiers {
		if m == "public" {
			exports = true
			break
		}
	}
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           className,
		QualifiedName:  qualJoin(parentClass, className),
		Signature:      sig,
		Span:           internalast.NodeSpan(n),
		Exported:       exports,
		Body:           raw,
		ParentName:     qualLast(parentClass),
		Modifiers:      modifiers,
		TypeParameters: javaTypeParameters(n, src),
		Annotations:    javaAnnotations(n, src),
	})
	body := n.ChildByFieldName("body")
	if n.Type() == "record_declaration" {
		javaRecordComponents(n, src, qualJoin(parentClass, className), out)
	}
	if body != nil {
		before := len(*out)
		javaVisit(body, filePath, blobSHA, src, imports, qualJoin(parentClass, className), out)
		synthesizeLombokAccessors(className, javaAnnotations(n, src), before, out)
	}
}

// javaRecordComponents emits the implicit field and public accessor that Java
// creates for each record header component. Neither declaration exists in the
// record body, but callers and field references must still have graph targets.
func javaRecordComponents(n *sitter.Node, src []byte, parentClass string, out *[]astkit.Symbol) {
	params := n.ChildByFieldName("parameters")
	if params == nil {
		params = findChildByType(n, "formal_parameters")
	}
	if params == nil {
		return
	}
	for i := 0; i < int(params.NamedChildCount()); i++ {
		component := params.NamedChild(i)
		if component == nil || component.Type() != "formal_parameter" {
			continue
		}
		nameNode := component.ChildByFieldName("name")
		typeNode := component.ChildByFieldName("type")
		if nameNode == nil || typeNode == nil {
			continue
		}
		name := nameNode.Content(src)
		typ := strings.TrimSpace(typeNode.Content(src))
		span := internalast.NodeSpan(component)
		qualified := qualJoin(parentClass, name)
		*out = append(*out,
			astkit.Symbol{
				Kind: astkit.KindField, Name: name, QualifiedName: qualified,
				Signature: component.Content(src), Span: span, ParentName: qualLast(parentClass),
				Modifiers: []string{"record-component"},
			},
			astkit.Symbol{
				Kind: astkit.KindMethod, Name: name, QualifiedName: qualified,
				Signature: typ + " " + name + "()", Span: span, ParentName: qualLast(parentClass),
				Exported: true, Modifiers: []string{"public", "record-accessor"},
			},
		)
	}
}

func javaEnumConstantDecl(n *sitter.Node, src []byte, parentClass string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindField, Name: name, QualifiedName: qualJoin(parentClass, name),
		Signature: n.Content(src), Span: internalast.NodeSpan(n), Exported: true,
		ParentName: qualLast(parentClass), Modifiers: []string{"public", "static", "final", "enum-constant"},
	})
}

// synthesizeLombokAccessors emits getter/setter method symbols for fields of
// a class annotated @Getter/@Setter/@Data/@Value (or fields carrying those
// annotations themselves). Without this, a Lombok entity's accessors exist
// only in bytecode: call sites reference getLoanId() but no such symbol is
// indexed, so the field is UNANCHORABLE for change-impact and every accessor
// caller dangles (measured on apache/fineract: entity fields with derived
// queries could not be queried at all). Synthesized symbols carry the
// "lombok-generated" modifier and the FIELD's span, so results point at the
// declaration a human would edit.
func synthesizeLombokAccessors(className string, classAnn []string, fieldsFrom int, out *[]astkit.Symbol) {
	has := func(ann []string, names ...string) bool {
		// javaAnnotations strips the leading @; match the bare name exactly
		// or with an argument list ("Getter", "Getter(...)").
		for _, a := range ann {
			for _, n := range names {
				if a == n || strings.HasPrefix(a, n+"(") {
					return true
				}
			}
		}
		return false
	}
	clsGetter := has(classAnn, "Getter", "Data", "Value")
	clsSetter := has(classAnn, "Setter", "Data")
	if !clsGetter && !clsSetter {
		// Field-level annotations may still apply; scan below regardless.
	}
	var synth []astkit.Symbol
	fields := (*out)[fieldsFrom:]
	explicitMethods := map[string]bool{}
	for i := range fields {
		if fields[i].Kind == astkit.KindMethod && fields[i].ParentName == className {
			explicitMethods[fields[i].Name] = true
		}
	}
	for i := range fields {
		f := &fields[i]
		if f.Kind != astkit.KindField || f.ParentName != className {
			continue
		}
		g := clsGetter || has(f.Annotations, "Getter", "Data", "Value")
		s := clsSetter || has(f.Annotations, "Setter", "Data")
		if !g && !s {
			continue
		}
		name := f.Name
		cap := strings.ToUpper(name[:1]) + name[1:]
		getter := "get" + cap
		if strings.Contains(f.Signature, "boolean ") {
			getter = "is" + cap
		}
		mk := func(mname, sig string) astkit.Symbol {
			return astkit.Symbol{
				Kind: astkit.KindMethod, Name: mname,
				QualifiedName: f.QualifiedName[:len(f.QualifiedName)-len(name)] + mname,
				ParentName:    className,
				Signature:     sig,
				Span:          f.Span,
				Exported:      true,
				Modifiers:     []string{"lombok-generated"},
			}
		}
		if g && !explicitMethods[getter] {
			synth = append(synth, mk(getter, "public "+getter+"() [lombok, from field "+name+"]"))
		}
		if setter := "set" + cap; s && !explicitMethods[setter] {
			synth = append(synth, mk(setter, "public "+setter+"(...) [lombok, from field "+name+"]"))
		}
	}
	*out = append(*out, synth...)
}

func javaMethodDecl(n *sitter.Node, kind astkit.SymbolKind, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	// Full header (not FirstLine): multi-line parameter lists must survive so
	// overloads can be discriminated by parameter types downstream.
	sig := internalast.SignatureBeforeBody(n, src)
	modifiers := javaModifiers(n, src)
	exports := strings.Contains(sig, "public")
	for _, m := range modifiers {
		if m == "public" {
			exports = true
		}
	}
	body := n.ChildByFieldName("body")
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  qualJoin(parentClass, name),
		Signature:      sig,
		Span:           internalast.NodeSpan(n),
		Exported:       exports,
		Body:           raw,
		ParentName:     qualLast(parentClass),
		Modifiers:      modifiers,
		TypeParameters: javaTypeParameters(n, src),
		Annotations:    javaAnnotations(n, src),
		CallSites:      javaCallSites(body, src),
	})
	javaNestedExecutables(body, filePath, blobSHA, src, imports, parentClass, out)
}

func javaNestedExecutables(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	if node == nil {
		return
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "object_creation_expression":
			if body := findChildByType(child, "class_body"); body != nil {
				javaAnonymousClassDecl(child, body, filePath, blobSHA, src, imports, parentClass, out)
				// Arguments can themselves contain lambdas/anonymous classes; the
				// anonymous class body is owned by the synthetic class above.
				for j := 0; j < int(child.NamedChildCount()); j++ {
					nested := child.NamedChild(j)
					if nested != nil && nested != body {
						javaNestedExecutables(nested, filePath, blobSHA, src, imports, parentClass, out)
					}
				}
				continue
			}
		case "lambda_expression":
			javaLambdaDecl(child, filePath, blobSHA, src, imports, parentClass, out)
			continue
		case "class_declaration", "record_declaration":
			javaTypeDecl(child, astkit.KindClass, filePath, blobSHA, src, imports, parentClass, out)
			continue
		case "interface_declaration":
			javaTypeDecl(child, astkit.KindInterface, filePath, blobSHA, src, imports, parentClass, out)
			continue
		case "enum_declaration":
			javaTypeDecl(child, astkit.KindEnum, filePath, blobSHA, src, imports, parentClass, out)
			continue
		}
		javaNestedExecutables(child, filePath, blobSHA, src, imports, parentClass, out)
	}
}

func javaSyntheticName(kind string, n *sitter.Node) string {
	p := n.StartPoint()
	return "<" + kind + "@" + strconv.Itoa(int(p.Row)+1) + ":" + strconv.Itoa(int(p.Column)+1) + ">"
}

func javaAnonymousClassDecl(n, body *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	name := javaSyntheticName("anonymous", n)
	qualified := qualJoin(parentClass, name)
	base := ""
	if typ := n.ChildByFieldName("type"); typ != nil {
		base = strings.TrimSpace(typ.Content(src))
	}
	signature := "class " + name
	if base != "" {
		signature += " extends " + base
	}
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindClass, Name: name, QualifiedName: qualified,
		ParentName: qualLast(parentClass), Signature: signature,
		Span: internalast.NodeSpan(n), Body: n.Content(src), Modifiers: []string{"anonymous"},
	})
	javaVisit(body, filePath, blobSHA, src, imports, qualified, out)
}

func javaLambdaDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	name := javaSyntheticName("lambda", n)
	body := n.ChildByFieldName("body")
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindFunction, Name: name, QualifiedName: qualJoin(parentClass, name),
		ParentName: qualLast(parentClass), Signature: strings.TrimSpace(n.Content(src)),
		Span: internalast.NodeSpan(n), Body: n.Content(src), Modifiers: []string{"lambda"},
		CallSites: javaCallSites(body, src),
	})
	javaNestedExecutables(body, filePath, blobSHA, src, imports, parentClass, out)
}

func javaFieldDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	raw := n.Content(src)
	// Full header, not FirstLine: an annotation on its own line before the
	// field made FirstLine return just "@Deprecated" as the signature.
	sig := internalast.SignatureBeforeBody(n, src)
	modifiers := javaModifiers(n, src)
	exports := strings.Contains(sig, "public")
	for _, m := range modifiers {
		if m == "public" {
			exports = true
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		decl := n.NamedChild(i)
		if decl == nil || decl.Type() != "variable_declarator" {
			continue
		}
		nameNode := decl.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		*out = append(*out, astkit.Symbol{
			Kind:          astkit.KindField,
			Name:          name,
			QualifiedName: qualJoin(parentClass, name),
			Signature:     sig,
			Span:          internalast.NodeSpan(n),
			Exported:      exports,
			Body:          raw,
			ParentName:    qualLast(parentClass),
			Modifiers:     modifiers,
			Annotations:   javaAnnotations(n, src),
		})
	}
}

// ─── Rust ─────────────────────────────────────────────────────────────────────

func extractRustNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	rustVisit(root, filePath, blobSHA, src, imports, "", nil, "", &out)
	impls := rustImplTraits(root, src)
	for i := range out {
		if out[i].Kind != astkit.KindStruct && out[i].Kind != astkit.KindEnum {
			continue
		}
		for _, trait := range impls[out[i].Name] {
			out[i].Annotations = append(out[i].Annotations, "implements:"+trait)
		}
	}
	return out
}

// rustImplTraits records impl Trait for Type headers on their declared type.
// This preserves empty impl blocks too; method annotations alone cannot carry
// an implementation relation when the impl intentionally defines no methods.
func rustImplTraits(root *sitter.Node, src []byte) map[string][]string {
	out := map[string][]string{}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "impl_item" {
			typeNode := n.ChildByFieldName("type")
			traitNode := n.ChildByFieldName("trait")
			if typeNode != nil && traitNode != nil {
				typ := rustImplBaseName(typeNode.Content(src))
				trait := rustImplBaseName(traitNode.Content(src))
				if typ != "" && trait != "" {
					out[typ] = append(out[typ], trait)
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return out
}

func rustImplBaseName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.IndexByte(name, '<'); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "::"); i >= 0 {
		name = name[i+2:]
	}
	return strings.TrimSpace(name)
}

func rustVisit(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, implBounds []string, implTrait string, out *[]astkit.Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		n := node.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "function_item":
			nameNode := n.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			name := nameNode.Content(src)
			raw := n.Content(src)
			kind := astkit.KindFunction
			if implType != "" {
				kind = astkit.KindMethod
				if name == "new" {
					kind = astkit.KindConstructor
				}
			}
			annotations := rustAttributes(n, src)
			if implTrait != "" {
				annotations = append(annotations, "impl_trait:"+implTrait)
			}
			body := n.ChildByFieldName("body")
			*out = append(*out, astkit.Symbol{
				Kind:           kind,
				Name:           name,
				QualifiedName:  name,
				Signature:      funcSig(n, src),
				Span:           internalast.NodeSpan(n),
				Exported:       strings.HasPrefix(strings.TrimSpace(raw), "pub"),
				Body:           raw,
				ParentName:     implType,
				Modifiers:      rustModifiers(n, src),
				TypeParameters: append(rustTypeParameters(n, src), implBounds...),
				Annotations:    annotations,
				CallSites:      rustCallSites(body, src),
			})
			if body != nil {
				scope := qualJoin(implType, name)
				start := len(*out)
				rustVisit(body, filePath, blobSHA, src, imports, "", nil, "", out)
				for j := start; j < len(*out); j++ {
					sym := &(*out)[j]
					sym.QualifiedName = qualJoin(scope, sym.QualifiedName)
					if sym.ParentName == "" {
						sym.ParentName = name
					}
				}
			}
		case "struct_item":
			rustNamedItem(n, astkit.KindStruct, filePath, blobSHA, src, imports, out)
			rustStructFields(n, filePath, blobSHA, src, imports, out)
		case "enum_item":
			rustNamedItem(n, astkit.KindEnum, filePath, blobSHA, src, imports, out)
		case "const_item", "static_item":
			// `const FLAGS: &[&dyn Flag] = &[...]`: the declared type is
			// what types `for flag in FLAGS.iter()` downstream.
			rustNamedItem(n, astkit.KindVariable, filePath, blobSHA, src, imports, out)
		case "trait_item":
			rustNamedItem(n, astkit.KindTrait, filePath, blobSHA, src, imports, out)
			// Trait bodies declare the methods dynamic dispatch goes
			// through — default-bodied methods and bare signatures both.
			if nameNode := n.ChildByFieldName("name"); nameNode != nil {
				if body := n.ChildByFieldName("body"); body != nil {
					rustVisit(body, filePath, blobSHA, src, imports, nameNode.Content(src), nil, "", out)
				}
			}
		case "function_signature_item":
			// Body-less trait method signature: a dispatch point, like an
			// abstract method.
			nameNode := n.ChildByFieldName("name")
			if nameNode == nil || implType == "" {
				continue
			}
			raw := n.Content(src)
			*out = append(*out, astkit.Symbol{
				Kind:           astkit.KindMethod,
				Name:           nameNode.Content(src),
				QualifiedName:  nameNode.Content(src),
				Signature:      funcSig(n, src),
				Span:           internalast.NodeSpan(n),
				Exported:       strings.HasPrefix(strings.TrimSpace(raw), "pub"),
				Body:           raw,
				ParentName:     implType,
				Modifiers:      rustModifiers(n, src),
				TypeParameters: append(rustTypeParameters(n, src), implBounds...),
				Annotations:    rustAttributes(n, src),
			})
		case "type_item":
			rustNamedItem(n, astkit.KindType, filePath, blobSHA, src, imports, out)
		case "impl_item":
			rustImplItem(n, filePath, blobSHA, src, imports, out)
		case "mod_item":
			// Inline modules (`mod tests { ... }`) nest arbitrary items;
			// unit tests live here by convention, so not descending hides
			// every #[cfg(test)] function in the file. A declaration
			// (`pub mod hiargs;`) becomes a module symbol so a lib.rs made
			// of mod lines still exists to the graph.
			rustNamedItem(n, astkit.KindModule, filePath, blobSHA, src, imports, out)
			if body := n.ChildByFieldName("body"); body != nil {
				nameNode := n.ChildByFieldName("name")
				if nameNode == nil {
					continue
				}
				moduleName := nameNode.Content(src)
				start := len(*out)
				rustVisit(body, filePath, blobSHA, src, imports, implType, implBounds, implTrait, out)
				for j := start; j < len(*out); j++ {
					sym := &(*out)[j]
					sym.QualifiedName = qualJoin(moduleName, sym.QualifiedName)
					if sym.ParentName == "" && (sym.Kind == astkit.KindFunction || sym.Kind == astkit.KindModule) {
						sym.ParentName = moduleName
					}
				}
			}
		}
	}
}

func rustNamedItem(n *sitter.Node, kind astkit.SymbolKind, filePath, blobSHA string, src []byte, imports []string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  name,
		Signature:      internalast.FirstLine(raw),
		Span:           internalast.NodeSpan(n),
		Exported:       strings.HasPrefix(strings.TrimSpace(raw), "pub"),
		Body:           raw,
		Modifiers:      rustModifiers(n, src),
		TypeParameters: rustTypeParameters(n, src),
		Annotations:    rustAttributes(n, src),
	})
}

// rustStructFields emits a Field symbol for each named field of a Rust struct.
func rustStructFields(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, out *[]astkit.Symbol) {
	structName := ""
	if nm := n.ChildByFieldName("name"); nm != nil {
		structName = nm.Content(src)
	}
	body := internalast.FindChildByType(n, "field_declaration_list")
	if body == nil {
		body = internalast.FindChildByType(n, "ordered_field_declaration_list")
	}
	if body == nil {
		return
	}
	if body.Type() == "ordered_field_declaration_list" {
		fieldIndex := 0
		public := false
		for i := 0; i < int(body.NamedChildCount()); i++ {
			child := body.NamedChild(i)
			if child == nil {
				continue
			}
			if child.Type() == "visibility_modifier" {
				public = true
				continue
			}
			name := strconv.Itoa(fieldIndex)
			mods := []string(nil)
			if public {
				mods = []string{"pub"}
			}
			*out = append(*out, astkit.Symbol{
				Kind: astkit.KindField, Name: name, QualifiedName: name,
				Signature: child.Content(src), Span: internalast.NodeSpan(child),
				Exported: public, Body: child.Content(src), ParentName: structName,
				Modifiers: mods,
			})
			fieldIndex++
			public = false
		}
		return
	}
	for i := 0; i < int(body.ChildCount()); i++ {
		fd := body.Child(i)
		if fd == nil || fd.Type() != "field_declaration" {
			continue
		}
		nameNode := fd.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		raw := fd.Content(src)
		*out = append(*out, astkit.Symbol{
			Kind:          astkit.KindField,
			Name:          name,
			QualifiedName: name,
			Signature:     internalast.FirstLine(raw),
			Span:          internalast.NodeSpan(fd),
			Exported:      strings.HasPrefix(strings.TrimSpace(raw), "pub"),
			Body:          raw,
			ParentName:    structName,
			Modifiers:     rustModifiers(fd, src),
			Annotations:   rustAttributes(fd, src),
		})
	}
}

func rustImplItem(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, out *[]astkit.Symbol) {
	typeNode := n.ChildByFieldName("type")
	if typeNode == nil {
		return
	}
	typeName := typeNode.Content(src)
	// Strip generic parameters: "Service<T>" → "Service"
	if idx := strings.IndexByte(typeName, '<'); idx >= 0 {
		typeName = typeName[:idx]
	}
	body := n.ChildByFieldName("body")
	if body == nil {
		return
	}
	// Impl-level generics carry the trait bounds the body's methods are
	// typed against (impl<M: Matcher, S: Sink> Core<M, S> where ...);
	// methods inherit them so consumers can resolve M::/m.-style calls.
	bounds := rustTypeParameters(n, src)
	bounds = append(bounds, rustWherePredicates(n, src)...)
	// impl Trait for Type: methods remember the trait so consumers can
	// route default-trait-method calls (self.x() with no own x) to the
	// trait's declaration.
	implTrait := ""
	if tr := n.ChildByFieldName("trait"); tr != nil {
		implTrait = tr.Content(src)
		if idx := strings.IndexByte(implTrait, '<'); idx >= 0 {
			implTrait = implTrait[:idx]
		}
		if idx := strings.LastIndex(implTrait, "::"); idx >= 0 {
			implTrait = implTrait[idx+2:]
		}
	}
	rustVisit(body, filePath, blobSHA, src, imports, typeName, bounds, implTrait, out)
}

// rustWherePredicates extracts "T: Bound" predicates from an impl's where
// clause, complementing inline type-parameter bounds.
func rustWherePredicates(n *sitter.Node, src []byte) []string {
	wc := internalast.FindChildByType(n, "where_clause")
	if wc == nil {
		return nil
	}
	text := strings.TrimSpace(wc.Content(src))
	text = strings.TrimPrefix(text, "where")
	var out []string
	depth := 0
	last := 0
	flush := func(end int) {
		if p := strings.TrimSpace(text[last:end]); p != "" {
			out = append(out, p)
		}
	}
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '<', '(', '[':
			depth++
		case '>', ')', ']':
			depth--
		case ',':
			if depth == 0 {
				flush(i)
				last = i + 1
			}
		}
	}
	flush(len(text))
	return out
}

// ─── C / C++ ─────────────────────────────────────────────────────────────────
//
// C and C++ share the same extractor — the C++ grammar is a superset of C, and
// both use identical node types for the constructs we care about (functions,
// structs, enums, classes in C++).

// cMacroKeywords are call-looking tokens in a macro body that are not calls.
var cMacroKeywords = map[string]bool{
	"if": true, "while": true, "for": true, "switch": true, "return": true, "sizeof": true,
	"defined": true, "__attribute__": true, "__typeof__": true, "typeof": true, "do": true,
	"else": true, "case": true, "alignof": true, "_Alignof": true, "__extension__": true,
	"static_assert": true, "_Static_assert": true, "__builtin_expect": true,
}

var cMacroCallRe = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`)

// cMacroSym emits a `#define` as a KindMacro symbol. A function-like
// macro's Signature is "#define NAME(a, b)" (its parameter names, which a
// consumer substitutes with the invocation's arguments); its CallSites are
// the calls its body text makes, so a caller that invokes the macro can be
// credited with them — the C preprocessor is what makes
// `json_object_foreach(o, k, v)` call json_object_iter and `RUN_TEST(f)`
// call UnityDefaultTestRun(f, ..). Tree-sitter keeps the body as opaque
// preproc_arg text, so calls are found lexically.
func cMacroSym(n *sitter.Node, filePath, blobSHA, language string, src []byte) *astkit.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	params := ""
	var paramNames []string
	if p := n.ChildByFieldName("parameters"); p != nil {
		params = strings.Join(strings.Fields(p.Content(src)), "")
		for _, x := range strings.Split(strings.Trim(params, "()"), ",") {
			if x = strings.TrimSpace(x); x != "" {
				paramNames = append(paramNames, x)
			}
		}
	}
	body := ""
	if v := n.ChildByFieldName("value"); v != nil {
		body = v.Content(src)
	}
	isParam := map[string]bool{}
	for _, p := range paramNames {
		isParam[p] = true
	}
	var sites []astkit.CallSite
	for _, m := range cMacroCallRe.FindAllStringSubmatchIndex(body, -1) {
		callee := body[m[2]:m[3]]
		if cMacroKeywords[callee] {
			continue
		}
		open := m[1] - 1 // index of '('
		args, argc := cMacroArgs(body, open)
		sites = append(sites, astkit.CallSite{
			Callee: callee, Line: int(n.StartPoint().Row) + 1, Argc: argc, Args: args,
		})
	}
	sig := "#define " + name + params
	return &astkit.Symbol{
		Kind: astkit.KindMacro, Name: name, QualifiedName: name, Signature: sig,
		Span: internalast.NodeSpan(n), Exported: true, Body: n.Content(src),
		CallSites: sites,
	}
}

// cMacroArgs tokenizes the argument list opening at body[open]: an
// identifier stays (a macro parameter name is substituted downstream, a
// function name becomes a reference), a string or stringized `#param`
// argument is "#String", a number "#int", anything else "".
func cMacroArgs(body string, open int) ([]string, int) {
	depth := 0
	start := open + 1
	var raw []string
	for i := open; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				raw = append(raw, body[start:i])
				i = len(body)
			}
		case ',':
			if depth == 1 {
				raw = append(raw, body[start:i])
				start = i + 1
			}
		}
	}
	if len(raw) == 1 && strings.TrimSpace(raw[0]) == "" {
		return nil, 0
	}
	out := make([]string, len(raw))
	for i, a := range raw {
		a = strings.TrimSpace(a)
		switch {
		case a == "":
		case strings.HasPrefix(a, "#") || strings.HasPrefix(a, "\""):
			out[i] = "#String"
		case a[0] >= '0' && a[0] <= '9':
			out[i] = "#int"
		default:
			if ok, _ := regexp.MatchString(`^[A-Za-z_]\w*$`, a); ok {
				out[i] = a
			}
		}
	}
	return out, len(raw)
}

func extractCNodes(root *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	defined := map[string]bool{} // function names with a body in this file
	prototype := map[int]bool{}  // out indices that came from declarations
	for i := 0; i < int(root.ChildCount()); i++ {
		n := root.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "function_definition":
			if sym := cFuncSym(n, filePath, blobSHA, language, src, imports, ""); sym != nil {
				out = append(out, *sym)
				defined[sym.Name] = true
			}
		case "declaration":
			// Catches typedef struct, extern function declarations, etc.
			for _, sym := range cDeclarationSyms(n, filePath, blobSHA, language, src, imports) {
				if sym.Kind == astkit.KindFunction {
					prototype[len(out)] = true
				}
				out = append(out, sym)
			}
		case "struct_specifier":
			if language == "cpp" {
				out = append(out, cppClassSym(n, filePath, blobSHA, language, src, imports)...)
			} else if sym := cTaggedTypeSym(n, astkit.KindStruct, filePath, blobSHA, language, src, imports); sym != nil {
				out = append(out, *sym)
			}
		case "union_specifier":
			if sym := cTaggedTypeSym(n, astkit.KindStruct, filePath, blobSHA, language, src, imports); sym != nil {
				out = append(out, *sym)
			}
		case "enum_specifier":
			if sym := cTaggedTypeSym(n, astkit.KindEnum, filePath, blobSHA, language, src, imports); sym != nil {
				out = append(out, *sym)
			}
		case "type_definition":
			out = append(out, cTypedefSyms(n, filePath, blobSHA, language, src, imports)...)
		// C++ only
		case "class_specifier":
			out = append(out, cppClassSym(n, filePath, blobSHA, language, src, imports)...)
		case "namespace_definition":
			if sym := cppNamespaceSym(n, filePath, blobSHA, language, src, imports); sym != nil {
				out = append(out, *sym)
			}
			// Recurse into namespace body to find enclosed classes, functions, etc.
			if body := n.ChildByFieldName("body"); body != nil {
				out = append(out, extractCNodes(body, filePath, blobSHA, language, src, imports)...)
			}
		case "template_declaration":
			out = append(out, cppTemplateDecl(n, filePath, blobSHA, language, src, imports)...)
		case "preproc_if", "preproc_ifdef", "preproc_elif", "preproc_else":
			// Declarations in conditional branches are ordinary source symbols.
			// Tree-sitter nests them below the preprocessor node rather than the
			// translation unit, so explicitly descend into each guarded region.
			out = append(out, extractCNodes(n, filePath, blobSHA, language, src, imports)...)
		case "preproc_function_def", "preproc_def":
			if sym := cMacroSym(n, filePath, blobSHA, language, src); sym != nil {
				out = append(out, *sym)
			}
		case "linkage_specification":
			// `extern "C" { ... }` — in a C header it sits under `#ifdef
			// __cplusplus`, and the C grammar hands the whole rest of the
			// header to its declaration_list (the closing brace is under
			// another #ifdef). Everything Unity's unity.h and jansson.h
			// declare lives inside one; not descending hid all of it.
			if body := n.ChildByFieldName("body"); body != nil {
				out = append(out, extractCNodes(body, filePath, blobSHA, language, src, imports)...)
			}
		}
	}
	// A prototype (`static int do_dump(...);`) for a function defined in
	// the same file is the same function, not a second one: as its own
	// 1-line symbol it split the function's identity (two do_dump
	// declarations in change-impact) and, in span-based matching, claimed
	// the definition's line so every call from the real body went missing.
	if len(prototype) > 0 {
		kept := out[:0]
		for i, sym := range out {
			if prototype[i] && defined[sym.Name] {
				continue
			}
			kept = append(kept, sym)
		}
		out = kept
	}
	if language == "cpp" {
		cppApplyDeclaredVisibility(out)
	}

	return out
}

func cFuncSym(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string, parentClass string) *astkit.Symbol {
	// function_definition: type declarator body
	declarator := n.ChildByFieldName("declarator")
	if declarator == nil {
		return nil
	}
	name := cDeclaratorName(declarator, src)
	if name == "" && declarator.Type() == "parenthesized_declarator" {
		// `int CJSON_CDECL main(void) {…}`: an unexpanded calling-convention
		// macro makes the grammar read `int CJSON_CDECL` as a declaration
		// and `main(void)` as a definition whose type is `main` and whose
		// declarator is the bare parameter list. The type is the name.
		if t := n.ChildByFieldName("type"); t != nil && t.Type() == "type_identifier" {
			name = t.Content(src)
		}
	}
	if name == "" {
		return nil
	}
	if language == "cpp" && parentClass == "" {
		parentClass = cppDeclaratorOwner(declarator.Content(src))
	}
	raw := n.Content(src)
	modifiers := cStorageModifiers(n, src)
	kind := astkit.KindFunction
	qualifiedName := name
	if parentClass != "" {
		kind = astkit.KindMethod
		ownerName := parentClass
		if i := strings.LastIndex(ownerName, "::"); i >= 0 {
			ownerName = ownerName[i+2:]
		}
		if language == "cpp" && name == ownerName {
			kind = astkit.KindConstructor
		}
		qualifiedName = parentClass + "." + name
	}
	return &astkit.Symbol{
		Kind:          kind,
		Name:          name,
		QualifiedName: qualifiedName,
		Signature:     funcSig(n, src),
		Span:          internalast.NodeSpan(n),
		Exported:      !strings.HasPrefix(name, "_") && !cHasModifier(modifiers, "static"),
		Body:          raw,
		ParentName:    parentClass,
		Modifiers:     modifiers,
		CallSites:     cCallSites(n, src),
	}
}

func cStorageModifiers(n *sitter.Node, src []byte) []string {
	var modifiers []string
	for i := 0; n != nil && i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child == nil || child.Type() != "storage_class_specifier" {
			continue
		}
		modifier := strings.TrimSpace(child.Content(src))
		if modifier != "" && !cHasModifier(modifiers, modifier) {
			modifiers = append(modifiers, modifier)
		}
	}
	return modifiers
}

func cHasModifier(modifiers []string, want string) bool {
	for _, modifier := range modifiers {
		if modifier == want {
			return true
		}
	}
	return false
}

func cppDeclaratorOwner(declarator string) string {
	head := declarator
	if i := strings.IndexByte(head, '('); i >= 0 {
		head = head[:i]
	}
	sep := strings.LastIndex(head, "::")
	if sep < 0 {
		return ""
	}
	owner := strings.TrimSpace(head[:sep])
	var out strings.Builder
	depth := 0
	for _, r := range owner {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				out.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(out.String())
}

// cCallSites extracts call sites from a C/C++ function/method definition:
// plain calls foo(), member calls obj.m()/obj->m(), scoped calls Foo::m(),
// and C++ object construction (new Foo()). Member receivers keep their last
// segment as a qualifier (repo->save → "repo.save", this->run → "this.run")
// so the graph layer can narrow by the receiver's inferred type.
func cCallSites(decl *sitter.Node, src []byte) []astkit.CallSite {
	return collectCallSites(decl, src, callSpec{
		nodeTypes: []string{"call_expression", "new_expression"},
		calleeFn: func(call *sitter.Node, src []byte) string {
			if call.Type() == "new_expression" {
				if t := call.ChildByFieldName("type"); t != nil {
					return cTypeLastName(t, src)
				}
				return ""
			}
			fn := call.ChildByFieldName("function")
			if fn == nil {
				return ""
			}
			return cCalleeFromExpr(fn, src)
		},
	})
}

func cCalleeFromExpr(fn *sitter.Node, src []byte) string {
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "field_expression":
		// obj.method / obj->method / a.b.method
		field := fn.ChildByFieldName("field")
		if field == nil {
			return ""
		}
		qual := cReceiverName(fn.ChildByFieldName("argument"), src)
		return joinQualified(qual, field.Content(src))
	case "qualified_identifier":
		// Ns::Class::method → qualifier "Class", name "method"
		name := fn.ChildByFieldName("name")
		if name == nil {
			return ""
		}
		if name.Type() == "qualified_identifier" {
			return cCalleeFromExpr(name, src)
		}
		qual := ""
		if scope := fn.ChildByFieldName("scope"); scope != nil {
			qual = cTypeLastName(scope, src)
		}
		return joinQualified(qual, cTypeLastName(name, src))
	case "template_function":
		// foo<T>(...) — the name child holds the identifier.
		if name := fn.ChildByFieldName("name"); name != nil {
			return cCalleeFromExpr(name, src)
		}
	case "parenthesized_expression":
		for i := 0; i < int(fn.ChildCount()); i++ {
			if c := fn.Child(i); c != nil && c.IsNamed() {
				return cCalleeFromExpr(c, src)
			}
		}
	}
	return ""
}

// cReceiverName reduces a member-call receiver to a bare qualifier:
// identifier → itself, this → "this", a->b / a.b → "b", call() → "call()".
func cReceiverName(obj *sitter.Node, src []byte) string {
	if obj == nil {
		return ""
	}
	switch obj.Type() {
	case "identifier":
		return obj.Content(src)
	case "this":
		return "this"
	case "field_expression":
		if f := obj.ChildByFieldName("field"); f != nil {
			return f.Content(src)
		}
	case "call_expression":
		if f := obj.ChildByFieldName("function"); f != nil {
			if n := cCalleeFromExpr(f, src); n != "" {
				return cLastSegment(n) + "()"
			}
		}
	}
	return ""
}

// cTypeLastName reduces a type/name node to its final identifier:
// Foo → "Foo", Ns::Foo → "Foo", Foo<T> → "Foo".
func cTypeLastName(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	switch n.Type() {
	case "type_identifier", "identifier", "field_identifier", "primitive_type":
		return n.Content(src)
	case "qualified_identifier", "template_type", "template_function", "scoped_type_identifier", "scoped_identifier":
		if name := n.ChildByFieldName("name"); name != nil {
			return cTypeLastName(name, src)
		}
	}
	return cLastSegment(strings.TrimSpace(n.Content(src)))
}

func cLastSegment(s string) string {
	if i := strings.IndexAny(s, "<("); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "::"); i >= 0 {
		s = s[i+2:]
	}
	return strings.TrimSpace(s)
}

// cDeclaratorName walks nested declarator nodes (pointer_declarator,
// function_declarator, etc.) to extract the final identifier name.
func cDeclaratorName(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	switch n.Type() {
	case "identifier", "field_identifier":
		return n.Content(src)
	case "pointer_declarator", "function_declarator",
		"abstract_pointer_declarator", "qualified_identifier":
		for i := 0; i < int(n.ChildCount()); i++ {
			if name := cDeclaratorName(n.Child(i), src); name != "" {
				return name
			}
		}
	case "destructor_name": // C++ ~ClassName
		return n.Content(src)
	case "operator_name": // C++ operator overload
		return n.Content(src)
	}
	return ""
}

func cDeclarationSyms(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string) []astkit.Symbol {
	// Pick up top-level variable/function declarations (e.g. extern declarations).
	var out []astkit.Symbol
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "function_declarator" {
			name := cDeclaratorName(child, src)
			if name == "" {
				continue
			}
			raw := n.Content(src)
			modifiers := cStorageModifiers(n, src)
			out = append(out, astkit.Symbol{
				Kind:          astkit.KindFunction,
				Name:          name,
				QualifiedName: name,
				Signature:     strings.TrimSpace(raw),
				Span:          internalast.NodeSpan(n),
				Exported:      !strings.HasPrefix(name, "_") && !cHasModifier(modifiers, "static"),
				Body:          raw,
				Modifiers:     modifiers,
				Annotations:   []string{"declaration"},
			})
		}
	}
	return out
}

// cTypedefSyms handles `typedef struct {...} Name` and similar typedef forms.
// The alias name lives in the child with field "declarator".
func cTypedefSyms(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string) []astkit.Symbol {
	declaratorNode := n.ChildByFieldName("declarator")
	if declaratorNode == nil {
		return nil
	}
	name := ""
	switch declaratorNode.Type() {
	case "type_identifier":
		name = declaratorNode.Content(src)
	default:
		// Pointer typedef: typedef struct {...} *PName — find type_identifier child.
		for i := 0; i < int(declaratorNode.ChildCount()); i++ {
			if c := declaratorNode.Child(i); c != nil && c.Type() == "type_identifier" {
				name = c.Content(src)
				break
			}
		}
	}
	if name == "" {
		return nil
	}
	kind := astkit.KindType
	if typeNode := n.ChildByFieldName("type"); typeNode != nil {
		switch typeNode.Type() {
		case "struct_specifier", "union_specifier":
			kind = astkit.KindStruct
		case "enum_specifier":
			kind = astkit.KindEnum
		}
	}
	raw := n.Content(src)
	return []astkit.Symbol{{
		Kind:          kind,
		Name:          name,
		QualifiedName: name,
		Signature:     internalast.FirstLine(raw),
		Span:          internalast.NodeSpan(n),
		Exported:      true,
		Body:          raw,
	}}
}

func cTaggedTypeSym(n *sitter.Node, kind astkit.SymbolKind, filePath, blobSHA, language string, src []byte, imports []string) *astkit.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	return &astkit.Symbol{
		Kind:          kind,
		Name:          name,
		QualifiedName: name,
		Signature:     internalast.FirstLine(raw),
		Span:          internalast.NodeSpan(n),
		Exported:      true,
		Body:          raw,
	}
}

func cppClassSym(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string) []astkit.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	className := nameNode.Content(src)
	raw := n.Content(src)
	kind := astkit.KindClass
	if n.Type() == "struct_specifier" {
		kind = astkit.KindStruct
	}
	out := []astkit.Symbol{{
		Kind:          kind,
		Name:          className,
		QualifiedName: className,
		Signature:     internalast.SignatureBeforeBody(n, src),
		Span:          internalast.NodeSpan(n),
		Exported:      true,
		Body:          raw,
	}}
	// Extract member functions from the class body.
	body := n.ChildByFieldName("body")
	if body == nil {
		return out
	}
	access := "private"
	if kind == astkit.KindStruct {
		access = "public"
	}
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "access_specifier" {
			if declared := strings.TrimSuffix(strings.TrimSpace(child.Content(src)), ":"); declared == "public" || declared == "private" || declared == "protected" {
				access = declared
			}
			continue
		}
		if child.Type() == "function_definition" {
			if sym := cFuncSym(child, filePath, blobSHA, language, src, imports, className); sym != nil {
				cppSetMemberAccess(sym, access)
				out = append(out, *sym)
			}
			continue
		}
		if child.Type() == "field_declaration" || child.Type() == "declaration" {
			for _, sym := range cDeclarationSyms(child, filePath, blobSHA, language, src, imports) {
				if sym.Kind != astkit.KindFunction {
					continue
				}
				sym.Kind = astkit.KindMethod
				if sym.Name == className {
					sym.Kind = astkit.KindConstructor
				}
				sym.ParentName = className
				sym.QualifiedName = className + "." + sym.Name
				cppSetMemberAccess(&sym, access)
				out = append(out, sym)
			}
		}
	}
	return out
}

func cppSetMemberAccess(sym *astkit.Symbol, access string) {
	sym.Exported = access == "public"
	for _, modifier := range sym.Modifiers {
		if modifier == "public" || modifier == "private" || modifier == "protected" {
			return
		}
	}
	sym.Modifiers = append(sym.Modifiers, access)
}

func cppApplyDeclaredVisibility(symbols []astkit.Symbol) {
	type visibility struct {
		exported  bool
		modifiers []string
	}
	declared := map[string]visibility{}
	for i := range symbols {
		if symbols[i].Kind != astkit.KindMethod {
			continue
		}
		for _, modifier := range symbols[i].Modifiers {
			if modifier == "public" || modifier == "private" || modifier == "protected" {
				declared[symbols[i].QualifiedName] = visibility{symbols[i].Exported, append([]string(nil), symbols[i].Modifiers...)}
				break
			}
		}
	}
	for i := range symbols {
		visibility, ok := declared[symbols[i].QualifiedName]
		if !ok {
			continue
		}
		symbols[i].Exported = visibility.exported
		symbols[i].Modifiers = append([]string(nil), visibility.modifiers...)
	}
}

func cppNamespaceSym(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string) *astkit.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	return &astkit.Symbol{
		Kind:          astkit.KindNamespace,
		Name:          name,
		QualifiedName: name,
		Signature:     internalast.FirstLine(raw),
		Span:          internalast.NodeSpan(n),
		Exported:      true,
		Body:          raw,
	}
}

func cppTemplateDecl(n *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string) []astkit.Symbol {
	// template<...> function_definition | class_specifier
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "function_definition":
			if sym := cFuncSym(child, filePath, blobSHA, language, src, imports, ""); sym != nil {
				return []astkit.Symbol{*sym}
			}
		case "class_specifier", "struct_specifier":
			return cppClassSym(child, filePath, blobSHA, language, src, imports)
		}
	}
	return nil
}

// ─── C# ───────────────────────────────────────────────────────────────────────

func extractCSharpNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	csVisit(root, filePath, blobSHA, src, imports, "", false, &out)
	if top := csTopLevelSymbol(root, src); top != nil {
		out = append(out, *top)
	}
	return out
}

func csTopLevelSymbol(root *sitter.Node, src []byte) *astkit.Symbol {
	var sites []astkit.CallSite
	start, end := 0, 0
	for i := 0; i < int(root.ChildCount()); i++ {
		n := root.Child(i)
		if n == nil || n.Type() != "global_statement" || internalast.FindChildByType(n, "local_function_statement") != nil {
			continue
		}
		if start == 0 {
			start = int(n.StartPoint().Row) + 1
		}
		end = int(n.EndPoint().Row) + 1
		sites = append(sites, csCallSites(n, src)...)
	}
	if start == 0 {
		return nil
	}
	return &astkit.Symbol{
		Kind: astkit.KindFunction, Name: "<top-level>", QualifiedName: "<top-level>",
		Signature: "<top-level>", Span: astkit.LineRange{Start: start, End: end},
		Body: string(src), CallSites: sites,
	}
}

func csVisit(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, parentInterface bool, out *[]astkit.Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		n := node.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "namespace_declaration":
			nameNode := n.ChildByFieldName("name")
			if nameNode == nil {
				break
			}
			nsName := nameNode.Content(src)
			raw := n.Content(src)
			*out = append(*out, astkit.Symbol{
				Kind:          astkit.KindNamespace,
				Name:          nsName,
				QualifiedName: nsName,
				Signature:     internalast.FirstLine(raw),
				Span:          internalast.NodeSpan(n),
				Exported:      true,
				Body:          raw,
			})
			if body := n.ChildByFieldName("body"); body != nil {
				csVisit(body, filePath, blobSHA, src, imports, "", false, out)
			}
		case "class_declaration":
			csTypeDecl(n, astkit.KindClass, filePath, blobSHA, src, imports, parentClass, out)
		case "record_declaration":
			kind := astkit.KindClass
			if strings.Contains(internalast.SignatureBeforeBody(n, src), "record struct") {
				kind = astkit.KindStruct
			}
			csTypeDecl(n, kind, filePath, blobSHA, src, imports, parentClass, out)
		case "struct_declaration":
			csTypeDecl(n, astkit.KindStruct, filePath, blobSHA, src, imports, parentClass, out)
		case "interface_declaration":
			csTypeDecl(n, astkit.KindInterface, filePath, blobSHA, src, imports, parentClass, out)
		case "enum_declaration":
			csTypeDecl(n, astkit.KindEnum, filePath, blobSHA, src, imports, parentClass, out)
		case "method_declaration", "constructor_declaration", "destructor_declaration":
			csMethodDecl(n, filePath, blobSHA, src, imports, parentClass, out)
		case "global_statement":
			csVisit(n, filePath, blobSHA, src, imports, parentClass, parentInterface, out)
		case "local_function_statement":
			csMethodDecl(n, filePath, blobSHA, src, imports, parentClass, out)
		case "property_declaration":
			csPropertyDecl(n, filePath, blobSHA, src, imports, parentClass, parentInterface, out)
		case "indexer_declaration":
			csIndexerDecl(n, filePath, blobSHA, src, imports, parentClass, out)
		case "field_declaration":
			csFieldDecl(n, filePath, blobSHA, src, imports, parentClass, out)
		case "file_scoped_namespace_declaration":
			// `namespace Foo;` (C# 10): declarations follow as siblings in
			// the same node, sharing this namespace.
			csVisit(n, filePath, blobSHA, src, imports, parentClass, parentInterface, out)
		case "preproc_if", "preproc_elif", "preproc_else":
			// Multi-target code lives inside #if/#elif/#else blocks; the
			// grammar nests the conditional declarations as children. Not
			// descending hides whole files (Newtonsoft wraps every file in
			// `#if !(PORTABLE || ...)`) — the C# analog of Rust's mod_item.
			// All branches are visited so a symbol guarded by any target is
			// found; the oracle's chosen branch is always a subset.
			csVisit(n, filePath, blobSHA, src, imports, parentClass, parentInterface, out)
		}
	}
}

func csTypeDecl(n *sitter.Node, kind astkit.SymbolKind, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	modifiers := csModifiers(n, src)
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  qualJoin(parentClass, name),
		Signature:      internalast.SignatureBeforeBody(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       csIsExported(modifiers),
		Body:           raw,
		ParentName:     qualLast(parentClass),
		Modifiers:      modifiers,
		TypeParameters: csTypeParams(n, src),
		Annotations:    csAttributes(n, src),
	})
	if body := n.ChildByFieldName("body"); body != nil {
		csVisit(body, filePath, blobSHA, src, imports, qualJoin(parentClass, name), kind == astkit.KindInterface, out)
	}
}

func csMethodDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	modifiers := csModifiers(n, src)
	kind := astkit.KindMethod
	if parentClass == "" {
		kind = astkit.KindFunction
	} else if n.Type() == "constructor_declaration" {
		kind = astkit.KindConstructor
	}
	sites := csCallSites(n, src)
	if n.Type() == "constructor_declaration" {
		sites = append(sites, csConstructorInitializerSites(n, src, parentClass)...)
	}
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  qualJoin(parentClass, name),
		Signature:      funcSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       csIsExported(modifiers),
		Body:           raw,
		ParentName:     qualLast(parentClass),
		Modifiers:      modifiers,
		TypeParameters: csTypeParams(n, src),
		Annotations:    csAttributes(n, src),
		CallSites:      sites,
	})
}

func csConstructorInitializerSites(n *sitter.Node, src []byte, parentClass string) []astkit.CallSite {
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child == nil || child.Type() != "constructor_initializer" {
			continue
		}
		raw := strings.TrimSpace(child.Content(src))
		callee := ""
		switch {
		case strings.HasPrefix(raw, ": base"):
			callee = "super()"
		case strings.HasPrefix(raw, ": this"):
			callee = qualLast(parentClass)
		}
		if callee == "" {
			return nil
		}
		argc := 0
		if args := internalast.FindChildByType(child, "argument_list"); args != nil {
			argc = int(args.NamedChildCount())
		}
		return []astkit.CallSite{{Callee: callee, Line: int(child.StartPoint().Row) + 1, Argc: argc}}
	}
	return nil
}

// csCallSites extracts invocation and object-creation call sites from a C#
// method/constructor declaration. Member-access calls keep their receiver's
// last segment as a qualifier (repo.Save → "repo.Save"); object creation
// resolves to the constructed type's name (new Repo() → "Repo", matching the
// constructor symbol Grove records). The whole declaration is walked so
// expression-bodied members (=> Expr) and lambda bodies are covered, with
// lambda calls attributed to the enclosing method as Grove expects.
// csCallIsGeneric reports whether a C# call supplies explicit type arguments,
// i.e. the invoked method name (or constructed type) is a generic_name:
// DeserializeObject<T>(...) or new List<T>(). The grammar drops the <...> when
// the callee reduces to a bare name, so this is the only surviving signal that
// splits a generic overload from its same-arity non-generic sibling.
func csCallIsGeneric(call *sitter.Node, src []byte) bool {
	if call.Type() == "object_creation_expression" {
		t := call.ChildByFieldName("type")
		return t != nil && t.Type() == "generic_name"
	}
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return false
	}
	switch fn.Type() {
	case "generic_name":
		return true
	case "member_access_expression":
		name := fn.ChildByFieldName("name")
		return name != nil && name.Type() == "generic_name"
	}
	return false
}

func csCallSites(decl *sitter.Node, src []byte) []astkit.CallSite {
	return collectCallSites(decl, src, callSpec{
		nodeTypes: []string{"invocation_expression", "object_creation_expression"},
		genericFn: csCallIsGeneric,
		calleeFn: func(call *sitter.Node, src []byte) string {
			if call.Type() == "object_creation_expression" {
				return csTypeLastName(call.ChildByFieldName("type"), src)
			}
			fn := call.ChildByFieldName("function")
			if fn == nil {
				return ""
			}
			switch fn.Type() {
			case "identifier":
				return fn.Content(src)
			case "generic_name":
				return csGenericBaseName(fn, src)
			case "member_access_expression":
				nameNode := fn.ChildByFieldName("name")
				if nameNode == nil {
					return ""
				}
				method := csNameToken(nameNode, src)
				if method == "" {
					return ""
				}
				recv := fn.ChildByFieldName("expression")
				qual := qualifierName(recv, src,
					// tree-sitter-c-sharp names the receiver keywords plainly
					// ("this", "base"); the *_expression spellings matched
					// nothing, so base.WriteValue(v) arrived as a bare
					// WriteValue and bound the caller's own overload family.
					// predefined_type: `string.Join(...)` is a static call on
					// the runtime's String, never a bare Join.
					[]string{"identifier", "this", "base", "this_expression", "base_expression", "predefined_type"},
					map[string]string{"member_access_expression": "name"},
					map[string]string{
						"invocation_expression":      "function",
						"object_creation_expression": "type",
					})
				if qual == "" {
					qual = csTypedReceiver(recv, src)
				}
				return joinQualified(qual, method)
			}
			return ""
		},
	})
}

// csTypedReceiver names the static type of a receiver qualifierName cannot
// reduce to an identifier, so the call does not arrive bare (a bare C# call
// means implicit `this`): a string literal or interpolation is a `string`
// (`"{0}".FormatWith(..)` binds the string extension), a cast names the
// cast type (`((ICollection<JToken>)a).CopyTo(..)`). Returns "" otherwise.
func csTypedReceiver(recv *sitter.Node, src []byte) string {
	if recv == nil {
		return ""
	}
	switch recv.Type() {
	case "string_literal", "verbatim_string_literal", "interpolated_string_expression", "raw_string_literal":
		return "string"
	case "character_literal":
		return "char"
	case "boolean_literal":
		return "bool"
	case "parenthesized_expression":
		for i := 0; i < int(recv.NamedChildCount()); i++ {
			inner := recv.NamedChild(i)
			if inner != nil && inner.Type() == "cast_expression" {
				return csTypeLastName(inner.ChildByFieldName("type"), src)
			}
		}
	case "element_access_expression":
		// `o["x"].Children()` / `a[0].Replace(..)`: the receiver is an
		// element of `o` — written "o[]", which Grove types by o's
		// indexer or array element type.
		base := recv.ChildByFieldName("expression")
		if base == nil && recv.NamedChildCount() > 0 {
			base = recv.NamedChild(0)
		}
		if base != nil {
			switch base.Type() {
			case "identifier", "this":
				return base.Content(src) + "[]"
			case "member_access_expression":
				if name := base.ChildByFieldName("name"); name != nil {
					return csNameToken(name, src) + "[]"
				}
			case "element_access_expression":
				// rss["channel"]["item"].Children(): one "[]" per level.
				if inner := csTypedReceiver(base, src); inner != "" {
					return inner + "[]"
				}
			}
		}
	}
	return ""
}

// csNameToken reduces a member-access name (identifier or generic_name) to
// its bare identifier.
func csNameToken(n *sitter.Node, src []byte) string {
	if n.Type() == "generic_name" {
		return csGenericBaseName(n, src)
	}
	return n.Content(src)
}

// csGenericBaseName returns the identifier of a generic_name (Method<T> → Method).
func csGenericBaseName(n *sitter.Node, src []byte) string {
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c != nil && c.Type() == "identifier" {
			return c.Content(src)
		}
	}
	return ""
}

// csTypeLastName reduces a C# type node to its final identifier:
// Repo → "Repo", Foo.Bar.Repo → "Repo", List<T> → "List", Repo? → "Repo".
func csTypeLastName(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	switch n.Type() {
	case "identifier":
		return n.Content(src)
	case "generic_name":
		return csGenericBaseName(n, src)
	case "qualified_name":
		if name := n.ChildByFieldName("name"); name != nil {
			return csTypeLastName(name, src)
		}
	case "nullable_type", "array_type":
		if t := n.ChildByFieldName("type"); t != nil {
			return csTypeLastName(t, src)
		}
	}
	// Fallback: last dotted segment of the raw text.
	text := strings.TrimSpace(n.Content(src))
	if i := strings.IndexAny(text, "<?["); i >= 0 {
		text = text[:i]
	}
	if i := strings.LastIndexByte(text, '.'); i >= 0 {
		text = text[i+1:]
	}
	return strings.TrimSpace(text)
}

// csIndexerDecl emits `public JToken this[string key] { get; set; }` as a
// field named "this[]" whose Signature carries the element type, so an
// element-access receiver (`o["x"].Children()`) can be typed by the
// receiver's indexer.
func csIndexerDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	raw := n.Content(src)
	sig := raw
	if i := strings.IndexByte(sig, '{'); i >= 0 {
		sig = sig[:i]
	}
	if i := strings.Index(sig, "=>"); i >= 0 {
		sig = sig[:i]
	}
	modifiers := csModifiers(n, src)
	*out = append(*out, astkit.Symbol{
		Kind:          astkit.KindField,
		Name:          "this[]",
		QualifiedName: qualJoin(parentClass, "this[]"),
		Signature:     strings.Join(strings.Fields(sig), " "),
		Span:          internalast.NodeSpan(n),
		Exported:      csIsExported(modifiers),
		Body:          raw,
		ParentName:    qualLast(parentClass),
		Modifiers:     modifiers,
		Annotations:   csAttributes(n, src),
	})
}

func csPropertyDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, parentInterface bool, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	modifiers := csModifiers(n, src)
	kind := astkit.KindField
	if parentInterface {
		kind = astkit.KindMethod
	}
	*out = append(*out, astkit.Symbol{
		Kind:          kind,
		Name:          name,
		QualifiedName: qualJoin(parentClass, name),
		Signature:     internalast.FirstLine(raw),
		Span:          internalast.NodeSpan(n),
		Exported:      csIsExported(modifiers),
		Body:          raw,
		ParentName:    qualLast(parentClass),
		Modifiers:     modifiers,
		Annotations:   csAttributes(n, src),
		CallSites:     csCallSites(n, src),
	})
}

func csFieldDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	modifiers := csModifiers(n, src)
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child == nil || child.Type() != "variable_declaration" {
			continue
		}
		for j := 0; j < int(child.ChildCount()); j++ {
			decl := child.Child(j)
			if decl == nil || decl.Type() != "variable_declarator" {
				continue
			}
			nameNode := decl.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			name := nameNode.Content(src)
			raw := decl.Content(src)
			*out = append(*out, astkit.Symbol{
				Kind: astkit.KindField, Name: name, QualifiedName: qualJoin(parentClass, name),
				Signature: internalast.FirstLine(raw), Span: internalast.NodeSpan(decl),
				Exported: csIsExported(modifiers), Body: raw, ParentName: qualLast(parentClass), Modifiers: modifiers,
			})
		}
	}
}

func csModifiers(n *sitter.Node, src []byte) []string {
	var mods []string
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child != nil && child.Type() == "modifier" {
			mods = append(mods, child.Content(src))
		}
	}
	return mods
}

func csIsExported(modifiers []string) bool {
	for _, m := range modifiers {
		if m == "public" || m == "protected" || m == "internal" {
			return true
		}
	}
	return false
}

func csTypeParams(n *sitter.Node, src []byte) []string {
	tp := internalast.FindChildByType(n, "type_parameter_list")
	if tp == nil {
		return nil
	}
	var params []string
	for i := 0; i < int(tp.ChildCount()); i++ {
		child := tp.Child(i)
		if child != nil && child.Type() == "type_parameter" {
			params = append(params, child.Content(src))
		}
	}
	return params
}

func csAttributes(n *sitter.Node, src []byte) []string {
	var attrs []string
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child != nil && child.Type() == "attribute_list" {
			attrs = append(attrs, child.Content(src))
		}
	}
	return attrs
}

// ─── PHP ─────────────────────────────────────────────────────────────────────

func extractPHPNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	// PHP files have a program node; sometimes wrapped in php_tag + program.
	phpVisit(root, filePath, blobSHA, src, imports, "", &out)
	return out
}

func phpVisit(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		n := node.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "function_definition":
			if sym := phpFuncSym(n, filePath, blobSHA, src, imports, parentClass); sym != nil {
				*out = append(*out, *sym)
			}
		case "class_declaration":
			phpClassDecl(n, astkit.KindClass, filePath, blobSHA, src, imports, out)
		case "interface_declaration":
			phpClassDecl(n, astkit.KindInterface, filePath, blobSHA, src, imports, out)
		case "trait_declaration":
			phpClassDecl(n, astkit.KindTrait, filePath, blobSHA, src, imports, out)
		case "enum_declaration":
			phpClassDecl(n, astkit.KindEnum, filePath, blobSHA, src, imports, out)
		case "method_declaration":
			if sym := phpFuncSym(n, filePath, blobSHA, src, imports, parentClass); sym != nil {
				*out = append(*out, *sym)
			}
		case "object_creation_expression":
			if body := internalast.FindChildByType(n, "declaration_list"); body != nil {
				phpAnonymousClassDecl(n, body, filePath, blobSHA, src, imports, out)
			} else {
				phpVisit(n, filePath, blobSHA, src, imports, parentClass, out)
			}
		default:
			// Recurse into program, namespace_definition, compound_statement, etc.
			phpVisit(n, filePath, blobSHA, src, imports, parentClass, out)
		}
	}
}

func phpAnonymousClassDecl(n, body *sitter.Node, filePath, blobSHA string, src []byte, imports []string, out *[]astkit.Symbol) {
	line := int(n.StartPoint().Row) + 1
	name := "<anonymous@" + strconv.Itoa(line) + ">"
	raw := n.Content(src)
	*out = append(*out, astkit.Symbol{
		Kind:          astkit.KindClass,
		Name:          name,
		QualifiedName: name,
		Signature:     internalast.SignatureBeforeBody(n, src),
		Span:          internalast.NodeSpan(n),
		Exported:      false,
		Body:          raw,
	})
	phpVisit(body, filePath, blobSHA, src, imports, name, out)
}

func phpFuncSym(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string) *astkit.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	kind := astkit.KindFunction
	if parentClass != "" {
		kind = astkit.KindMethod
		if strings.EqualFold(name, "__construct") {
			kind = astkit.KindConstructor
		}
	}
	return &astkit.Symbol{
		Kind:          kind,
		Name:          name,
		QualifiedName: name,
		Signature:     funcSig(n, src),
		Span:          internalast.NodeSpan(n),
		Exported:      phpIsExported(n, src),
		Body:          raw,
		ParentName:    parentClass,
		Modifiers:     phpModifiers(n, src),
		CallSites:     phpCallSites(n, src, parentClass),
	}
}

// phpCallSites extracts call sites from a PHP function/method declaration:
// free function calls, member calls ($obj->m()), static calls (Foo::m()),
// and object creation (new Foo()). Receiver qualifiers are preserved
// ($repo->save → "repo.save", $this->run → "this.run") so the graph layer
// can narrow by the receiver's inferred type instead of name alone.
func phpCallSites(decl *sitter.Node, src []byte, parentClass string) []astkit.CallSite {
	return collectCallSites(decl, src, callSpec{
		nodeTypes: []string{
			"function_call_expression", "member_call_expression",
			"nullsafe_member_call_expression", "scoped_call_expression",
			"object_creation_expression",
		},
		calleeFn: func(call *sitter.Node, src []byte) string {
			switch call.Type() {
			case "function_call_expression":
				fn := call.ChildByFieldName("function")
				if fn == nil {
					return ""
				}
				return phpLastNamePart(fn.Content(src))
			case "member_call_expression", "nullsafe_member_call_expression":
				name := call.ChildByFieldName("name")
				if name == nil {
					return ""
				}
				qual := phpReceiverName(call.ChildByFieldName("object"), src)
				return joinQualified(qual, name.Content(src))
			case "scoped_call_expression":
				name := call.ChildByFieldName("name")
				if name == nil {
					return ""
				}
				qual := phpScopeName(call.ChildByFieldName("scope"), src)
				return joinQualified(qual, name.Content(src))
			case "object_creation_expression":
				return phpNewClassName(call, src, parentClass)
			}
			return ""
		},
	})
}

// phpReceiverName reduces a member-call receiver to a bare qualifier:
// $repo → "repo", $this → "this", $this->field → "field", method() → "method()".
func phpReceiverName(obj *sitter.Node, src []byte) string {
	if obj == nil {
		return ""
	}
	switch obj.Type() {
	case "variable_name":
		return strings.TrimPrefix(obj.Content(src), "$")
	case "member_access_expression":
		if f := obj.ChildByFieldName("name"); f != nil {
			return f.Content(src)
		}
	case "member_call_expression", "nullsafe_member_call_expression", "function_call_expression", "scoped_call_expression":
		if f := obj.ChildByFieldName("name"); f != nil {
			return f.Content(src) + "()"
		}
		if f := obj.ChildByFieldName("function"); f != nil {
			return phpLastNamePart(f.Content(src)) + "()"
		}
	}
	return ""
}

// phpScopeName reduces a scoped-call scope to a bare qualifier: Foo → "Foo",
// Ns\Foo → "Foo", self/parent/static kept verbatim.
func phpScopeName(scope *sitter.Node, src []byte) string {
	if scope == nil {
		return ""
	}
	return phpLastNamePart(scope.Content(src))
}

// phpNewClassName returns the constructed class's bare name (new Foo() →
// "Foo", new Ns\Foo() → "Foo"); dynamic `new $cls()` yields "".
func phpNewClassName(call *sitter.Node, src []byte, parentClass string) string {
	for i := 0; i < int(call.ChildCount()); i++ {
		c := call.Child(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "name", "qualified_name":
			name := phpLastNamePart(c.Content(src))
			if (name == "self" || name == "static") && parentClass != "" {
				return phpLastNamePart(parentClass)
			}
			return name
		}
	}
	return ""
}

// phpLastNamePart reduces a (possibly namespaced) name to its last segment:
// "Ns\\Sub\\Foo" → "Foo", "foo" → "foo".
func phpLastNamePart(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\\'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func phpClassDecl(n *sitter.Node, kind astkit.SymbolKind, filePath, blobSHA string, src []byte, imports []string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	className := nameNode.Content(src)
	raw := n.Content(src)
	*out = append(*out, astkit.Symbol{
		Kind:          kind,
		Name:          className,
		QualifiedName: className,
		Signature:     internalast.SignatureBeforeBody(n, src),
		Span:          internalast.NodeSpan(n),
		Exported:      true,
		Body:          raw,
		Modifiers:     phpModifiers(n, src),
	})
	body := n.ChildByFieldName("body")
	if body == nil {
		return
	}
	phpVisit(body, filePath, blobSHA, src, imports, className, out)
}

func phpModifiers(n *sitter.Node, src []byte) []string {
	var mods []string
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "visibility_modifier", "static_modifier", "abstract_modifier", "final_modifier":
			mods = append(mods, child.Content(src))
		}
	}
	return mods
}

func phpIsExported(n *sitter.Node, src []byte) bool {
	for _, m := range phpModifiers(n, src) {
		if m == "public" {
			return true
		}
	}
	// Top-level functions (no class parent) are always accessible.
	return true
}

// ─── Swift ────────────────────────────────────────────────────────────────────
//
// The vendored Swift grammar models class/struct/enum/extension/actor under
// one node type, class_declaration, discriminated by its "declaration_kind"
// field (the keyword token itself). Protocols get their own node type.
// Function/init/deinit bodies are walked only for call sites, never
// recursed into for nested declarations — property_declaration also covers
// local `let`/`var` bindings inside a function body, and descending would
// misreport every local variable as a field.

func extractSwiftNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	swiftVisit(root, filePath, blobSHA, src, imports, "", &out)
	extConf := swiftExtensionConformances(root, src)
	for i := range out {
		switch out[i].Kind {
		case astkit.KindClass, astkit.KindStruct, astkit.KindEnum:
		default:
			continue
		}
		for _, p := range extConf[out[i].Name] {
			out[i].Annotations = append(out[i].Annotations, "implements:"+p)
		}
	}
	return out
}

// swiftExtensionConformances records protocol names an `extension X: P {}`
// adds to X. Unlike a class's own declaration (whose conformance list is
// part of its Signature, parsed downstream by Grove), an extension's
// conformances live in a separate declaration entirely and would otherwise
// be invisible — the Rust-strategy analog of rustImplTraits.
func swiftExtensionConformances(root *sitter.Node, src []byte) map[string][]string {
	out := map[string][]string{}
	internalast.WalkTree(root, func(n *sitter.Node) {
		if n == nil || n.Type() != "class_declaration" || swiftDeclarationKind(n, src) != "extension" {
			return
		}
		nameNode := n.ChildByFieldName("name")
		if nameNode == nil {
			return
		}
		name := nameNode.Content(src)
		if p := swiftInheritedNames(n, src); len(p) > 0 {
			out[name] = append(out[name], p...)
		}
	})
	return out
}

// swiftDeclarationKind returns the keyword distinguishing what a Swift
// class_declaration node represents ("class", "struct", "enum",
// "extension", "actor").
func swiftDeclarationKind(n *sitter.Node, src []byte) string {
	if k := n.ChildByFieldName("declaration_kind"); k != nil {
		return k.Content(src)
	}
	return "class"
}

// swiftInheritedNames returns the superclass/protocol names off a
// class_declaration or protocol_declaration's inheritance_specifier list.
func swiftInheritedNames(n *sitter.Node, src []byte) []string {
	var names []string
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil || c.Type() != "inheritance_specifier" {
			continue
		}
		t := c.ChildByFieldName("inherits_from")
		if t == nil {
			continue
		}
		if name := swiftTypeLastName(t, src); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func swiftTypeLastName(n *sitter.Node, src []byte) string {
	text := strings.TrimSpace(n.Content(src))
	if i := strings.IndexAny(text, "<&"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if i := strings.LastIndexByte(text, '.'); i >= 0 {
		text = text[i+1:]
	}
	return text
}

func swiftVisit(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, out *[]astkit.Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		n := node.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "class_declaration":
			swiftClassLikeDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "protocol_declaration":
			swiftProtocolDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "function_declaration":
			swiftFunctionDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "init_declaration":
			swiftInitDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "deinit_declaration":
			swiftDeinitDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "property_declaration":
			swiftPropertyDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "typealias_declaration":
			swiftNamedItem(n, astkit.KindType, filePath, blobSHA, src, imports, out)
		case "subscript_declaration":
			swiftSubscriptDecl(n, filePath, blobSHA, src, imports, implType, out)
		}
	}
}

// swiftClassLikeDecl handles class/struct/enum/actor declarations directly,
// and extensions by recursing into the body with no new symbol of its own
// (an extension declares no type; its members attach to the extended type).
func swiftClassLikeDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentChain string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	kindWord := swiftDeclarationKind(n, src)
	body := n.ChildByFieldName("body")

	if kindWord == "extension" {
		// No new type is declared; members attach to the extended type. The
		// extended type's own name is looked up (not qualJoin'd) because a
		// top-level extension of a nested type still names it unqualified
		// (extension Outer.Inner), which qualLast already reduces correctly.
		if body != nil {
			swiftVisit(body, filePath, blobSHA, src, imports, qualLast(name), out)
		}
		return
	}

	kind := astkit.KindClass
	switch kindWord {
	case "struct":
		kind = astkit.KindStruct
	case "enum":
		kind = astkit.KindEnum
	}
	modifiers := swiftModifiers(n, src)
	if kindWord == "actor" {
		modifiers = append(modifiers, "actor")
	}
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  qualJoin(parentChain, name),
		Signature:      internalast.SignatureBeforeBody(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       swiftIsExported(modifiers),
		Body:           n.Content(src),
		ParentName:     qualLast(parentChain),
		Modifiers:      modifiers,
		TypeParameters: swiftTypeParameters(n, src),
		Annotations:    swiftAttributes(n, src),
	})
	if body == nil {
		return
	}
	chain := qualJoin(parentChain, name)
	if body.Type() == "enum_class_body" {
		swiftEnumCases(body, src, name, out)
	}
	swiftVisit(body, filePath, blobSHA, src, imports, chain, out)
}

func swiftEnumCases(body *sitter.Node, src []byte, enumName string, out *[]astkit.Symbol) {
	for i := 0; i < int(body.ChildCount()); i++ {
		e := body.Child(i)
		if e == nil || e.Type() != "enum_entry" {
			continue
		}
		// A single `case a, b, c` entry repeats the "name" field per case.
		for j := 0; j < int(e.ChildCount()); j++ {
			if e.FieldNameForChild(j) != "name" {
				continue
			}
			c := e.Child(j)
			if c == nil {
				continue
			}
			name := c.Content(src)
			*out = append(*out, astkit.Symbol{
				Kind: astkit.KindConst, Name: name, QualifiedName: qualJoin(enumName, name),
				Signature: name, Span: internalast.NodeSpan(e), Exported: true,
				Body: e.Content(src), ParentName: enumName,
			})
		}
	}
}

func swiftProtocolDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentChain string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	modifiers := swiftModifiers(n, src)
	*out = append(*out, astkit.Symbol{
		Kind:          astkit.KindInterface,
		Name:          name,
		QualifiedName: qualJoin(parentChain, name),
		Signature:     internalast.SignatureBeforeBody(n, src),
		Span:          internalast.NodeSpan(n),
		Exported:      swiftIsExported(modifiers),
		Body:          n.Content(src),
		ParentName:    qualLast(parentChain),
		Modifiers:     modifiers,
		Annotations:   swiftAttributes(n, src),
	})
	body := n.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := 0; i < int(body.ChildCount()); i++ {
		m := body.Child(i)
		if m == nil {
			continue
		}
		switch m.Type() {
		case "protocol_function_declaration":
			nameNode := m.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			*out = append(*out, astkit.Symbol{
				Kind: astkit.KindMethod, Name: nameNode.Content(src), QualifiedName: nameNode.Content(src),
				Signature: internalast.FirstLine(m.Content(src)), Span: internalast.NodeSpan(m),
				Exported: true, Body: m.Content(src), ParentName: name,
			})
		case "protocol_property_declaration":
			swiftPropertyDecl(m, filePath, blobSHA, src, imports, name, out)
		}
	}
}

// swiftPropertyNames returns every bound identifier a property declaration
// names (usually one; `var a, b: Int` declares several under repeated
// "name" fields, mirroring swiftEnumCases).
func swiftPropertyNames(n *sitter.Node, src []byte) []string {
	var names []string
	for i := 0; i < int(n.ChildCount()); i++ {
		if n.FieldNameForChild(i) != "name" {
			continue
		}
		c := n.Child(i)
		if c == nil {
			continue
		}
		names = append(names, swiftPatternName(c, src))
	}
	return names
}

func swiftPatternName(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	if bi := n.ChildByFieldName("bound_identifier"); bi != nil {
		return bi.Content(src)
	}
	if n.Type() == "simple_identifier" {
		return n.Content(src)
	}
	return strings.TrimSpace(n.Content(src))
}

func swiftPropertyDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, out *[]astkit.Symbol) {
	names := swiftPropertyNames(n, src)
	if len(names) == 0 {
		return
	}
	modifiers := swiftModifiers(n, src)
	raw := n.Content(src)
	for _, name := range names {
		if name == "" {
			continue
		}
		*out = append(*out, astkit.Symbol{
			Kind: astkit.KindField, Name: name, QualifiedName: name,
			Signature: internalast.FirstLine(raw), Span: internalast.NodeSpan(n),
			Exported: swiftIsExported(modifiers), Body: raw, ParentName: qualLast(implType),
			Modifiers: modifiers, Annotations: swiftAttributes(n, src),
		})
	}
}

func swiftFunctionDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	kind := astkit.KindFunction
	if implType != "" {
		kind = astkit.KindMethod
	}
	modifiers := swiftModifiers(n, src)
	body := n.ChildByFieldName("body")
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  name,
		Signature:      funcSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       swiftIsExported(modifiers),
		Body:           n.Content(src),
		ParentName:     qualLast(implType),
		Modifiers:      modifiers,
		TypeParameters: swiftTypeParameters(n, src),
		Annotations:    swiftAttributes(n, src),
		CallSites:      sharedNavCallSites(body, src, true),
	})
}

// swiftInitDecl names the symbol after the enclosing type, not literally
// "init" — Swift constructs an instance by calling the type name
// (`Person(name: "x")`), never `init(...)` directly, so a caller's bare
// callee is the type name. Naming every overload identically to its type,
// the same convention astkit's Java/C# strategies already use for their
// own constructors, is what lets Grove's name-indexed candidate lookup
// (idx.byName[typeName]) find them at all; every overload keeping the
// SAME name is also what makes arity-based overload narrowing
// (declParamCount/filterByArgc, from each Symbol's own Signature) the
// right and sufficient disambiguator downstream — no astkit-side change
// needed there.
func swiftInitDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, out *[]astkit.Symbol) {
	modifiers := swiftModifiers(n, src)
	body := n.ChildByFieldName("body")
	name := qualLast(implType)
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindConstructor, Name: name, QualifiedName: name,
		Signature: funcSig(n, src), Span: internalast.NodeSpan(n),
		Exported: swiftIsExported(modifiers), Body: n.Content(src), ParentName: name,
		Modifiers: modifiers, Annotations: swiftAttributes(n, src),
		CallSites: sharedNavCallSites(body, src, true),
	})
}

func swiftDeinitDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, out *[]astkit.Symbol) {
	body := n.ChildByFieldName("body")
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindMethod, Name: "deinit", QualifiedName: "deinit",
		Signature: "deinit", Span: internalast.NodeSpan(n),
		Body: n.Content(src), ParentName: qualLast(implType), CallSites: sharedNavCallSites(body, src, true),
	})
}

func swiftSubscriptDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, out *[]astkit.Symbol) {
	modifiers := swiftModifiers(n, src)
	// A subscript's code lives in its get/set accessor blocks, not a single
	// "body" field; walking the whole declaration reaches both.
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindMethod, Name: "subscript", QualifiedName: qualJoin(qualLast(implType), "subscript"),
		Signature: internalast.FirstLine(n.Content(src)), Span: internalast.NodeSpan(n),
		Exported: swiftIsExported(modifiers), Body: n.Content(src), ParentName: qualLast(implType), Modifiers: modifiers,
		CallSites: sharedNavCallSites(n, src, true),
	})
}

func swiftNamedItem(n *sitter.Node, kind astkit.SymbolKind, filePath, blobSHA string, src []byte, imports []string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	modifiers := swiftModifiers(n, src)
	*out = append(*out, astkit.Symbol{
		Kind: kind, Name: name, QualifiedName: name,
		Signature: internalast.FirstLine(n.Content(src)), Span: internalast.NodeSpan(n),
		Exported: swiftIsExported(modifiers), Body: n.Content(src), Modifiers: modifiers,
		Annotations: swiftAttributes(n, src),
	})
}

func swiftModifiers(n *sitter.Node, src []byte) []string {
	var out []string
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil || c.Type() != "modifiers" {
			continue
		}
		for j := 0; j < int(c.ChildCount()); j++ {
			m := c.Child(j)
			if m == nil || !m.IsNamed() || m.Type() == "attribute" {
				continue
			}
			out = append(out, strings.TrimSpace(m.Content(src)))
		}
	}
	return out
}

func swiftIsExported(modifiers []string) bool {
	for _, m := range modifiers {
		switch m {
		case "public", "open":
			return true
		}
	}
	return false
}

func swiftAttributes(n *sitter.Node, src []byte) []string {
	var out []string
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c != nil && c.Type() == "attribute" {
			out = append(out, strings.TrimSpace(c.Content(src)))
		}
	}
	return out
}

func swiftTypeParameters(n *sitter.Node, src []byte) []string {
	tp := internalast.FindChildByType(n, "type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.ChildCount()); i++ {
		c := tp.Child(i)
		if c != nil && c.Type() == "type_parameter" {
			out = append(out, strings.TrimSpace(c.Content(src)))
		}
	}
	return out
}

// ─── Kotlin ───────────────────────────────────────────────────────────────────
//
// Unlike every other grammar astkit supports, the vendored Kotlin grammar
// exposes no field names at all — every relationship (declaration name,
// body, superclass list) is purely positional, so every helper here walks
// by node type instead of ChildByFieldName.

func extractKotlinNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	kotlinVisit(root, filePath, blobSHA, src, imports, "", &out)
	return out
}

// kotlinTypeName returns a class/interface/object declaration's name: the
// grammar's only direct type_identifier child (a superclass's type_identifier
// is nested inside a delegation_specifier, one level deeper).
func kotlinTypeName(n *sitter.Node) *sitter.Node {
	return internalast.FindChildByType(n, "type_identifier")
}

// kotlinDeclarationKeyword distinguishes class/interface/enum: the grammar
// reuses class_declaration for all three, discriminated by an anonymous
// leading keyword token rather than a field.
func kotlinDeclarationKeyword(n *sitter.Node) string {
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil || c.IsNamed() {
			continue
		}
		switch c.Type() {
		case "class", "interface", "enum":
			return c.Type()
		}
	}
	return "class"
}

func kotlinVisit(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, out *[]astkit.Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		n := node.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "class_declaration":
			kotlinClassDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "object_declaration":
			kotlinObjectDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "companion_object":
			// An anonymous singleton nested in a class; its members are
			// accessible as ClassName.member, so attribute them directly to
			// the enclosing class rather than modeling a separate type.
			if body := internalast.FindChildByType(n, "class_body"); body != nil {
				kotlinVisit(body, filePath, blobSHA, src, imports, implType, out)
			}
		case "function_declaration":
			kotlinFunctionDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "property_declaration":
			kotlinPropertyDecl(n, filePath, blobSHA, src, imports, implType, out)
		case "secondary_constructor":
			kotlinSecondaryConstructor(n, src, implType, out)
		}
	}
}

// kotlinConstructorSymbol emits a constructor named after its class (the
// Java/C#/Swift convention: `Person(...)` is what a caller writes), with a
// Signature of the form `Person(params)` so parameter parsing and arity
// narrowing read it like any callable.
func kotlinConstructorSymbol(className, qualified, params string, span astkit.LineRange, body string, modifiers []string, sites []astkit.CallSite, out *[]astkit.Symbol) {
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindConstructor, Name: className, QualifiedName: qualified,
		Signature: className + strings.Join(strings.Fields(params), " "), Span: span,
		Exported: kotlinIsExported(modifiers), Body: body, ParentName: className,
		Modifiers: modifiers, CallSites: sites,
	})
}

func kotlinPrimaryConstructorSites(pc, body *sitter.Node, src []byte) []astkit.CallSite {
	var sites []astkit.CallSite
	if pc != nil {
		sites = append(sites, sharedNavCallSites(pc, src, false)...)
	}
	if body == nil {
		return sites
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "anonymous_initializer":
			sites = append(sites, sharedNavCallSites(c, src, false)...)
		case "property_declaration":
			if internalast.FindChildByType(c, "getter") == nil && internalast.FindChildByType(c, "setter") == nil {
				sites = append(sites, sharedNavCallSites(c, src, false)...)
			}
		}
	}
	return sites
}

func kotlinSecondaryConstructor(n *sitter.Node, src []byte, implType string, out *[]astkit.Symbol) {
	params := "()"
	if p := internalast.FindChildByType(n, "function_value_parameters"); p != nil {
		params = p.Content(src)
	}
	kotlinConstructorSymbol(qualLast(implType), implType, params, internalast.NodeSpan(n), n.Content(src),
		kotlinModifiers(n, src), sharedNavCallSites(n, src, false), out)
}

func kotlinClassDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentChain string, out *[]astkit.Symbol) {
	nameNode := kotlinTypeName(n)
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	keyword := kotlinDeclarationKeyword(n)
	kind := astkit.KindClass
	switch keyword {
	case "interface":
		kind = astkit.KindInterface
	case "enum":
		kind = astkit.KindEnum
	}
	modifiers := kotlinModifiers(n, src)
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  qualJoin(parentChain, name),
		Signature:      kotlinSignatureBeforeBody(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       kotlinIsExported(modifiers),
		Body:           n.Content(src),
		ParentName:     qualLast(parentChain),
		Modifiers:      modifiers,
		TypeParameters: kotlinTypeParameters(n, src),
		Annotations:    kotlinAnnotations(n, src),
	})
	pc := internalast.FindChildByType(n, "primary_constructor")
	if pc != nil {
		kotlinPrimaryConstructorFields(pc, src, name, out)
	}
	body := internalast.FindChildByType(n, "class_body")
	if body == nil {
		body = internalast.FindChildByType(n, "enum_class_body")
	}
	if kind == astkit.KindClass {
		// Every class has a constructor kotlinc emits and callers invoke as
		// `Name(...)`: the primary one (its parameter list), or the implicit
		// no-arg one when the class declares neither a primary nor a
		// secondary constructor. Its span is the header, not the body, so
		// a constructor site inside the class binds the constructor, not
		// the class.
		//
		// kotlinc compiles parameter defaults, property initializers and
		// `init` blocks into that constructor, so their calls are its
		// call sites (property getters/setters run their own code).
		sites := kotlinPrimaryConstructorSites(pc, body, src)
		switch {
		case pc != nil:
			kotlinConstructorSymbol(name, qualJoin(parentChain, name), pc.Content(src), internalast.NodeSpan(pc), pc.Content(src), modifiers, sites, out)
		case body == nil || internalast.FindChildByType(body, "secondary_constructor") == nil:
			kotlinConstructorSymbol(name, qualJoin(parentChain, name), "()", internalast.NodeSpan(nameNode), name+"()", modifiers, sites, out)
		}
	}
	if body == nil {
		return
	}
	chain := qualJoin(parentChain, name)
	if body.Type() == "enum_class_body" {
		kotlinEnumEntries(body, src, name, out)
	}
	kotlinVisit(body, filePath, blobSHA, src, imports, chain, out)
}

func kotlinObjectDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentChain string, out *[]astkit.Symbol) {
	nameNode := kotlinTypeName(n)
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	modifiers := append(kotlinModifiers(n, src), "object")
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindClass, Name: name, QualifiedName: qualJoin(parentChain, name),
		Signature: kotlinSignatureBeforeBody(n, src), Span: internalast.NodeSpan(n),
		Exported: kotlinIsExported(modifiers), Body: n.Content(src), ParentName: qualLast(parentChain),
		Modifiers: modifiers, Annotations: kotlinAnnotations(n, src),
	})
	if body := internalast.FindChildByType(n, "class_body"); body != nil {
		kotlinVisit(body, filePath, blobSHA, src, imports, qualJoin(parentChain, name), out)
	}
}

func kotlinPrimaryConstructorFields(pc *sitter.Node, src []byte, className string, out *[]astkit.Symbol) {
	for i := 0; i < int(pc.ChildCount()); i++ {
		p := pc.Child(i)
		if p == nil || p.Type() != "class_parameter" {
			continue
		}
		if internalast.FindChildByType(p, "binding_pattern_kind") == nil {
			continue // a plain constructor argument, not a val/var property
		}
		nameNode := internalast.FindChildByType(p, "simple_identifier")
		if nameNode == nil {
			continue
		}
		modifiers := kotlinModifiers(p, src)
		*out = append(*out, astkit.Symbol{
			Kind: astkit.KindField, Name: nameNode.Content(src), QualifiedName: nameNode.Content(src),
			Signature: strings.TrimSpace(p.Content(src)), Span: internalast.NodeSpan(p),
			Exported: kotlinIsExported(modifiers), Body: p.Content(src), ParentName: className,
			Modifiers: modifiers,
		})
	}
}

func kotlinEnumEntries(body *sitter.Node, src []byte, enumName string, out *[]astkit.Symbol) {
	for i := 0; i < int(body.ChildCount()); i++ {
		e := body.Child(i)
		if e == nil || e.Type() != "enum_entry" {
			continue
		}
		nameNode := internalast.FindChildByType(e, "simple_identifier")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		*out = append(*out, astkit.Symbol{
			Kind: astkit.KindConst, Name: name, QualifiedName: qualJoin(enumName, name),
			Signature: name, Span: internalast.NodeSpan(e), Exported: true,
			Body: e.Content(src), ParentName: enumName,
		})
	}
}

// kotlinPropertyDecl handles a val/var member declaration. Only invoked as a
// direct child of a class_body (via kotlinVisit) — property_declaration also
// covers local bindings inside a function body, which are never visited
// here, avoiding misreporting locals as fields.
func kotlinPropertyDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, out *[]astkit.Symbol) {
	vd := internalast.FindChildByType(n, "variable_declaration")
	if vd == nil {
		return
	}
	nameNode := internalast.FindChildByType(vd, "simple_identifier")
	if nameNode == nil {
		return
	}
	modifiers := kotlinModifiers(n, src)
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindField, Name: nameNode.Content(src), QualifiedName: nameNode.Content(src),
		Signature: internalast.FirstLine(n.Content(src)), Span: internalast.NodeSpan(n),
		Exported: kotlinIsExported(modifiers), Body: n.Content(src), ParentName: qualLast(implType),
		Modifiers: modifiers, Annotations: kotlinAnnotations(n, src),
	})
}

func kotlinFunctionDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, implType string, out *[]astkit.Symbol) {
	nameNode := internalast.FindChildByType(n, "simple_identifier")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	kind := astkit.KindFunction
	if implType != "" {
		kind = astkit.KindMethod
	}
	modifiers := kotlinModifiers(n, src)
	body := internalast.FindChildByType(n, "function_body")
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  name,
		Signature:      kotlinFuncSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       kotlinIsExported(modifiers),
		Body:           n.Content(src),
		ParentName:     qualLast(implType),
		Modifiers:      modifiers,
		TypeParameters: kotlinTypeParameters(n, src),
		Annotations:    kotlinAnnotations(n, src),
		CallSites:      sharedNavCallSites(body, src, false),
	})
}

// kotlinFuncSig returns a function declaration's header (through its
// parameter list and return type) without the body — funcSig cannot be
// reused here since it looks up the body via ChildByFieldName("body"), and
// this grammar carries no field names.
func kotlinFuncSig(n *sitter.Node, src []byte) string {
	body := internalast.FindChildByType(n, "function_body")
	if body == nil {
		return internalast.FirstLine(strings.TrimSpace(n.Content(src)))
	}
	start := n.StartByte()
	bodyStart := body.StartByte()
	if bodyStart <= start {
		return internalast.FirstLine(n.Content(src))
	}
	sig := strings.TrimSpace(string(src[start:bodyStart]))
	sig = strings.TrimRight(sig, " \t\n{=")
	return strings.TrimSpace(sig)
}

// kotlinSignatureBeforeBody returns a class/interface/object declaration's
// header (through its superclass list) without the body — analogous to
// internalast.SignatureBeforeBody, which cannot be reused here since it
// locates the body via ChildByFieldName("body") and this grammar carries no
// field names.
func kotlinSignatureBeforeBody(n *sitter.Node, src []byte) string {
	body := internalast.FindChildByType(n, "class_body")
	if body == nil {
		body = internalast.FindChildByType(n, "enum_class_body")
	}
	if body == nil {
		return internalast.FirstLine(strings.TrimSpace(n.Content(src)))
	}
	start := n.StartByte()
	bodyStart := body.StartByte()
	if bodyStart <= start {
		return internalast.FirstLine(n.Content(src))
	}
	sig := strings.TrimSpace(string(src[start:bodyStart]))
	return strings.Join(strings.Fields(sig), " ")
}

func kotlinModifiers(n *sitter.Node, src []byte) []string {
	mods := internalast.FindChildByType(n, "modifiers")
	if mods == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(mods.ChildCount()); i++ {
		c := mods.Child(i)
		if c == nil || !c.IsNamed() || c.Type() == "annotation" {
			continue
		}
		out = append(out, strings.TrimSpace(c.Content(src)))
	}
	return out
}

func kotlinIsExported(modifiers []string) bool {
	for _, m := range modifiers {
		switch m {
		case "private", "internal":
			return false
		}
	}
	// Kotlin's default visibility (no modifier present) is public.
	return true
}

func kotlinAnnotations(n *sitter.Node, src []byte) []string {
	mods := internalast.FindChildByType(n, "modifiers")
	if mods == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(mods.ChildCount()); i++ {
		c := mods.Child(i)
		if c != nil && c.Type() == "annotation" {
			out = append(out, strings.TrimSpace(c.Content(src)))
		}
	}
	return out
}

func kotlinTypeParameters(n *sitter.Node, src []byte) []string {
	tp := internalast.FindChildByType(n, "type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.ChildCount()); i++ {
		c := tp.Child(i)
		if c != nil && c.Type() == "type_parameter" {
			out = append(out, strings.TrimSpace(c.Content(src)))
		}
	}
	return out
}

// ─── Objective-C ────────────────────────────────────────────────────────────
//
// Objective-C is a strict superset of C: astkit's existing C extractor
// (extractCNodes) already covers every plain C top-level construct a .m
// file may contain (functions, structs, enums, typedefs, #if-guarded
// regions) unchanged when passed "objc" as its language parameter — its
// only two `language == "cpp"` branches simply never fire for it. This
// walker layers the Objective-C-specific constructs
// (@interface/@protocol/@implementation and their members) on top.
//
// Unlike every class-based grammar astkit supports, an
// @interface/@protocol/@implementation has no enclosing "body" node — its
// member declarations are flat siblings of the header tokens (name,
// superclass, category parens, protocol list) between the opening keyword
// and @end. objcHeaderText locates the header/member boundary itself
// instead of reading a body field.

func extractObjCNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	out := extractCNodes(root, filePath, blobSHA, "objc", src, imports)
	for i := 0; i < int(root.ChildCount()); i++ {
		n := root.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "protocol_declaration":
			objcProtocolDecl(n, src, &out)
		case "class_interface":
			objcInterfaceDecl(n, src, &out)
		case "class_implementation":
			objcImplementationDecl(n, src, &out)
		}
	}
	return out
}

var objcMemberNodeTypes = map[string]bool{
	"property_declaration": true,
	"method_declaration":   true,
	"instance_variables":   true,
	"@end":                 true,
}

// objcHeaderText returns n's text from its start up to its first member
// declaration (or the whole node, if it declares none) — the
// name/superclass/protocol-list text buildExtendsImplements's "objc" case
// (Grove-side) parses.
func objcHeaderText(n *sitter.Node, src []byte) string {
	start := n.StartByte()
	end := n.EndByte()
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c != nil && objcMemberNodeTypes[c.Type()] {
			end = c.StartByte()
			break
		}
	}
	if end > uint32(len(src)) {
		end = uint32(len(src))
	}
	if start >= end {
		return internalast.FirstLine(n.Content(src))
	}
	return strings.Join(strings.Fields(string(src[start:end])), " ")
}

// objcCategoryName returns the category name of `@interface Foo (Bar)` — a
// category adds methods to an existing class, so it declares no new type —
// or "" for an ordinary (non-category) interface, recognized by having no
// "superclass" field.
func objcCategoryName(n *sitter.Node, src []byte) string {
	if n.ChildByFieldName("superclass") != nil {
		return ""
	}
	sawOpenParen := false
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch {
		case c.Type() == "(":
			sawOpenParen = true
		case sawOpenParen && c.Type() == "identifier":
			return c.Content(src)
		}
	}
	return ""
}

func objcInterfaceDecl(n *sitter.Node, src []byte, out *[]astkit.Symbol) {
	nameNode := n.NamedChild(0)
	if nameNode == nil || nameNode.Type() != "identifier" {
		return
	}
	className := nameNode.Content(src)
	if objcCategoryName(n, src) == "" {
		*out = append(*out, astkit.Symbol{
			Kind: astkit.KindClass, Name: className, QualifiedName: className,
			Signature: objcHeaderText(n, src), Span: internalast.NodeSpan(n),
			Exported: true, Body: n.Content(src),
		})
	}
	objcMembers(n, className, src, out)
}

func objcProtocolDecl(n *sitter.Node, src []byte, out *[]astkit.Symbol) {
	nameNode := n.NamedChild(0)
	if nameNode == nil || nameNode.Type() != "identifier" {
		return
	}
	name := nameNode.Content(src)
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindInterface, Name: name, QualifiedName: name,
		Signature: objcHeaderText(n, src), Span: internalast.NodeSpan(n),
		Exported: true, Body: n.Content(src),
	})
	objcMembers(n, name, src, out)
}

// objcMembers emits property and (bodyless) method-declaration symbols for
// the direct children of an @interface/@protocol node.
func objcMembers(n *sitter.Node, parentName string, src []byte, out *[]astkit.Symbol) {
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "property_declaration":
			objcPropertyDecl(c, parentName, src, out)
		case "method_declaration":
			objcMethodDeclSym(c, parentName, src, out)
		case "instance_variables":
			objcInstanceVariables(c, parentName, src, out)
		}
	}
}

// objcInstanceVariables emits one field per declarator of an @interface's
// or @implementation's `{ Type *a, *b; }` block.
func objcInstanceVariables(block *sitter.Node, parentName string, src []byte, out *[]astkit.Symbol) {
	for i := 0; i < int(block.NamedChildCount()); i++ {
		iv := block.NamedChild(i)
		if iv == nil || iv.Type() != "instance_variable" {
			continue
		}
		if decl := internalast.FindChildByType(iv, "struct_declaration"); decl != nil {
			objcFieldSymbols(decl, "", parentName, src, internalast.NodeSpan(iv), iv.Content(src), out)
		}
	}
}

// objcFieldSymbols emits one KindField per declarator of a
// struct_declaration (`Type *a, *b;`), each with a single-declarator
// Signature (`prefix Type *a;`) that Grove's ivar/property typing reads.
func objcFieldSymbols(decl *sitter.Node, prefix, parentName string, src []byte, span astkit.LineRange, body string, out *[]astkit.Symbol) {
	var typeParts []string
	for i := 0; i < int(decl.ChildCount()); i++ {
		c := decl.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "struct_declarator" {
			var nameNode *sitter.Node
			internalast.WalkTree(c, func(x *sitter.Node) {
				if x != nil && x.Type() == "identifier" {
					nameNode = x
				}
			})
			if nameNode == nil {
				continue
			}
			sig := strings.TrimSpace(prefix + " " + strings.Join(typeParts, " ") + " " + strings.Join(strings.Fields(c.Content(src)), "") + ";")
			*out = append(*out, astkit.Symbol{
				Kind: astkit.KindField, Name: nameNode.Content(src), QualifiedName: nameNode.Content(src),
				Signature: sig, Span: span, Exported: true, Body: body, ParentName: parentName,
			})
			continue
		}
		if c.IsNamed() {
			typeParts = append(typeParts, strings.Join(strings.Fields(c.Content(src)), " "))
		}
	}
}

func objcPropertyDecl(n *sitter.Node, parentName string, src []byte, out *[]astkit.Symbol) {
	// A @property line's "Type *name;" tail parses as a struct_declaration
	// (this grammar reuses the C field-declaration shape for it); each
	// declarator (`T *a, *b;`) is a property.
	decl := internalast.FindChildByType(n, "struct_declaration")
	if decl == nil {
		return
	}
	prefix := "@property"
	if attrs := internalast.FindChildByType(n, "property_attributes_declaration"); attrs != nil {
		prefix += " " + strings.Join(strings.Fields(attrs.Content(src)), " ")
	}
	objcFieldSymbols(decl, prefix, parentName, src, internalast.NodeSpan(n), n.Content(src), out)
}

func objcMethodDeclSym(n *sitter.Node, parentName string, src []byte, out *[]astkit.Symbol) {
	sel := objcDeclSelector(n, src)
	if sel == "" {
		return
	}
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindMethod, Name: sel, QualifiedName: sel,
		Signature: strings.Join(strings.Fields(n.Content(src)), " "), Span: internalast.NodeSpan(n),
		Exported: true, Body: n.Content(src), ParentName: parentName,
		Modifiers: objcMethodModifiers(n),
	})
}

func objcMethodModifiers(n *sitter.Node) []string {
	if objcIsClassMethod(n) {
		return []string{"class"}
	}
	return nil
}

// objcImplementationDecl emits the real (bodied, call-site-bearing) method
// symbols from an @implementation block. It emits no class symbol of its
// own: @implementation restates no superclass/protocol information the
// @interface declaration (commonly in a separate .h file) doesn't already
// carry, and Grove's ParentName-based resolution finds these methods
// against the interface's class symbol across files without one.
func objcImplementationDecl(n *sitter.Node, src []byte, out *[]astkit.Symbol) {
	nameNode := n.NamedChild(0)
	if nameNode == nil || nameNode.Type() != "identifier" {
		return
	}
	className := nameNode.Content(src)
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c != nil && c.Type() == "instance_variables" {
			objcInstanceVariables(c, className, src, out)
			continue
		}
		if c == nil || c.Type() != "implementation_definition" {
			continue
		}
		for j := 0; j < int(c.ChildCount()); j++ {
			m := c.Child(j)
			if m != nil && m.Type() == "method_definition" {
				objcMethodDefSym(m, className, src, out)
			}
		}
	}
}

func objcMethodDefSym(n *sitter.Node, parentName string, src []byte, out *[]astkit.Symbol) {
	sel := objcDeclSelector(n, src)
	if sel == "" {
		return
	}
	body := internalast.FindChildByType(n, "compound_statement")
	*out = append(*out, astkit.Symbol{
		Kind: astkit.KindMethod, Name: sel, QualifiedName: sel,
		Signature: objcMethodSigBeforeBody(n, src), Span: internalast.NodeSpan(n),
		Exported: true, Body: n.Content(src), ParentName: parentName,
		Modifiers: objcMethodModifiers(n),
		CallSites: objcCallSites(body, src),
	})
}

// objcMethodSigBeforeBody returns a method_definition's header (through its
// parameter list and return type) without the body.
func objcMethodSigBeforeBody(n *sitter.Node, src []byte) string {
	body := internalast.FindChildByType(n, "compound_statement")
	if body == nil {
		return internalast.FirstLine(strings.TrimSpace(n.Content(src)))
	}
	start := n.StartByte()
	bodyStart := body.StartByte()
	if bodyStart <= start {
		return internalast.FirstLine(n.Content(src))
	}
	sig := strings.TrimSpace(string(src[start:bodyStart]))
	return strings.Join(strings.Fields(sig), " ")
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// funcSig returns the function/method signature without the body.
func funcSig(n *sitter.Node, src []byte) string {
	body := n.ChildByFieldName("body")
	if body == nil {
		return strings.TrimSpace(n.Content(src))
	}
	start := n.StartByte()
	bodyStart := body.StartByte()
	if bodyStart <= start {
		return internalast.FirstLine(n.Content(src))
	}
	sig := strings.TrimSpace(string(src[start:bodyStart]))
	sig = strings.TrimRight(sig, " \t\n{")
	return strings.TrimSpace(sig)
}
