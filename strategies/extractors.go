package strategies

import (
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
		switch typeNode.Type() {
		case "pointer_type":
			for j := 0; j < int(typeNode.ChildCount()); j++ {
				c := typeNode.Child(j)
				if c != nil && c.Type() == "type_identifier" {
					return c.Content(src)
				}
			}
		case "type_identifier":
			return typeNode.Content(src)
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
			Kind:          kind,
			Name:          name,
			QualifiedName: name,
			Signature:     internalast.FirstLine(raw),
			Span:          internalast.NodeSpan(spec),
			Exported:      internalast.IsCapitalized(name),
			Body:          raw,
		})
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
				jsVisit(body, filePath, blobSHA, language, src, imports, sym.Name, false, out)
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
			Exported:      true,
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
	*out = append(*out, astkit.Symbol{
		Kind:           astkit.KindClass,
		Name:           className,
		QualifiedName:  className,
		Signature:      internalast.FirstLine(raw),
		Span:           internalast.NodeSpan(n),
		Exported:       exported,
		Body:           raw,
		ParentName:     parentClass,
		Modifiers:      jsModifiers(n, src),
		TypeParameters: jsTypeParameters(n, src),
		Annotations:    jsDecorators(n, src),
	})
	// Visit class body for methods. Methods are never directly exported.
	body := n.ChildByFieldName("body")
	if body != nil {
		jsVisit(body, filePath, blobSHA, language, src, imports, className, false, out)
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
		QualifiedName:  name,
		Signature:      funcSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       false, // methods are accessed via their class, not exported directly
		Body:           raw,
		ParentName:     parentClass,
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
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	*out = append(*out, astkit.Symbol{
		Kind:          astkit.KindField,
		Name:          name,
		QualifiedName: name,
		Signature:     internalast.FirstLine(raw),
		Span:          internalast.NodeSpan(n),
		Exported:      false,
		Body:          raw,
		ParentName:    parentClass,
		Modifiers:     jsModifiers(n, src),
		Annotations:   jsDecorators(n, src),
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
		QualifiedName:  name,
		Signature:      funcSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       exported,
		Body:           raw,
		ParentName:     parentClass,
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
				QualifiedName:  name,
				Signature:      internalast.FirstLine(raw),
				Span:           internalast.NodeSpan(decl),
				Exported:       exported,
				Body:           raw,
				ParentName:     parentClass,
				TypeParameters: jsTypeParameters(valueNode, src),
				CallSites:      jsCallSites(body, src),
			})
		}
	}
}

// ─── Python ──────────────────────────────────────────────────────────────────

func extractPythonNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	pythonVisit(root, filePath, blobSHA, src, imports, "", &out)
	return out
}

func pythonVisit(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		n := node.Child(i)
		if n == nil {
			continue
		}
		pythonVisitDefinition(n, filePath, blobSHA, src, imports, parentClass, nil, out)
	}
}

func pythonVisitDefinition(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, decorators []string, out *[]astkit.Symbol) {
	switch n.Type() {
	case "function_definition":
		nameNode := n.ChildByFieldName("name")
		if nameNode == nil {
			return
		}
		name := nameNode.Content(src)
		kind := astkit.KindFunction
		if parentClass != "" {
			kind = astkit.KindMethod
			if name == "__init__" {
				kind = astkit.KindConstructor
			}
		}
		raw := n.Content(src)
		body := n.ChildByFieldName("body")
		*out = append(*out, astkit.Symbol{
			Kind:          kind,
			Name:          name,
			QualifiedName: name,
			Signature:     internalast.FirstLine(raw),
			Span:          internalast.NodeSpan(n),
			Exported:      !strings.HasPrefix(name, "_"),
			Body:          raw,
			ParentName:    parentClass,
			Modifiers:     pythonModifiers(name),
			Annotations:   decorators,
			CallSites:     pythonCallSites(body, src),
			AttrSites:     pythonAttrSites(body, src),
		})
	case "class_definition":
		nameNode := n.ChildByFieldName("name")
		if nameNode == nil {
			return
		}
		className := nameNode.Content(src)
		raw := n.Content(src)
		*out = append(*out, astkit.Symbol{
			Kind:          astkit.KindClass,
			Name:          className,
			QualifiedName: className,
			Signature:     internalast.FirstLine(raw),
			Span:          internalast.NodeSpan(n),
			Exported:      !strings.HasPrefix(className, "_"),
			Body:          raw,
			ParentName:    parentClass,
			Modifiers:     pythonModifiers(className),
			Annotations:   decorators,
		})
		body := n.ChildByFieldName("body")
		if body != nil {
			pythonVisit(body, filePath, blobSHA, src, imports, className, out)
		}
	case "decorated_definition":
		decos := pythonDecorators(n, src)
		for j := 0; j < int(n.ChildCount()); j++ {
			inner := n.Child(j)
			if inner != nil && (inner.Type() == "function_definition" || inner.Type() == "class_definition") {
				pythonVisitDefinition(inner, filePath, blobSHA, src, imports, parentClass, decos, out)
				return
			}
		}
	}
}

