package strategies

import (
	"regexp"
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/provasign/astkit"
	"github.com/provasign/astkit/internalast"
)

var _ = internalast.NodeSpan // imported by extractors.go callers

// ─── Generic call-site walker ────────────────────────────────────────────────
//
// collectCallSites walks every descendant of `body` and returns a CallSite for
// every call-expression node it encounters. Per-language wrappers map the
// idiomatic node types and callee field name.

type callSpec struct {
	nodeTypes []string // call-expression node types in this language
	calleeFn  func(call *sitter.Node, src []byte) string
	// genericFn reports whether a call carries explicit type arguments
	// (DeserializeObject<T>(...)); nil means "never generic" for the language.
	genericFn func(call *sitter.Node, src []byte) bool
}

// qualifierName reduces a callee's receiver operand to one identifier so
// CallSite.Callee can honor the documented "Receiver.callee" form:
//
//	r.Write(b)            → "r"
//	c.Writer.Flush()      → "Writer"  (last selector segment)
//	c.Writer.Header().Set → "Header()" (receiver is a call result)
//
// Unknown operand shapes (index expressions, parenthesized expressions)
// return "" and the callee stays bare — same behavior as before.
// selectorTypes maps this grammar's selector node type to its name field;
// callTypes maps its call node type to its function field.
func qualifierName(op *sitter.Node, src []byte, identTypes []string, selectorTypes, callTypes map[string]string) string {
	if op == nil {
		return ""
	}
	t := op.Type()
	for _, it := range identTypes {
		if t == it {
			return string(op.Content(src))
		}
	}
	if nameField, ok := selectorTypes[t]; ok {
		if f := op.ChildByFieldName(nameField); f != nil {
			return string(f.Content(src))
		}
		return ""
	}
	if fnField, ok := callTypes[t]; ok {
		inner := op.ChildByFieldName(fnField)
		if inner == nil {
			return ""
		}
		if name := qualifierName(inner, src, identTypes, selectorTypes, callTypes); name != "" {
			return name + "()"
		}
		// The inner function is itself a selector: reuse its name segment.
		for selType, nameField := range selectorTypes {
			if inner.Type() == selType {
				if f := inner.ChildByFieldName(nameField); f != nil {
					return string(f.Content(src)) + "()"
				}
			}
		}
		return ""
	}
	return ""
}

func joinQualified(qualifier, name string) string {
	if qualifier == "" {
		return name
	}
	// Identifier tokens can't contain whitespace; reject anything that
	// slipped through except the deliberate "name()" call-result marker.
	bare := strings.TrimSuffix(qualifier, "()")
	if strings.ContainsAny(bare, " \t\n().") {
		return name
	}
	return qualifier + "." + name
}

