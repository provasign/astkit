package strategies

import "github.com/tabladrum/grove-suite/astkit"

// Default returns a Registry pre-populated with every strategy astkit ships.
// TS is registered both under "typescript" and "tsx"; JS under "javascript".
func Default() *astkit.Registry {
	r := astkit.NewRegistry()
	r.Register(NewGo())
	r.Register(NewPython())
	r.Register(NewJava())
	r.Register(NewRust())
	r.Register(NewJavaScript())
	ts := NewTypeScript(false)
	r.Register(ts)
	r.RegisterAs(astkit.LangTSX, NewTypeScript(true))
	return r
}