// ─── Java ─────────────────────────────────────────────────────────────────────

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
		QualifiedName:  className,
		Signature:      sig,
		Span:           internalast.NodeSpan(n),
		Exported:       exports,
		Body:           raw,
		ParentName:     parentClass,
		Modifiers:      modifiers,
		TypeParameters: javaTypeParameters(n, src),
		Annotations:    javaAnnotations(n, src),
	})
	body := n.ChildByFieldName("body")
	if body != nil {
		javaVisit(body, filePath, blobSHA, src, imports, className, out)
	}
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
		QualifiedName:  name,
		Signature:      sig,
		Span:           internalast.NodeSpan(n),
		Exported:       exports,
		Body:           raw,
		ParentName:     parentClass,
		Modifiers:      modifiers,
		TypeParameters: javaTypeParameters(n, src),
		Annotations:    javaAnnotations(n, src),
		CallSites:      javaCallSites(body, src),
	})
}

func javaFieldDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	decl := internalast.FindChildByType(n, "variable_declarator")
	if decl == nil {
		return
	}
	nameNode := decl.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	sig := internalast.FirstLine(raw)
	modifiers := javaModifiers(n, src)
	exports := strings.Contains(sig, "public")
	for _, m := range modifiers {
		if m == "public" {
			exports = true
		}
	}
	*out = append(*out, astkit.Symbol{
		Kind:          astkit.KindField,
		Name:          name,
		QualifiedName: name,
		Signature:     sig,
		Span:          internalast.NodeSpan(n),
		Exported:      exports,
		Body:          raw,
		ParentName:    parentClass,
		Modifiers:     modifiers,
		Annotations:   javaAnnotations(n, src),
	})
}

// ─── Rust ─────────────────────────────────────────────────────────────────────

func extractRustNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	rustVisit(root, filePath, blobSHA, src, imports, "", nil, "", &out)
	return out
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
		case "struct_item":
			rustNamedItem(n, astkit.KindStruct, filePath, blobSHA, src, imports, out)
			rustStructFields(n, filePath, blobSHA, src, imports, out)
		case "enum_item":
			rustNamedItem(n, astkit.KindEnum, filePath, blobSHA, src, imports, out)
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
			// every #[cfg(test)] function in the file.
			if body := n.ChildByFieldName("body"); body != nil {
				rustVisit(body, filePath, blobSHA, src, imports, implType, implBounds, implTrait, out)
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

func extractCNodes(root *sitter.Node, filePath, blobSHA, language string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	for i := 0; i < int(root.ChildCount()); i++ {
		n := root.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "function_definition":
			if sym := cFuncSym(n, filePath, blobSHA, language, src, imports, ""); sym != nil {
				out = append(out, *sym)
			}
		case "declaration":
			// Catches typedef struct, extern function declarations, etc.
			out = append(out, cDeclarationSyms(n, filePath, blobSHA, language, src, imports)...)
		case "struct_specifier", "union_specifier":
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
		}
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
	if name == "" {
		return nil
	}
	raw := n.Content(src)
	kind := astkit.KindFunction
	if parentClass != "" {
		kind = astkit.KindMethod
	}
	return &astkit.Symbol{
		Kind:          kind,
		Name:          name,
		QualifiedName: name,
		Signature:     funcSig(n, src),
		Span:          internalast.NodeSpan(n),
		Exported:      !strings.HasPrefix(name, "_"),
		Body:          raw,
		ParentName:    parentClass,
		CallSites:     cCallSites(n, src),
	}
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
			out = append(out, astkit.Symbol{
				Kind:          astkit.KindFunction,
				Name:          name,
				QualifiedName: name,
				Signature:     strings.TrimSpace(raw),
				Span:          internalast.NodeSpan(n),
				Exported:      !strings.HasPrefix(name, "_"),
				Body:          raw,
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
	out := []astkit.Symbol{{
		Kind:          astkit.KindClass,
		Name:          className,
		QualifiedName: className,
		Signature:     internalast.FirstLine(raw),
		Span:          internalast.NodeSpan(n),
		Exported:      true,
		Body:          raw,
	}}
	// Extract member functions from the class body.
	body := n.ChildByFieldName("body")
	if body == nil {
		return out
	}
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "function_definition" {
			if sym := cFuncSym(child, filePath, blobSHA, language, src, imports, className); sym != nil {
				out = append(out, *sym)
			}
		}
	}
	return out
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
		case "class_specifier":
			return cppClassSym(child, filePath, blobSHA, language, src, imports)
		}
	}
	return nil
}

// ─── C# ───────────────────────────────────────────────────────────────────────

func extractCSharpNodes(root *sitter.Node, filePath, blobSHA string, src []byte, imports []string) []astkit.Symbol {
	var out []astkit.Symbol
	csVisit(root, filePath, blobSHA, src, imports, "", &out)
	return out
}

