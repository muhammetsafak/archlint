// Package arch is the linter: it walks a codebase, maps each source file and each of its
// imports to a declared layer, and reports imports that cross a boundary the
// architecture.json forbids. Per-language import handling lives in internal/lang, so arch
// itself is language-agnostic — it deals only in repo-relative paths and layers.
package arch

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/muhammetsafak/archlint/internal/config"
	"github.com/muhammetsafak/archlint/internal/lang"
)

// Violation is one import that breaks a declared boundary.
type Violation struct {
	File      string // repo-relative path of the importing file
	Line      int
	FromLayer string
	ToLayer   string
	Import    string // the offending import specifier
	// Source names where the broken rule was declared: "" for architecture.json, or the
	// repo-relative path of the ADR file that forbade the edge (executable ADR). It is
	// purely for traceability in the report — it does not affect whether an edge violates.
	Source string
}

// RuleSource attributes a forbidden from -> to edge to the ADR file that declared it (or ""
// when the rule came from architecture.json). internal/adr.Ruleset satisfies it; a nil
// RuleSource means "no ADR rules", leaving every violation attributed to the json config.
type RuleSource interface {
	SourceOf(from, to string) string
}

var ignoredDirs = map[string]struct{}{
	"vendor": {}, ".git": {}, "node_modules": {}, "testdata": {},
}

// Lint scans root and returns the boundary violations, sorted by file then line. src may be
// nil; when non-nil it attributes each violation to the ADR file that declared the broken
// rule (see internal/adr), leaving json-sourced rules with an empty Source.
func Lint(root string, cfg *config.Config, ruleSrc RuleSource) ([]Violation, error) {
	var violations []Violation

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, skip := ignoredDirs[d.Name()]; skip && p != root {
				return fs.SkipDir
			}
			return nil
		}

		language := lang.For(p)
		if language == nil {
			return nil // not a source file we understand
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		fromLayer := cfg.LayerOf(rel)
		if fromLayer == "" {
			return nil // the file is in no governed layer
		}

		src, err := os.ReadFile(p)
		if err != nil {
			return nil // unreadable file — skip rather than fail the whole lint
		}
		imports, perr := language.Imports(p, src)
		if perr != nil {
			return nil // unparseable file — skip it
		}
		for _, imp := range imports {
			target := language.Resolve(imp.Raw, rel, cfg)
			if target == "" {
				continue // external / unresolvable — not a governed layer
			}
			toLayer := cfg.LayerOf(target)
			if toLayer == "" || cfg.Allows(fromLayer, toLayer) {
				continue
			}
			source := ""
			if ruleSrc != nil {
				source = ruleSrc.SourceOf(fromLayer, toLayer)
			}
			violations = append(violations, Violation{
				File:      rel,
				Line:      imp.Line,
				FromLayer: fromLayer,
				ToLayer:   toLayer,
				Import:    imp.Raw,
				Source:    source,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		return violations[i].Line < violations[j].Line
	})
	return violations, nil
}
