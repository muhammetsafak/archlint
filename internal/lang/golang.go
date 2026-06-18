package lang

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/muhammetsafak/archlint/internal/config"
)

// goLang reads Go imports with the standard library's parser (exact, no compilation).
type goLang struct{}

func (goLang) Name() string { return "go" }

func (goLang) Imports(path string, src []byte) ([]Import, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []Import
	for _, imp := range f.Imports {
		p, uerr := strconv.Unquote(imp.Path.Value)
		if uerr != nil {
			continue
		}
		out = append(out, Import{Raw: p, Line: fset.Position(imp.Pos()).Line})
	}
	return out, nil
}

// Resolve strips the module prefix to get a repo-relative path; external imports (and the
// bare module root) resolve to "".
func (goLang) Resolve(raw, _ string, cfg *config.Config) string {
	if cfg.Module == "" || raw == cfg.Module {
		return ""
	}
	if strings.HasPrefix(raw, cfg.Module+"/") {
		return strings.TrimPrefix(raw, cfg.Module+"/")
	}
	return ""
}
