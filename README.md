# astkit

Shared code-intelligence layer for the Provasign toolchain: language
detection, tree-sitter grammar dispatch, and per-language symbol/call-site
extraction behind one persistence-agnostic API.

Consumers ([Grove](https://github.com/provasign/grove),
[Fuse](https://github.com/provasign/fuse),
[Prism](https://github.com/provasign/prism)) import astkit and project its
`Symbol` type down to their own storage models — they do not depend on each
other for parsing.

## What it owns

- **Language identification** — `LanguageKey` + file-extension detection
  (`DetectLanguage`).
- **Tree-sitter dispatch** — `Engine.Parse` / `Engine.Validate`, one place
  for every supported grammar, with a parse timeout.
- **Symbol extraction** — a `Registry` of per-language `Strategy`
  implementations emitting `Symbol` (with spans, signatures, receivers,
  call sites) and `ImportStatement`.

## Supported languages

| AST extraction | Detection only (no symbols) |
|---|---|
| Go, Python, Java, Rust, JavaScript, TypeScript, TSX, C, C++, C#, PHP, Swift, Kotlin, Objective-C, COBOL, JCL | JSON, YAML, TOML |

Swift, Kotlin and Objective-C (v0.13.0+) extract classes, structs, enums,
protocols/interfaces, extensions, categories, methods, constructors (named
after their type, like Java/C#), properties and instance variables, with
receiver-qualified call sites. Kotlin desugars `a in b` → `b.contains(a)`,
`a + b` → `a.plus(b)`, and constructor delegation; Swift records argument
labels (`label:value`) so overloads are told apart; Objective-C joins
selectors (`doThing:withOption:`) and types `[[Type alloc] init]` chains
as `Type()`. Grove measures all three against compiler oracles.

## Usage

```go
import "github.com/provasign/astkit"
import "github.com/provasign/astkit/strategies"

lang := astkit.DetectLanguage(path, "")          // e.g. astkit.LangGo
engine := astkit.NewEngine()
tree, err := engine.Parse(ctx, lang, src)

reg := strategies.Default()
symbols, err := reg.Extract(lang, tree, src)     // []astkit.Symbol
imports, err := reg.ExtractImports(lang, tree, src)
```

Each `Symbol` carries its kind (function, method, class, interface, …),
line spans, declaration signature, receiver/container, and resolved-name
`CallSite` records — everything a caller needs to build reference graphs
or structural diffs without touching tree-sitter directly.

## Development

```sh
go build ./...
go test ./...
```

Grammar bindings come from `github.com/smacker/go-tree-sitter`, except
Objective-C, whose grammar (tree-sitter-grammars/tree-sitter-objc, MIT) is
vendored under `thirdparty/tsobjc` because the go-tree-sitter fork does not
ship it. Adding a language means registering its grammar in `engine.go` and
providing a `Strategy` under `strategies/`. Strategies are hand-written tree
walks over node types (no `.scm` queries); the Swift and Kotlin grammars are
positional (Kotlin has no field names at all), so their walkers index
children by position.

## License

MIT
