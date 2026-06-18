package lang

import (
	"path"
	"regexp"
	"strings"

	"github.com/muhammetsafak/archlint/internal/config"
)

// tsLang handles TypeScript/JavaScript. Imports are extracted with regexes rather than a
// full parser (zero dependencies, in keeping with the rest of archlint). It recognises the
// standard static forms — `import … from '…'`, `export … from '…'`, bare `import '…'`,
// `require('…')`, and dynamic `import('…')`. It does NOT parse the language, so an
// import-like string inside a comment or string literal can be a false positive.
type tsLang struct{}

func (tsLang) Name() string { return "typescript" }

var (
	tsFrom    = regexp.MustCompile(`(?:^|[^.\w])(?:import|export)\b[^'"]*?\bfrom\s*['"]([^'"]+)['"]`)
	tsBare    = regexp.MustCompile(`(?:^|[^.\w])import\s+['"]([^'"]+)['"]`)
	tsRequire = regexp.MustCompile(`(?:^|[^.\w])require\s*\(\s*['"]([^'"]+)['"]\s*\)`)
	tsDynamic = regexp.MustCompile(`(?:^|[^.\w])import\s*\(\s*['"]([^'"]+)['"]\s*\)`)
)

func (tsLang) Imports(_ string, src []byte) ([]Import, error) {
	var out []Import
	for _, re := range []*regexp.Regexp{tsFrom, tsBare, tsRequire, tsDynamic} {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			out = append(out, Import{Raw: string(src[m[2]:m[3]]), Line: lineAt(src, m[2])})
		}
	}
	return out, nil
}

// Resolve handles relative specifiers (against the importing file) and configured path
// aliases; bare specifiers (npm packages) resolve to "".
func (tsLang) Resolve(raw, fileRel string, cfg *config.Config) string {
	if strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") {
		return clean(path.Join(path.Dir(toSlash(fileRel)), raw))
	}
	if target, ok := applyAlias(raw, cfg); ok {
		return clean(target)
	}
	return "" // bare / external
}
