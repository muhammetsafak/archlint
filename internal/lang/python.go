package lang

import (
	"path"
	"regexp"
	"strings"

	"github.com/muhammetsafak/archlint/internal/config"
)

// pyLang handles Python. Like the TS scanner it uses regexes (zero dependencies), reading
// the `import a.b.c` and `from a.b import …` forms — including relative imports
// (`from . import x`, `from ..pkg import y`). It is not a parser, so an import-like line
// inside a triple-quoted string can be a false positive.
type pyLang struct{}

func (pyLang) Name() string { return "python" }

var (
	// `from <module> import …` — captures the dotted module, including leading dots.
	pyFrom = regexp.MustCompile(`(?m)^[ \t]*from[ \t]+(\.*[\w.]*)[ \t]+import\b`)
	// `import a.b, c.d as e` — captures the comma-separated module list (sans comment).
	pyImport = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+([^\n#]+)`)
)

func (pyLang) Imports(_ string, src []byte) ([]Import, error) {
	var out []Import

	for _, m := range pyFrom.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		if mod == "" {
			continue
		}
		out = append(out, Import{Raw: mod, Line: lineAt(src, m[2])})
	}

	for _, m := range pyImport.FindAllSubmatchIndex(src, -1) {
		line := lineAt(src, m[2])
		for _, part := range strings.Split(string(src[m[2]:m[3]]), ",") {
			mod := strings.TrimSpace(part)
			if i := strings.Index(mod, " as "); i >= 0 {
				mod = mod[:i]
			}
			fields := strings.Fields(mod)
			if len(fields) == 0 {
				continue
			}
			out = append(out, Import{Raw: fields[0], Line: line})
		}
	}
	return out, nil
}

// Resolve maps a module to a repo-relative path. Relative imports (leading dots) resolve
// against the importing file's package; absolute imports map dotted→slash, with an optional
// alias prefix for src-layout roots. External/stdlib modules resolve to a path that simply
// matches no layer.
func (pyLang) Resolve(raw, fileRel string, cfg *config.Config) string {
	if strings.HasPrefix(raw, ".") {
		n := 0
		for n < len(raw) && raw[n] == '.' {
			n++
		}
		base := path.Dir(toSlash(fileRel))
		for i := 1; i < n; i++ { // 1 dot = current package; each extra dot goes up one
			base = path.Dir(base)
		}
		rest := raw[n:]
		if rest == "" {
			return clean(base)
		}
		return clean(path.Join(base, strings.ReplaceAll(rest, ".", "/")))
	}

	slashed := strings.ReplaceAll(raw, ".", "/")
	if target, ok := applyAlias(slashed, cfg); ok {
		return clean(target)
	}
	return clean(slashed)
}
