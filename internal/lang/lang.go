// Package lang abstracts per-language import handling so the architecture linter stays
// language-agnostic. Each Language knows how to (1) extract a source file's imports and
// (2) resolve an import specifier to a repo-relative path (or "" when it points outside the
// project). The linter itself only deals in repo-relative paths and layers.
package lang

import (
	"path/filepath"

	"github.com/muhammetsafak/archlint/internal/config"
)

// Import is one import found in a source file.
type Import struct {
	Raw  string // the specifier as written ("github.com/x/y" for Go, "../db" for TS)
	Line int
}

// Language handles one family of source files.
type Language interface {
	Name() string
	// Imports extracts the imports from a file's source.
	Imports(path string, src []byte) ([]Import, error)
	// Resolve turns a raw specifier (found in the file at repo-relative fileRel) into a
	// repo-relative path, or "" when it is external / unresolvable.
	Resolve(raw, fileRel string, cfg *config.Config) string
}

var byExt = map[string]Language{}

func register(l Language, exts ...string) {
	for _, e := range exts {
		byExt[e] = l
	}
}

// For returns the Language registered for a file path's extension, or nil if unsupported.
func For(path string) Language {
	return byExt[filepath.Ext(path)]
}

func init() {
	register(goLang{}, ".go")
	register(tsLang{}, ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs")
	register(pyLang{}, ".py", ".pyi")
}
