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
| Go, Python, Java, Rust, JavaScript, TypeScript, TSX, C, C++, C#, PHP | JSON, YAML, TOML |

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

Grammar bindings come from `github.com/smacker/go-tree-sitter`. Adding a
language means registering its grammar in `engine.go` and providing a
`Strategy` under `strategies/`.

## License

MIT
