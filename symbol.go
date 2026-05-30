package astkit

// SymbolKind enumerates the kinds of declarations astkit recognizes.
// New kinds may be added; consumers should treat unknown kinds as "other".
type SymbolKind string

const (
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindClass     SymbolKind = "class"
	KindInterface SymbolKind = "interface"
	KindStruct    SymbolKind = "struct"
	KindEnum      SymbolKind = "enum"
	KindTrait     SymbolKind = "trait"
	KindType      SymbolKind = "type"
	KindConst     SymbolKind = "const"
	KindVar       SymbolKind = "variable"
	KindField     SymbolKind = "field"
	KindOther     SymbolKind = "other"
)

// LineRange is a 1-indexed inclusive line span within a source file.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Symbol is the canonical, persistence-agnostic representation of a single
// declaration extracted from source. It is intentionally a superset of what
// any single consumer needs:
//
//   - Grove projects Symbol → SymbolRecord (adding ID/BlobSHA/FilePath,
//     usually dropping Body to control storage size).
//   - Fuse projects Symbol → SymbolData (deriving a per-file Key from
//     QualifiedName, preserving Body for merge reconstruction).
//   - Prism and Relay can consume either projection via Grove's APIs.
//
// The zero value is not a valid symbol; producers must at minimum set Kind
// and Name.
type Symbol struct {
	// Kind classifies the declaration (function, class, etc.).
	Kind SymbolKind `json:"kind"`

	// Name is the bare identifier (e.g. "Bar" for method Foo.Bar).
	Name string `json:"name"`

	// QualifiedName disambiguates within a file (e.g. "Foo.Bar" for methods
	// of class Foo). For top-level symbols it equals Name.
	QualifiedName string `json:"qualifiedName"`

	// ParentName is the immediate enclosing type/class name, if any.
	ParentName string `json:"parentName,omitempty"`

	// Signature is the declaration text without a body (best-effort).
	Signature string `json:"signature"`

	// Docstring is the language-idiomatic documentation comment immediately
	// preceding the declaration, if any.
	Docstring string `json:"docstring,omitempty"`

	// Body is the full source text covering the declaration including its
	// body. Required by consumers that need to reconstruct merged source
	// (Fuse). Grove may drop this before persisting.
	Body string `json:"body,omitempty"`

	// Span is the 1-indexed inclusive line range covered by Body.
	Span LineRange `json:"span"`

	// Modifiers lists language-idiomatic modifier keywords (public, static,
	// async, pub, etc.).
	Modifiers []string `json:"modifiers,omitempty"`

	// Exported reports whether the declaration is visible outside its
	// defining unit (file/module/package), per language semantics.
	Exported bool `json:"exported"`
}

// ImportStatement is one parsed import / use / require clause.
type ImportStatement struct {
	// Raw is the original source text of the clause.
	Raw string `json:"raw"`

	// Path is the imported module/package path.
	Path string `json:"path"`

	// Alias is the local binding name, if the import was aliased.
	Alias string `json:"alias,omitempty"`

	// Group classifies the import as stdlib | external | relative when known.
	Group string `json:"group,omitempty"`

	// Line is the 1-indexed source line of the clause.
	Line int `json:"line"`
}

// Export is a declared public surface entry. Some consumers compute this from
// Symbol.Exported; others need it explicit (e.g. `export {x}` re-exports in
// TS/JS).
type Export struct {
	Name string     `json:"name"`
	Kind SymbolKind `json:"kind"`
}