func collectCallSites(body *sitter.Node, src []byte, spec callSpec) []astkit.CallSite {
	if body == nil {
		return nil
	}
	var out []astkit.CallSite
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		for _, t := range spec.nodeTypes {
			if n.Type() == t {
				callee := spec.calleeFn(n, src)
				if callee != "" {
					out = append(out, astkit.CallSite{
						Callee:  callee,
						Line:    int(n.StartPoint().Row) + 1,
						Argc:    callArgc(n),
						Args:    callArgs(n, src),
						Generic: spec.genericFn != nil && spec.genericFn(n, src),
					})
				}
				break
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return out
}

// callArgc counts argument expressions in a call node's argument list.
func callArgc(call *sitter.Node) int {
	if call.Type() == "type_conversion_expression" {
		if call.ChildByFieldName("operand") != nil {
			return 1
		}
		return 0
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return 0
	}
	argc := 0
	for i := uint32(0); i < args.NamedChildCount(); i++ {
		child := args.NamedChild(int(i))
		if child != nil && child.Type() != "variadic_placeholder" {
			argc++
		}
	}
	return argc
}

// callArgs returns, per argument: the identifier text for bare identifiers,
// a "#type" literal marker for literals (so consumers can type-match
// overloads), or "" for complex expressions. Nil when nothing is known.
func callArgs(call *sitter.Node, src []byte) []string {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	n := int(args.NamedChildCount())
	out := make([]string, n)
	any := false
	for i := 0; i < n; i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		if v := argToken(c, src); v != "" {
			out[i] = v
			any = true
		}
	}
	if !any {
		return nil
	}
	return out
}

// argToken classifies one argument node: identifier name, or a literal
// type marker. Covers the literal node types of the Java/TS/JS/Go/Python
// grammars; unknown shapes return "".
func argToken(c *sitter.Node, src []byte) string {
	if c.Type() == "argument" || c.Type() == "value_argument" {
		// C# wraps each argument in an `argument` node; Swift/Kotlin wrap
		// each in a `value_argument` node — both shapes are (label)?
		// (ref/out/in or external-name)? expression. The value expression
		// is the last named child; unwrap it so literals/identifiers classify
		// instead of the wrapper returning "" (left Args empty entirely).
		if nc := int(c.NamedChildCount()); nc > 0 {
			return argToken(c.NamedChild(nc-1), src)
		}
		return ""
	}
	switch c.Type() {
	case "identifier", "simple_identifier":
		return string(c.Content(src))
	case "string_literal", "interpreted_string_literal", "raw_string_literal", "string":
		return "#String"
	case "character_literal", "rune_literal":
		return "#char"
	case "decimal_integer_literal", "hex_integer_literal", "octal_integer_literal",
		"binary_integer_literal", "int_literal", "integer", "integer_literal", "number_literal":
		text := string(c.Content(src))
		switch {
		case strings.HasSuffix(text, "L") || strings.HasSuffix(text, "l"):
			return "#long"
		case strings.HasSuffix(text, "F") || strings.HasSuffix(text, "f"):
			return "#float"
		case strings.HasSuffix(text, "D") || strings.HasSuffix(text, "d"):
			return "#double"
		}
		return "#int"
	case "boolean_literal":
		return "#boolean"
	case "decimal_floating_point_literal", "float_literal", "float", "real_literal":
		text := string(c.Content(src))
		if strings.HasSuffix(text, "F") || strings.HasSuffix(text, "f") {
			return "#float"
		}
		return "#double"
	case "true", "false":
		return "#boolean"
	case "cast_expression":
		// "(char[]) obj" — the canonical Java overload disambiguator.
		if t := c.ChildByFieldName("type"); t != nil {
			typ := strings.TrimSpace(string(t.Content(src)))
			if i := strings.IndexByte(typ, '<'); i >= 0 {
				typ = typ[:i]
			}
			return "#" + typ
		}
	case "field_access":
		// array.length is always int.
		f := c.ChildByFieldName("field")
		if f != nil && string(f.Content(src)) == "length" {
			return "#int"
		}
		// Integer.MAX_VALUE and friends are typed constants: without them
		// lastIndexOf(array, v, Integer.MAX_VALUE) cannot tell the int
		// startIndex overload from the double tolerance one.
		if f != nil {
			if fld := string(f.Content(src)); fld == "MAX_VALUE" || fld == "MIN_VALUE" {
				if obj := c.ChildByFieldName("object"); obj != nil {
					switch string(obj.Content(src)) {
					case "Integer":
						return "#int"
					case "Long":
						return "#long"
					case "Short":
						return "#short"
					case "Byte":
						return "#byte"
					case "Character":
						return "#char"
					case "Double":
						return "#double"
					case "Float":
						return "#float"
					}
				}
			}
		}
	case "member_access_expression":
		// C#: int.MaxValue / long.MinValue are typed constants.
		name := c.ChildByFieldName("name")
		obj := c.ChildByFieldName("expression")
		if name != nil && obj != nil {
			n := string(name.Content(src))
			t := string(obj.Content(src))
			if n == "MaxValue" || n == "MinValue" {
				switch t {
				case "int", "long", "short", "byte", "char", "double", "float", "decimal", "uint", "ulong", "ushort", "sbyte":
					return "#" + t
				}
			}
			// Formatting.Indented: a PascalCase member of a PascalCase
			// identifier is an enum value (or a static member) — "%Type"
			// lets a consumer that knows Type is an enum treat the
			// argument as typed Type.
			if obj.Type() == "identifier" && len(t) > 0 && t[0] >= 'A' && t[0] <= 'Z' && len(n) > 0 && n[0] >= 'A' && n[0] <= 'Z' {
				return "%" + t
			}
		}
	case "object_creation_expression":
		// `new JValue(1)` as an argument is typed by the created class.
		if t := c.ChildByFieldName("type"); t != nil {
			if name := csTypeLastName(t, src); name != "" {
				return "#" + name
			}
		}
	case "lambda_expression":
		// A lambda binds only a functional-interface parameter; consumers
		// rule out primitive, array and String overload slots. The
		// parameter COUNT additionally splits same-arity overloads whose
		// only difference is which functional interface they take
		// (Supplier<T> vs Function<Integer,T>): `() -> x` is 0-ary,
		// `x -> x` and `(x,y) -> x` count their bound names.
		if n := javaLambdaArity(c); n >= 0 {
			return "#lambda:" + strconv.Itoa(n)
		}
		return "#lambda"
	case "method_reference", "arrow_function", "lambda":
		return "#lambda"
	case "method_invocation", "call_expression", "call":
		// Consumers can resolve the called function's return type.
		if n := c.ChildByFieldName("name"); n != nil {
			return "call:" + string(n.Content(src))
		}
		if fn := c.ChildByFieldName("function"); fn != nil && fn.Type() == "identifier" {
			return "call:" + string(fn.Content(src))
		}
		// Swift/Kotlin: positional callee; `Type(...)` is a constructor
		// call whose result is that type.
		if c.NamedChildCount() > 0 {
			if fn := c.NamedChild(0); fn != nil && fn.Type() == "simple_identifier" {
				return "call:" + string(fn.Content(src))
			}
		}
	}
	return ""
}

// javaLambdaArity counts a Java lambda_expression's bound parameters:
// 0 for `() -> ...`, 1 for a bare `x -> ...` or single-element
// `(x) -> ...`, N for `(x, y, ...) -> ...`. Returns -1 when the shape is
// unrecognized (never guess wrong; the caller falls back to "#lambda").
func javaLambdaArity(c *sitter.Node) int {
	params := c.ChildByFieldName("parameters")
	if params == nil {
		return -1
	}
	switch params.Type() {
	case "identifier":
		return 1
	case "formal_parameters", "inferred_parameters":
		n := 0
		for i := 0; i < int(params.NamedChildCount()); i++ {
			if params.NamedChild(i) != nil {
				n++
			}
		}
		return n
	}
	return -1
}

func goCallSites(body *sitter.Node, src []byte) []astkit.CallSite {
	identTypes := []string{"identifier"}
	selectorTypes := map[string]string{"selector_expression": "field"}
	callTypes := map[string]string{"call_expression": "function"}
	out := collectCallSites(body, src, callSpec{
		nodeTypes: []string{"call_expression", "type_conversion_expression"},
		calleeFn: func(call *sitter.Node, src []byte) string {
			fn := call.ChildByFieldName("function")
			if call.Type() == "type_conversion_expression" {
				fn = call.ChildByFieldName("type")
			}
			if fn == nil {
				return ""
			}
			if fn.Type() == "generic_type" {
				fn = fn.ChildByFieldName("type")
				if fn == nil {
					return ""
				}
			}
			switch fn.Type() {
			case "identifier", "type_identifier":
				return fn.Content(src)
			case "selector_expression":
				field := fn.ChildByFieldName("field")
				if field != nil {
					qual := qualifierName(fn.ChildByFieldName("operand"), src, identTypes, selectorTypes, callTypes)
					return joinQualified(qual, string(field.Content(src)))
				}
			}
			return ""
		},
	})
	if body == nil {
		return out
	}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "selector_expression" {
			parent := n.Parent()
			isCalled := false
			if parent != nil && parent.Type() == "call_expression" {
				if fn := parent.ChildByFieldName("function"); fn != nil {
					isCalled = fn.StartByte() == n.StartByte() && fn.EndByte() == n.EndByte()
				}
			}
			if !isCalled {
				if field := n.ChildByFieldName("field"); field != nil {
					qual := qualifierName(n.ChildByFieldName("operand"), src, identTypes, selectorTypes, callTypes)
					out = append(out, astkit.CallSite{
						Callee: joinQualified(qual, field.Content(src)),
						Line:   int(n.StartPoint().Row) + 1, ReferenceOnly: true,
					})
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return out
}

func jsCallSites(body *sitter.Node, src []byte) []astkit.CallSite {
	return collectCallSites(body, src, callSpec{
		nodeTypes: []string{"call_expression", "new_expression", "jsx_opening_element", "jsx_self_closing_element"},
		calleeFn: func(call *sitter.Node, src []byte) string {
			if call.Type() == "jsx_opening_element" || call.Type() == "jsx_self_closing_element" {
				if name := call.ChildByFieldName("name"); name != nil {
					return name.Content(src)
				}
				return ""
			}
			fn := call.ChildByFieldName("function")
			if fn == nil {
				// new_expression uses "constructor" field
				fn = call.ChildByFieldName("constructor")
			}
			if fn == nil {
				return ""
			}
			switch fn.Type() {
			case "super":
				// super(...) invokes the base class constructor; graph
				// consumers resolve it through the extends relation.
				return "super()"
			case "identifier", "property_identifier":
				return fn.Content(src)
			case "member_expression":
				prop := fn.ChildByFieldName("property")
				if prop != nil {
					qual := qualifierName(fn.ChildByFieldName("object"), src,
						[]string{"identifier", "property_identifier", "this", "super"},
						map[string]string{"member_expression": "property"},
						map[string]string{
							"call_expression": "function",
							"new_expression":  "constructor",
						})
					return joinQualified(qual, string(prop.Content(src)))
				}
			}
			return ""
		},
	})
}

func pythonCallSites(body *sitter.Node, src []byte) []astkit.CallSite {
	if body == nil {
		return nil
	}
	var out []astkit.CallSite
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil || (n != body && pythonNestedScope(n.Type())) {
			return
		}
		if n.Type() == "call" {
			if callee := pythonCallCallee(n, src); callee != "" {
				out = append(out, astkit.CallSite{
					Callee: callee, Line: int(n.StartPoint().Row) + 1,
					Argc: callArgc(n), Args: callArgs(n, src),
				})
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return out
}

func pythonNestedScope(nodeType string) bool {
	return nodeType == "function_definition" || nodeType == "class_definition" || nodeType == "decorated_definition"
}

func pythonCallCallee(call *sitter.Node, src []byte) string {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return ""
	}
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "attribute":
		attr := fn.ChildByFieldName("attribute")
		if attr != nil {
			qual := pythonQualifierName(fn.ChildByFieldName("object"), src)
			if qual != "" {
				return qual + "." + attr.Content(src)
			}
			return attr.Content(src)
		}
	}
	return ""
}

func pythonQualifierName(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	switch n.Type() {
	case "identifier":
		return n.Content(src)
	case "attribute":
		object := pythonQualifierName(n.ChildByFieldName("object"), src)
		attr := n.ChildByFieldName("attribute")
		if attr == nil {
			return object
		}
		if object == "" {
			return attr.Content(src)
		}
		return object + "." + attr.Content(src)
	case "call":
		if fn := pythonQualifierName(n.ChildByFieldName("function"), src); fn != "" {
			return fn + "()"
		}
	}
	return ""
}

// pythonAttrSites collects attribute accesses that are not the function of
// a call ("self.db", "request.blueprints") — the access pattern through
// which @property code executes. One site per distinct access text.
func pythonAttrSites(body *sitter.Node, src []byte) []astkit.CallSite {
	if body == nil {
		return nil
	}
	identTypes := []string{"identifier"}
	selectorTypes := map[string]string{"attribute": "attribute"}
	callTypes := map[string]string{"call": "function"}
	seen := map[string]bool{}
	var out []astkit.CallSite
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil || (n != body && pythonNestedScope(n.Type())) {
			return
		}
		if n.Type() == "attribute" {
			parent := n.Parent()
			isCallFn := false
			isWrite := false
			if parent != nil && parent.Type() == "call" {
				// Node lookups aren't pointer-stable; compare by span.
				if fn := parent.ChildByFieldName("function"); fn != nil {
					isCallFn = fn.StartByte() == n.StartByte() && fn.EndByte() == n.EndByte()
				}
			}
			if parent != nil && (parent.Type() == "assignment" || parent.Type() == "augmented_assignment") {
				if left := parent.ChildByFieldName("left"); left != nil {
					isWrite = left.StartByte() == n.StartByte() && left.EndByte() == n.EndByte()
				}
			}
			if !isCallFn {
				if attr := n.ChildByFieldName("attribute"); attr != nil {
					qual := qualifierName(n.ChildByFieldName("object"), src, identTypes, selectorTypes, callTypes)
					name := joinQualified(qual, string(attr.Content(src)))
					key := name
					if isWrite {
						key += "#write"
					}
					if !seen[key] {
						seen[key] = true
						out = append(out, astkit.CallSite{
							Callee: name,
							Line:   int(n.StartPoint().Row) + 1,
							Write:  isWrite,
						})
					}
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return out
}

func javaCallSites(body *sitter.Node, src []byte) []astkit.CallSite {
	if body == nil {
		return nil
	}
	var out []astkit.CallSite
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Type() {
		case "lambda_expression", "class_declaration", "record_declaration", "interface_declaration", "enum_declaration":
			return // nested executable/type owns its descendants
		case "method_invocation", "object_creation_expression", "explicit_constructor_invocation":
			if callee := javaCallCallee(n, src); callee != "" {
				out = append(out, astkit.CallSite{
					Callee: callee, Line: int(n.StartPoint().Row) + 1,
					Argc: callArgc(n), Args: callArgs(n, src),
				})
			}
		case "method_reference":
			if qual, name, ok := strings.Cut(n.Content(src), "::"); ok {
				qual = strings.TrimSpace(qual)
				name = strings.TrimSpace(name)
				if strings.HasPrefix(name, "<") {
					if close := strings.IndexByte(name, '>'); close >= 0 {
						name = strings.TrimSpace(name[close+1:])
					}
				}
				if name == "new" {
					name = qual
				} else {
					name = joinQualified(qual, name)
				}
				out = append(out, astkit.CallSite{
					Callee: name, Line: int(n.StartPoint().Row) + 1, ReferenceOnly: true,
				})
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			child := n.Child(i)
			if n.Type() == "object_creation_expression" && child != nil && child.Type() == "class_body" {
				continue
			}
			walk(child)
		}
	}
	walk(body)
	return out
}

func javaCallCallee(call *sitter.Node, src []byte) string {
	if call.Type() == "object_creation_expression" {
		t := call.ChildByFieldName("type")
		if t == nil {
			return ""
		}
		// "new Range<>(...)" → callee "Range", not "Range<>".
		name := strings.TrimSpace(t.Content(src))
		if i := strings.IndexByte(name, '<'); i >= 0 {
			name = name[:i]
		}
		return name
	}
	if call.Type() == "explicit_constructor_invocation" {
		// `super(...)` / `this(...)` constructor delegation.
		if t := strings.TrimSpace(call.Content(src)); strings.HasPrefix(t, "super") {
			return "super()"
		} else if strings.HasPrefix(t, "this") {
			return "this()"
		}
		return ""
	}
	name := call.ChildByFieldName("name")
	if name == nil {
		return ""
	}
	obj := call.ChildByFieldName("object")
	qual := javaReceiverQualifier(obj, src)
	if qual == "" {
		qual = qualifierName(obj, src,
			[]string{"identifier", "this", "super"},
			map[string]string{"field_access": "field"},
			map[string]string{"method_invocation": "name"})
	}
	return joinQualified(qual, string(name.Content(src)))
}

// javaReceiverQualifier names the receiver for the expression shapes
// qualifierName cannot: a cast `((String) cs).indexOf(...)` is typed by the
// cast, an array element `array[i].intValue()` by the array variable, and
// `String.class.equals(...)` by Class. Without these the call arrived BARE
// and bound to the caller's own same-named method, or fanned out to every
// same-named method in the repo. Returns "" for shapes it does not handle.
func javaReceiverQualifier(obj *sitter.Node, src []byte) string {
	if obj == nil {
		return ""
	}
	switch obj.Type() {
	case "parenthesized_expression":
		for i := 0; i < int(obj.NamedChildCount()); i++ {
			inner := obj.NamedChild(i)
			if inner != nil && inner.Type() == "cast_expression" {
				if t := inner.ChildByFieldName("type"); t != nil {
					typ := strings.TrimSpace(string(t.Content(src)))
					if j := strings.IndexByte(typ, '<'); j >= 0 {
						typ = typ[:j]
					}
					typ = strings.TrimSuffix(typ, "[]")
					if j := strings.LastIndexByte(typ, '.'); j >= 0 {
						typ = typ[j+1:]
					}
					return typ
				}
			}
		}
	case "array_access":
		if arr := obj.ChildByFieldName("array"); arr != nil && arr.Type() == "identifier" {
			return string(arr.Content(src))
		}
	case "class_literal":
		return "Class" // String.class.equals(...) runs on a java.lang.Class
	}
	return ""
}

func rustCallSites(body *sitter.Node, src []byte) []astkit.CallSite {
	out := collectCallSites(body, src, callSpec{
		nodeTypes: []string{"call_expression", "macro_invocation"},
		calleeFn: func(call *sitter.Node, src []byte) string {
			if call.Type() == "macro_invocation" {
				m := call.ChildByFieldName("macro")
				if m != nil {
					return m.Content(src) + "!"
				}
				return ""
			}
			fn := call.ChildByFieldName("function")
			if fn == nil {
				return ""
			}
			if fn.Type() == "generic_function" {
				// foo::<T>(...) — unwrap the turbofish to the underlying
				// path before extracting the callee.
				if inner := fn.ChildByFieldName("function"); inner != nil {
					fn = inner
				}
			}
			switch fn.Type() {
			case "identifier":
				return fn.Content(src)
			case "field_expression":
				field := fn.ChildByFieldName("field")
				if field != nil {
					// scoped_identifier in the selector map lets a chain
					// rooted at a path call keep its qualifier:
					// SearcherTester::new(...).line_number(false) gives
					// line_number the "new()" call-result qualifier
					// instead of arriving bare.
					qual := qualifierName(fn.ChildByFieldName("value"), src,
						[]string{"identifier", "self"},
						map[string]string{"field_expression": "field", "scoped_identifier": "name"},
						map[string]string{"call_expression": "function"})
					return joinQualified(qual, string(field.Content(src)))
				}
			case "scoped_identifier":
				// Path calls keep their qualifying segment: Searcher::new
				// must arrive as "Searcher.new", not a bare "new" that
				// matches every constructor in scope (the v0.4.2 lesson).
				name := fn.ChildByFieldName("name")
				if name != nil {
					return joinQualified(rustPathQualifier(fn.ChildByFieldName("path"), src), string(name.Content(src)))
				}
			}
			return ""
		},
	})
	out = append(out, rustMacroCallSites(body, src)...)
	var nested []astkit.LineRange
	internalast.WalkTree(body, func(n *sitter.Node) {
		if n != nil && n.Type() == "function_item" {
			nested = append(nested, internalast.NodeSpan(n))
		}
	})
	if len(nested) > 0 {
		kept := out[:0]
		for _, site := range out {
			insideNested := false
			for _, span := range nested {
				if site.Line >= span.Start && site.Line <= span.End {
					insideNested = true
					break
				}
			}
			if !insideNested {
				kept = append(kept, site)
			}
		}
		out = kept
	}
	return out
}

// rustMacroStringRe strips string literals from macro token trees so format
// strings can't fabricate call sites.
var rustMacroStringRe = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// rustMacroCallRe finds call-shaped token runs inside a macro's token tree:
// "unescape(", "m.start(", "SearcherTester::new(".
var rustMacroCallRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*(?:(?:::|\.)[A-Za-z_][A-Za-z0-9_]*)*)\s*\(`)

var rustKeywordCallees = map[string]bool{
	"if": true, "match": true, "while": true, "for": true, "loop": true,
	"return": true, "as": true, "in": true, "fn": true, "move": true,
	"unsafe": true, "let": true, "else": true,
}

// rustMacroCallSites recovers calls written inside macro invocations
// (assert_eq!(b(b"\x00"), unescape(r"\x00"))). Macro arguments are token
// trees, not parsed expressions, so the AST walk cannot see them; without
// this every call under assert!/assert_eq!/write! is invisible — which in
// idiomatic Rust is most of the test suite's call surface.
func rustMacroCallSites(body *sitter.Node, src []byte) []astkit.CallSite {
	if body == nil {
		return nil
	}
	var out []astkit.CallSite
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "macro_invocation" {
			if tt := internalast.FindChildByType(n, "token_tree"); tt != nil {
				text := rustMacroStringRe.ReplaceAllString(tt.Content(src), `""`)
				line := int(n.StartPoint().Row) + 1
				for _, m := range rustMacroCallRe.FindAllStringSubmatchIndex(text, -1) {
					path := text[m[2]:m[3]]
					name := path
					qual := ""
					if i := strings.LastIndex(path, "::"); i >= 0 {
						name = path[i+2:]
						qual = rustLastSegment(path[:i])
					} else if i := strings.LastIndexByte(path, '.'); i >= 0 {
						name = path[i+1:]
						qual = rustLastSegment(path[:i])
					} else if q := rustMacroChainQualifier(text, m[0]); q != "" {
						// `ig.matched("", false).is_ignore()`: the regex
						// scan (no nested-structure awareness) matches
						// "ig.matched(" first, consuming it whole, so the
						// re-scan for the NEXT call starts past its "(" and
						// can only see the bare trailing ".is_ignore(" —
						// the receiver chain up to the dot is invisible to
						// a fresh match. Recover it by walking backward
						// from a chained call's own start: skip the ".",
						// find the immediately preceding call's closing
						// paren, and use ITS name as the call-result
						// qualifier, matching astkit's ordinary "name()"
						// convention for a chained receiver.
						qual = q
					}
					if rustKeywordCallees[name] || name == "" {
						continue
					}
					out = append(out, astkit.CallSite{
						Callee: joinQualified(qual, name),
						Line:   line,
					})
				}
			}
			return // nested macros' token trees are part of this text
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return out
}

// rustMacroChainQualifier looks backward from a macro-scanned call's start
// (the byte offset of its first identifier char) for ").ident(" immediately
// preceding it — a call chained onto a PRIOR call's result, invisible to
// rustMacroCallRe's single-pass scan because the prior call already
// consumed up through its own "(". Returns "priorName()" (the call-result
// marker astkit's ordinary receiver-chain resolution already understands),
// or "" when the call isn't chained this way.
func rustMacroChainQualifier(text string, start int) string {
	i := start
	for i > 0 && (text[i-1] == ' ' || text[i-1] == '\t' || text[i-1] == '\n') {
		i--
	}
	if i == 0 || text[i-1] != '.' {
		return ""
	}
	i--
	for i > 0 && (text[i-1] == ' ' || text[i-1] == '\t' || text[i-1] == '\n') {
		i--
	}
	if i == 0 || text[i-1] != ')' {
		return ""
	}
	close := i - 1
	depth := 1
	j := close
	for j > 0 && depth > 0 {
		j--
		switch text[j] {
		case ')':
			depth++
		case '(':
			depth--
		}
	}
	if depth != 0 {
		return ""
	}
	// j is the matching "(": the identifier immediately before it names
	// the prior call. Walk back over identifier characters only — a
	// non-identifier boundary (another ")", a "]", an operator) means the
	// callee isn't a simple name and the chain is too irregular to trust.
	end := j
	for j > 0 && rustIdentByte(text[j-1]) {
		j--
	}
	if j == end || (j > 0 && text[j-1] == ')') {
		return ""
	}
	name := text[j:end]
	if name == "" || rustKeywordCallees[name] {
		return ""
	}
	return name + "()"
}

func rustIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

// rustLastSegment reduces a dotted/scoped prefix to its final segment.
func rustLastSegment(p string) string {
	if i := strings.LastIndex(p, "::"); i >= 0 {
		p = p[i+2:]
	}
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// rustPathQualifier reduces a Rust path node to its last plain segment:
// "crate::flags::parse" → "parse", "Searcher" → "Searcher",
// "Vec<u8>" → "Vec". Single-segment by the same rule as qualifierName.
func rustPathQualifier(path *sitter.Node, src []byte) string {
	if path == nil {
		return ""
	}
	p := string(path.Content(src))
	if strings.HasPrefix(p, "<") && strings.HasSuffix(p, ">") {
		if typ, _, ok := strings.Cut(strings.TrimSpace(p[1:len(p)-1]), " as "); ok {
			p = strings.TrimSpace(typ)
		}
	}
	if i := strings.IndexByte(p, '<'); i >= 0 {
		p = strings.TrimSuffix(p[:i], "::")
	}
	if i := strings.LastIndex(p, "::"); i >= 0 {
		p = p[i+2:]
	}
	return p
}

// ─── Swift / Kotlin call sites ────────────────────────────────────────────────
//
// The vendored Swift and Kotlin grammars share identical node shapes for
// calls and member access (call_expression → call_suffix → value_arguments;
// navigation_expression → target, navigation_suffix) but — unlike every other
// grammar astkit supports — expose no field names on any of them. Every
// relationship is purely positional, so these helpers walk by node type and
// child index instead of ChildByFieldName, and are shared by both languages.

// sharedNavCallSites walks body for call_expression nodes and returns one
// CallSite per call. Nested closures/lambdas are covered (no separate scope
// boundary is tracked), matching how other languages attribute lambda calls
// to their enclosing declaration.
//
// labelArgs selects Swift's Args encoding: in Swift an argument label is
// part of the callee's identity (`init(parseJSON:)` and `init(jsonObject:)`
// are different functions, and the compiler tells same-arity overloads
// apart by exactly these labels), so each Args entry is "label:value" —
// "_" for an unlabeled argument — instead of the bare value token every
// other language records. Kotlin passes false: its named arguments are
// optional sugar that never distinguish overloads.
func sharedNavCallSites(body *sitter.Node, src []byte, labelArgs bool) []astkit.CallSite {
	if body == nil {
		return nil
	}
	var out []astkit.CallSite
	internalast.WalkTree(body, func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Type() {
		case "check_expression":
			// Kotlin `a in b` / `a !in b` desugars to `b.contains(a)` — an
			// `operator fun contains` call with no call syntax at all.
			if site, ok := navInOperatorSite(n, src, labelArgs); ok {
				out = append(out, site)
			}
			return
		case "additive_expression", "multiplicative_expression":
			// Kotlin `a + b` is `a.plus(b)` when a's type declares
			// `operator fun plus` (turtle's `Executable("ls") + args`).
			// Swift operators are static functions, not methods — skip.
			if !labelArgs {
				if site, ok := navBinaryOperatorSite(n, src); ok {
					out = append(out, site)
				}
			}
			return
		case "constructor_delegation_call":
			// Kotlin `constructor(...) : this(...)` / `: super(...)`, the
			// same forms Grove resolves for Java/C# as "this()"/"super()".
			if n.ChildCount() > 0 {
				if kw := n.Child(0); kw != nil && (kw.Type() == "this" || kw.Type() == "super") {
					out = append(out, astkit.CallSite{
						Callee: kw.Type() + "()",
						Line:   int(n.StartPoint().Row) + 1,
						Argc:   navValueArgumentCount(internalast.FindChildByType(n, "value_arguments")),
					})
				}
			}
			return
		}
		if n.Type() != "call_expression" || n.NamedChildCount() < 1 {
			return
		}
		var callee string
		if navIsSubscript(n, src) {
			// `x[i]` is a call_expression whose suffix is bracketed: a
			// subscript access, which Swift routes through the receiver
			// type's `subscript` declaration (astkit names it exactly
			// that). Read literally it would be a call to a function named
			// after the receiver.
			callee = joinQualified(navQualifierName(n.NamedChild(0), src), "subscript")
			if callee == "subscript" {
				return
			}
		} else {
			callee = navCalleeName(n.NamedChild(0), src)
		}
		if callee == "" {
			return
		}
		out = append(out, astkit.CallSite{
			Callee: callee,
			Line:   int(n.StartPoint().Row) + 1,
			Argc:   navCallArgc(n),
			Args:   navCallArgs(n, src, labelArgs),
		})
	})
	return out
}

// navCalleeName reduces a call_expression's callee operand (its first named
// child) to astkit's "Receiver.callee" convention.
func navCalleeName(callee *sitter.Node, src []byte) string {
	if callee == nil {
		return ""
	}
	switch callee.Type() {
	case "simple_identifier":
		return callee.Content(src)
	case "navigation_expression":
		if callee.NamedChildCount() < 2 {
			return ""
		}
		name := navSuffixName(callee.NamedChild(1), src)
		if name == "" {
			return ""
		}
		return joinQualified(navQualifierName(callee.NamedChild(0), src), name)
	}
	return ""
}

// navSuffixName returns the member name a navigation_suffix (".foo") names.
func navSuffixName(suffix *sitter.Node, src []byte) string {
	if suffix == nil || suffix.NamedChildCount() < 1 {
		return ""
	}
	c := suffix.NamedChild(0)
	if c == nil || c.Type() != "simple_identifier" {
		return ""
	}
	return c.Content(src)
}

// navQualifierName reduces a navigation_expression's target (receiver) to
// one identifier, mirroring qualifierName's contract for other grammars.
// self/this stay literal (matching how csCallSites keeps "this"/"base"
// explicit) so Grove's implicit-self resolution sees the same "self.x"/
// "this.x" shape it already special-cases for Java/C#/C++.
func navQualifierName(target *sitter.Node, src []byte) string {
	if target == nil {
		return ""
	}
	switch target.Type() {
	case "simple_identifier":
		return target.Content(src)
	case "self_expression":
		return "self"
	case "this_expression":
		return "this"
	case "navigation_expression":
		if target.NamedChildCount() < 2 {
			return ""
		}
		return navSuffixName(target.NamedChild(1), src)
	case "call_expression":
		if target.NamedChildCount() < 1 {
			return ""
		}
		name := navCalleeName(target.NamedChild(0), src)
		if name == "" {
			return ""
		}
		// A call-result receiver is named by the call alone
		// (`a.b(x).c()` → receiver "b()"), the convention Grove's
		// call-result typing reads for every language; carrying the
		// inner chain would make the qualifier unparseable.
		return name[strings.LastIndexByte(name, '.')+1:] + "()"
	}
	return ""
}

// navInOperatorSite turns a Kotlin check_expression carrying `in`/`!in`
// (not `is`) into the `rhs.contains(lhs)` call it denotes.
func navInOperatorSite(n *sitter.Node, src []byte, labelArgs bool) (astkit.CallSite, bool) {
	isIn := false
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c != nil && (c.Type() == "in" || c.Type() == "!in") {
			isIn = true
		}
	}
	if !isIn || n.NamedChildCount() < 2 {
		return astkit.CallSite{}, false
	}
	qual := navQualifierName(n.NamedChild(1), src)
	if qual == "" {
		return astkit.CallSite{}, false
	}
	arg := argToken(n.NamedChild(0), src)
	if labelArgs {
		arg = "_:" + arg
	}
	return astkit.CallSite{
		Callee: joinQualified(qual, "contains"),
		Line:   int(n.StartPoint().Row) + 1,
		Argc:   1,
		Args:   []string{arg},
	}, true
}

// kotlinBinaryOperators maps Kotlin's overloadable arithmetic operators to
// the operator functions they invoke.
var kotlinBinaryOperators = map[string]string{
	"+": "plus", "-": "minus", "*": "times", "/": "div", "%": "rem",
}

// navBinaryOperatorSite turns `lhs <op> rhs` into the `lhs.<opfun>(rhs)`
// call it denotes. A literal or nested-expression left operand has no
// qualifier to resolve against and yields nothing.
func navBinaryOperatorSite(n *sitter.Node, src []byte) (astkit.CallSite, bool) {
	if n.ChildCount() != 3 || n.NamedChildCount() != 2 {
		return astkit.CallSite{}, false
	}
	op := kotlinBinaryOperators[n.Child(1).Type()]
	if op == "" {
		return astkit.CallSite{}, false
	}
	qual := navQualifierName(n.NamedChild(0), src)
	if qual == "" {
		return astkit.CallSite{}, false
	}
	return astkit.CallSite{
		Callee: joinQualified(qual, op),
		Line:   int(n.StartPoint().Row) + 1,
		Argc:   1,
		Args:   []string{argToken(n.NamedChild(1), src)},
	}, true
}

// navValueArgumentCount counts the value_argument children of a
// value_arguments node (nil-safe).
func navValueArgumentCount(args *sitter.Node) int {
	if args == nil {
		return 0
	}
	n := 0
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if c := args.NamedChild(i); c != nil && c.Type() == "value_argument" {
			n++
		}
	}
	return n
}

// navIsSubscript reports whether a call_expression is a subscript access
// (`x[i]`): its argument list is bracketed rather than parenthesized.
func navIsSubscript(call *sitter.Node, src []byte) bool {
	args := navCallArguments(call)
	return args != nil && strings.HasPrefix(args.Content(src), "[")
}

// navCallArguments finds a call_expression's value_arguments node: the
// grammar nests it inside a positional call_suffix child, not a field.
func navCallArguments(call *sitter.Node) *sitter.Node {
	for i := 0; i < int(call.ChildCount()); i++ {
		c := call.Child(i)
		if c != nil && c.Type() == "call_suffix" {
			return internalast.FindChildByType(c, "value_arguments")
		}
	}
	return nil
}

func navCallArgc(call *sitter.Node) int {
	args := navCallArguments(call)
	if args == nil {
		return 0
	}
	n := 0
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if c := args.NamedChild(i); c != nil && c.Type() == "value_argument" {
			n++
		}
	}
	return n
}

func navCallArgs(call *sitter.Node, src []byte, labelArgs bool) []string {
	args := navCallArguments(call)
	if args == nil {
		return nil
	}
	var out []string
	any := false
	for i := 0; i < int(args.NamedChildCount()); i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Type() != "value_argument" {
			continue
		}
		v := ""
		if nc := int(arg.NamedChildCount()); nc > 0 {
			v = argToken(arg.NamedChild(nc-1), src)
		}
		if labelArgs {
			// The label is a value_argument_label child; its absence is an
			// unlabeled (`_`) argument, which is itself a fact the callee's
			// declaration must agree with.
			label := "_"
			if l := internalast.FindChildByType(arg, "value_argument_label"); l != nil {
				label = strings.TrimSpace(l.Content(src))
			}
			v = label + ":" + v
			any = true
		} else if v != "" {
			any = true
		}
		out = append(out, v)
	}
	if !any {
		return nil
	}
	return out
}

// ─── Objective-C message sends ─────────────────────────────────────────────
//
// Objective-C calls ("message sends") have no analog anywhere else astkit
// supports: `[receiver keyword1:arg1 keyword2:arg2]` names its callee across
// N "method"-fielded identifier children (one per keyword), not one. The
// selector is their colon-joined concatenation ("keyword1:keyword2:"), and
// it is this joined form — not any single keyword — that both a method
// declaration/definition and every call to it are named by, so definitions
// and call sites agree on one string to match against.

// objcJoinSelectorParts joins keyword-message parts ("doThing", "withOption")
// into astkit/Grove's one agreed-on selector string ("doThing:withOption:"),
// or returns the lone part bare when the message/method is unary (no ":").
func objcJoinSelectorParts(parts []string, hasColon bool) string {
	if len(parts) == 0 {
		return ""
	}
	if !hasColon {
		return parts[0]
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p)
		b.WriteByte(':')
	}
	return b.String()
}

// objcCallSelector reconstructs the full selector from a message_expression
// (a call): its keyword parts are the "method"-fielded identifier children.
func objcCallSelector(n *sitter.Node, src []byte) string {
	var parts []string
	hasColon := false
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if n.FieldNameForChild(i) == "method" {
			parts = append(parts, c.Content(src))
		}
		if c.Type() == ":" {
			hasColon = true
		}
	}
	return objcJoinSelectorParts(parts, hasColon)
}

// objcDeclSelector reconstructs the full selector from a
// method_declaration/method_definition (a declaration). Unlike
// message_expression, this grammar attaches no field name to a
// declaration's keyword-part identifiers — they are its only direct
// "identifier"-typed children (the return type's identifier is nested
// inside a method_type/type_name wrapper, a different node type, so there
// is no ambiguity to filter out) — and unlike message_expression, the ":"
// itself is NOT a direct child here: it is nested one level down inside
// each method_parameter. A keyword method is therefore recognized by the
// presence of at least one method_parameter child, not by a direct ":".
func objcDeclSelector(n *sitter.Node, src []byte) string {
	var parts []string
	hasParam := false
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "identifier":
			parts = append(parts, c.Content(src))
		case "method_parameter":
			hasParam = true
		}
	}
	return objcJoinSelectorParts(parts, hasParam)
}

// objcIsClassMethod reports whether a method_declaration/method_definition
// is declared with a leading `+` (a class/factory method) rather than `-`
// (an instance method).
func objcIsClassMethod(n *sitter.Node) bool {
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c != nil && c.Type() == "+" {
			return true
		}
	}
	return false
}

// objcMessageArgs returns a message_expression's argument-expression nodes:
// the grammar places each one as the sibling immediately after a ":" token,
// with no field name of its own.
func objcMessageArgs(n *sitter.Node) []*sitter.Node {
	var args []*sitter.Node
	for i := 0; i < int(n.ChildCount())-1; i++ {
		if c := n.Child(i); c != nil && c.Type() == ":" {
			if arg := n.Child(i + 1); arg != nil {
				args = append(args, arg)
			}
		}
	}
	return args
}

// objcCallSites walks body for message_expression nodes and returns one
// CallSite per send, receiver-qualified in astkit's usual "Receiver.callee"
// form. self/super stay literal, matching how every other language keeps
// its own receiver keyword explicit in the Callee (rustCallSites' "self",
// csCallSites' "this"/"base").
func objcCallSites(body *sitter.Node, src []byte) []astkit.CallSite {
	if body == nil {
		return nil
	}
	var out []astkit.CallSite
	internalast.WalkTree(body, func(n *sitter.Node) {
		if n == nil || n.Type() != "message_expression" {
			return
		}
		sel := objcCallSelector(n, src)
		if sel == "" {
			return
		}
		qual := objcReceiverQualifier(n.ChildByFieldName("receiver"), src)
		args := objcMessageArgs(n)
		var argToks []string
		any := false
		for _, a := range args {
			v := argToken(a, src)
			if v != "" {
				any = true
			}
			argToks = append(argToks, v)
		}
		if !any {
			argToks = nil
		}
		out = append(out, astkit.CallSite{
			Callee: joinQualified(qual, sel),
			Line:   int(n.StartPoint().Row) + 1,
			Argc:   len(args),
			Args:   argToks,
		})
	})
	return out
}

// objcReceiverQualifier reduces a message send's receiver to one identifier:
// a bare name (self, super, a local variable, or a class name) stays
// literal; a nested message send (`[[Person alloc] init]`) reduces to its
// own selector plus the "name()" call-result marker used elsewhere
// (qualifierName's contract for other grammars).
func objcReceiverQualifier(recv *sitter.Node, src []byte) string {
	if recv == nil {
		return ""
	}
	switch recv.Type() {
	case "identifier":
		return recv.Content(src)
	case "message_expression":
		sel := objcCallSelector(recv, src)
		if sel == "" {
			return ""
		}
		// `[[Type alloc] init]`, `[[self alloc] initWith...]`, `[Type new]`:
		// the inner send yields an instance of its receiver's class, so
		// the outer send's receiver is that class — written `Type()`,
		// the call-result form Grove types by name.
		if objcSelectorReturnsReceiver(sel) && recv.NamedChildCount() > 0 {
			if inner := recv.NamedChild(0); inner != nil {
				switch inner.Type() {
				case "identifier":
					return inner.Content(src) + "()"
				case "message_expression":
					if q := objcReceiverQualifier(inner, src); strings.HasSuffix(q, "()") {
						return q
					}
				}
			}
		}
		return sel + "()"
	case "field_expression":
		// `[self.delegate foo]`: the receiver is the property, whose
		// declared type the enclosing class's @interface carries.
		if recv.NamedChildCount() >= 2 {
			if base := recv.NamedChild(0); base != nil && base.Type() == "identifier" && base.Content(src) == "self" {
				if field := recv.NamedChild(1); field != nil && field.Type() == "field_identifier" {
					return field.Content(src)
				}
			}
		}
	}
	return ""
}

// objcSelectorReturnsReceiver reports whether a selector, by Cocoa
// convention, returns an instance of the receiver's own class: alloc, new,
// init and initWith..., copy/mutableCopy, self, class.
func objcSelectorReturnsReceiver(sel string) bool {
	switch sel {
	case "alloc", "new", "init", "copy", "mutableCopy", "self", "class", "sharedInstance":
		return true
	}
	return strings.HasPrefix(sel, "initWith") || strings.HasPrefix(sel, "init:")
}

// ─── Modifiers ───────────────────────────────────────────────────────────────

// jsModifiers extracts TS/JS modifier tokens (accessibility, static, async,
// readonly, abstract, override, declare). Recurses into the node's children
// looking for nodes whose type matches a known modifier keyword.
func jsModifiers(n *sitter.Node, src []byte) []string {
	wanted := map[string]bool{
		"public": true, "private": true, "protected": true,
		"static": true, "readonly": true, "abstract": true,
		"async": true, "override": true, "declare": true,
		"accessibility_modifier": true,
	}
	var out []string
	seen := map[string]bool{}
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if wanted[c.Type()] {
			text := strings.TrimSpace(c.Content(src))
			if text == "" {
				continue
			}
			if !seen[text] {
				seen[text] = true
				out = append(out, text)
			}
		}
	}
	return out
}

// javaModifiers walks the "modifiers" child node and returns each token.
// Annotations (children of type "marker_annotation" / "annotation") are
// emitted via javaAnnotations and not duplicated here.
func javaModifiers(n *sitter.Node, src []byte) []string {
	mods := findChildByType(n, "modifiers")
	if mods == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(mods.ChildCount()); i++ {
		c := mods.Child(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "marker_annotation", "annotation":
			continue
		default:
			text := strings.TrimSpace(c.Content(src))
			if text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

// rustModifiers returns the visibility token(s) for a Rust item.
// Most Rust items expose a "visibility_modifier" node: pub, pub(crate), pub(super), pub(in path).
func rustModifiers(n *sitter.Node, src []byte) []string {
	vis := findChildByType(n, "visibility_modifier")
	if vis == nil {
		return nil
	}
	return []string{strings.TrimSpace(vis.Content(src))}
}

// pythonModifiers maps Python visibility convention to a single modifier.
// Functions/methods starting with `__` are considered private,
// those starting with `_` are protected, others are public.
func pythonModifiers(name string) []string {
	switch {
	case strings.HasPrefix(name, "__") && !strings.HasSuffix(name, "__"):
		return []string{"private"}
	case strings.HasPrefix(name, "_"):
		return []string{"protected"}
	default:
		return []string{"public"}
	}
}

// ─── Type parameters (generics) ──────────────────────────────────────────────

// jsTypeParameters extracts TypeScript generic parameter names from a
// "type_parameters" child node: `<T extends Foo, U>` → ["T", "U"].
func jsTypeParameters(n *sitter.Node, src []byte) []string {
	tp := n.ChildByFieldName("type_parameters")
	if tp == nil {
		tp = findChildByType(n, "type_parameters")
	}
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.ChildCount()); i++ {
		c := tp.Child(i)
		if c == nil || c.Type() != "type_parameter" {
			continue
		}
		name := c.ChildByFieldName("name")
		if name == nil {
			// First identifier child is conventionally the parameter name.
			for j := 0; j < int(c.ChildCount()); j++ {
				if cc := c.Child(j); cc != nil && cc.Type() == "type_identifier" {
					name = cc
					break
				}
			}
		}
		if name != nil {
			out = append(out, strings.TrimSpace(name.Content(src)))
		}
	}
	return out
}

// javaTypeParameters extracts Java generic parameter names from a
// "type_parameters" child node.
func javaTypeParameters(n *sitter.Node, src []byte) []string {
	tp := findChildByType(n, "type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.ChildCount()); i++ {
		c := tp.Child(i)
		if c == nil || c.Type() != "type_parameter" {
			continue
		}
		for j := 0; j < int(c.ChildCount()); j++ {
			if cc := c.Child(j); cc != nil && cc.Type() == "type_identifier" {
				out = append(out, cc.Content(src))
				break
			}
		}
	}
	return out
}

// rustTypeParameters returns the names from a Rust "type_parameters" node.
func rustTypeParameters(n *sitter.Node, src []byte) []string {
	tp := findChildByType(n, "type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.ChildCount()); i++ {
		c := tp.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "type_identifier" || c.Type() == "lifetime" || c.Type() == "constrained_type_parameter" {
			text := strings.TrimSpace(c.Content(src))
			if text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

// goTypeParameters returns the names of Go type parameters from a
// "type_parameter_list" child of a function/method/type declaration.
func goTypeParameters(n *sitter.Node, src []byte) []string {
	tp := findChildByType(n, "type_parameter_list")
	if tp == nil {
		// Inside type_declaration → type_spec → type_parameter_list
		for i := 0; i < int(n.ChildCount()); i++ {
			c := n.Child(i)
			if c != nil && (c.Type() == "type_spec" || c.Type() == "alias_declaration") {
				if inner := findChildByType(c, "type_parameter_list"); inner != nil {
					tp = inner
					break
				}
			}
		}
	}
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.ChildCount()); i++ {
		c := tp.Child(i)
		if c == nil || c.Type() != "type_parameter_declaration" {
			continue
		}
		for j := 0; j < int(c.ChildCount()); j++ {
			if cc := c.Child(j); cc != nil && (cc.Type() == "identifier" || cc.Type() == "type_identifier") {
				out = append(out, cc.Content(src))
			}
		}
	}
	return out
}

// ─── Annotations / decorators ────────────────────────────────────────────────

// pythonDecorators returns the decorator names attached to a function or
// class via a parent "decorated_definition" node.
func pythonDecorators(decoratedDef *sitter.Node, src []byte) []string {
	if decoratedDef == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(decoratedDef.ChildCount()); i++ {
		c := decoratedDef.Child(i)
		if c == nil || c.Type() != "decorator" {
			continue
		}
		// Skip the leading "@".
		text := strings.TrimSpace(c.Content(src))
		text = strings.TrimPrefix(text, "@")
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

// jsDecorators returns the names of decorators applied to a TS class/method.
// Decorators are previous siblings of type "decorator".
func jsDecorators(n *sitter.Node, src []byte) []string {
	parent := n.Parent()
	if parent == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(parent.ChildCount()); i++ {
		c := parent.Child(i)
		if c == nil {
			continue
		}
		// Decorators precede the symbol node in the parent.
		if c.Type() != "decorator" {
			continue
		}
		if c.StartByte() >= n.StartByte() {
			break
		}
		text := strings.TrimSpace(c.Content(src))
		text = strings.TrimPrefix(text, "@")
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

// javaAnnotations extracts `@Annotation` tokens from a Java declaration's
// "modifiers" child.
func javaAnnotations(n *sitter.Node, src []byte) []string {
	mods := findChildByType(n, "modifiers")
	if mods == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(mods.ChildCount()); i++ {
		c := mods.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "marker_annotation" || c.Type() == "annotation" {
			text := strings.TrimSpace(c.Content(src))
			text = strings.TrimPrefix(text, "@")
			if text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

// rustAttributes returns the `#[...]` attribute macros attached to a Rust
// item. In tree-sitter-rust, outer attributes appear as previous-sibling
// `attribute_item` nodes; inner attributes appear as child `inner_attribute_item`
// nodes. We collect both.
func rustAttributes(n *sitter.Node, src []byte) []string {
	var out []string
	clean := func(text string) string {
		text = strings.TrimSpace(text)
		text = strings.TrimPrefix(text, "#")
		text = strings.TrimPrefix(text, "!")
		text = strings.TrimPrefix(text, "[")
		text = strings.TrimSuffix(text, "]")
		return strings.TrimSpace(text)
	}
	// Outer attributes — previous siblings of types attribute_item.
	for prev := n.PrevSibling(); prev != nil; prev = prev.PrevSibling() {
		if prev.Type() != "attribute_item" {
			break
		}
		if text := clean(prev.Content(src)); text != "" {
			out = append([]string{text}, out...)
		}
	}
	// Inner attributes — direct children of the item itself.
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "attribute_item" || c.Type() == "inner_attribute_item" {
			if text := clean(c.Content(src)); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

// ─── Utilities ───────────────────────────────────────────────────────────────

func findChildByType(n *sitter.Node, typeName string) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c != nil && c.Type() == typeName {
			return c
		}
	}
	return nil
}
