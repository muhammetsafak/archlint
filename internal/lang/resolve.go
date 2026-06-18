package lang

import (
	"bytes"
	"path"
	"sort"
	"strings"

	"github.com/muhammetsafak/archlint/internal/config"
)

// lineAt returns the 1-based line number of a byte offset in src.
func lineAt(src []byte, off int) int {
	return 1 + bytes.Count(src[:off], []byte("\n"))
}

// toSlash normalises OS separators (callers pass slash-formed paths, but guard against
// Windows input).
func toSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// clean normalises a path and rejects anything that escapes the repo root.
func clean(p string) string {
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return ""
	}
	return c
}

// applyAlias substitutes the longest matching alias prefix in a slash-formed path. ok is
// false when no alias matches. Used for TS/JS path aliases and Python src-layout roots.
func applyAlias(slashed string, cfg *config.Config) (string, bool) {
	keys := make([]string, 0, len(cfg.Aliases))
	for a := range cfg.Aliases {
		keys = append(keys, a)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })

	for _, alias := range keys {
		if slashed == strings.TrimSuffix(alias, "/") || strings.HasPrefix(slashed, alias) {
			rest := strings.TrimPrefix(strings.TrimPrefix(slashed, alias), "/")
			return path.Join(strings.TrimSuffix(cfg.Aliases[alias], "/"), rest), true
		}
	}
	return "", false
}