func csVisit(node *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
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
				csVisit(body, filePath, blobSHA, src, imports, "", out)
			}
		case "class_declaration", "record_declaration":
			csTypeDecl(n, astkit.KindClass, filePath, blobSHA, src, imports, parentClass, out)
		case "struct_declaration":
			csTypeDecl(n, astkit.KindStruct, filePath, blobSHA, src, imports, parentClass, out)
		case "interface_declaration":
			csTypeDecl(n, astkit.KindInterface, filePath, blobSHA, src, imports, parentClass, out)
		case "enum_declaration":
			csTypeDecl(n, astkit.KindEnum, filePath, blobSHA, src, imports, parentClass, out)
		case "method_declaration", "constructor_declaration", "destructor_declaration":
			csMethodDecl(n, filePath, blobSHA, src, imports, parentClass, out)
		case "property_declaration":
			csPropertyDecl(n, filePath, blobSHA, src, imports, parentClass, out)
		case "field_declaration":
			csFieldDecl(n, filePath, blobSHA, src, imports, parentClass, out)
		case "file_scoped_namespace_declaration":
			// `namespace Foo;` (C# 10): declarations follow as siblings in
			// the same node, sharing this namespace.
			csVisit(n, filePath, blobSHA, src, imports, parentClass, out)
		case "preproc_if", "preproc_elif", "preproc_else":
			// Multi-target code lives inside #if/#elif/#else blocks; the
			// grammar nests the conditional declarations as children. Not
			// descending hides whole files (Newtonsoft wraps every file in
			// `#if !(PORTABLE || ...)`) — the C# analog of Rust's mod_item.
			// All branches are visited so a symbol guarded by any target is
			// found; the oracle's chosen branch is always a subset.
			csVisit(n, filePath, blobSHA, src, imports, parentClass, out)
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
		QualifiedName:  name,
		Signature:      internalast.FirstLine(raw),
		Span:           internalast.NodeSpan(n),
		Exported:       csIsExported(modifiers),
		Body:           raw,
		ParentName:     parentClass,
		Modifiers:      modifiers,
		TypeParameters: csTypeParams(n, src),
		Annotations:    csAttributes(n, src),
	})
	if body := n.ChildByFieldName("body"); body != nil {
		csVisit(body, filePath, blobSHA, src, imports, name, out)
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
	if n.Type() == "constructor_declaration" {
		kind = astkit.KindConstructor
	}
	*out = append(*out, astkit.Symbol{
		Kind:           kind,
		Name:           name,
		QualifiedName:  name,
		Signature:      funcSig(n, src),
		Span:           internalast.NodeSpan(n),
		Exported:       csIsExported(modifiers),
		Body:           raw,
		ParentName:     parentClass,
		Modifiers:      modifiers,
		TypeParameters: csTypeParams(n, src),
		Annotations:    csAttributes(n, src),
		CallSites:      csCallSites(n, src),
	})
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
				qual := qualifierName(fn.ChildByFieldName("expression"), src,
					[]string{"identifier", "this_expression", "base_expression"},
					map[string]string{"member_access_expression": "name"},
					map[string]string{"invocation_expression": "function"})
				return joinQualified(qual, method)
			}
			return ""
		},
	})
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

func csPropertyDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Content(src)
	raw := n.Content(src)
	modifiers := csModifiers(n, src)
	*out = append(*out, astkit.Symbol{
		Kind:          astkit.KindField,
		Name:          name,
		QualifiedName: name,
		Signature:     internalast.FirstLine(raw),
		Span:          internalast.NodeSpan(n),
		Exported:      csIsExported(modifiers),
		Body:          raw,
		ParentName:    parentClass,
		Modifiers:     modifiers,
		Annotations:   csAttributes(n, src),
	})
}

func csFieldDecl(n *sitter.Node, filePath, blobSHA string, src []byte, imports []string, parentClass string, out *[]astkit.Symbol) {
	modifiers := csModifiers(n, src)
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child == nil || child.Type() != "variable_declaration" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		raw := child.Content(src)
		*out = append(*out, astkit.Symbol{
			Kind:          astkit.KindField,
			Name:          name,
			QualifiedName: name,
			Signature:     internalast.FirstLine(raw),
			Span:          internalast.NodeSpan(child),
			Exported:      csIsExported(modifiers),
			Body:          raw,
			ParentName:    parentClass,
			Modifiers:     modifiers,
		})
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
		default:
			// Recurse into program, namespace_definition, compound_statement, etc.
			phpVisit(n, filePath, blobSHA, src, imports, parentClass, out)
		}
	}
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
		CallSites:     phpCallSites(n, src),
	}
}

// phpCallSites extracts call sites from a PHP function/method declaration:
// free function calls, member calls ($obj->m()), static calls (Foo::m()),
// and object creation (new Foo()). Receiver qualifiers are preserved
// ($repo->save → "repo.save", $this->run → "this.run") so the graph layer
// can narrow by the receiver's inferred type instead of name alone.
func phpCallSites(decl *sitter.Node, src []byte) []astkit.CallSite {
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
				return phpNewClassName(call, src)
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
func phpNewClassName(call *sitter.Node, src []byte) string {
	for i := 0; i < int(call.ChildCount()); i++ {
		c := call.Child(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "name", "qualified_name":
			return phpLastNamePart(c.Content(src))
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
		Signature:     internalast.FirstLine(raw),
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
