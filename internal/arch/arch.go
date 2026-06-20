// SPDX-License-Identifier: MIT

// Package arch is the linter: it walks a codebase, maps each source file and each of its
// imports to a declared layer, and reports imports that cross a boundary the
// architecture.json forbids. Per-language import handling lives in internal/lang, so arch
// itself is language-agnostic — it deals only in repo-relative paths and layers.
//
// The single tree-walk that powers the linter also yields the inter-layer dependency graph
// (see BuildGraph and Edge), which the metrics analysis (internal/metrics) consumes to compute
// coupling, bounded-context, and Conway signals on the same edges the linter checks.
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

// ImportEdge is one resolved internal import: a single source file importing another file that
// archlint mapped to a (possibly the same) layer. It is the atom the layer graph is built from.
type ImportEdge struct {
	File      string // repo-relative path of the importing file
	Line      int
	FromLayer string
	ToLayer   string
	Import    string // the import specifier as written
}

// Edge is the aggregate dependency between two distinct layers: every ImportEdge whose
// endpoints map to From and To. Same-layer imports are not Edges (a layer does not depend on
// itself), but they are still returned among Graph.Imports for completeness.
type Edge struct {
	From    string
	To      string
	Imports []ImportEdge // the concrete imports that realise this layer→layer dependency
}

// Graph is the inter-layer dependency graph extracted from one tree-walk: the declared layers,
// the distinct directed layer→layer edges (cross-layer only), and the flat list of every
// resolved internal import for callers that need file-level provenance.
type Graph struct {
	Layers  []string     // declared layer names, sorted
	Edges   []Edge       // distinct cross-layer edges, sorted by (From, To)
	Imports []ImportEdge // every resolved internal import, sorted by (File, Line)
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

// BuildGraph walks root once and returns the inter-layer dependency graph: every source file
// archlint understands is mapped to a layer, and each of its imports that resolves to a
// governed layer becomes an ImportEdge. Files in no governed layer, external/unresolvable
// imports, and unreadable/unparseable files are skipped (never fatal), exactly as Lint treats
// them — so the graph and the lint verdict are derived from the same edges.
func BuildGraph(root string, cfg *config.Config) (*Graph, error) {
	var imports []ImportEdge

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
			return nil // unreadable file — skip rather than fail the whole walk
		}
		parsed, perr := language.Imports(p, src)
		if perr != nil {
			return nil // unparseable file — skip it
		}
		for _, imp := range parsed {
			target := language.Resolve(imp.Raw, rel, cfg)
			if target == "" {
				continue // external / unresolvable — not a governed layer
			}
			toLayer := cfg.LayerOf(target)
			if toLayer == "" {
				continue // resolves inside the repo but into no governed layer
			}
			imports = append(imports, ImportEdge{
				File:      rel,
				Line:      imp.Line,
				FromLayer: fromLayer,
				ToLayer:   toLayer,
				Import:    imp.Raw,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(imports, func(i, j int) bool {
		if imports[i].File != imports[j].File {
			return imports[i].File < imports[j].File
		}
		return imports[i].Line < imports[j].Line
	})

	return &Graph{
		Layers:  sortedLayers(cfg),
		Edges:   aggregateEdges(imports),
		Imports: imports,
	}, nil
}

// sortedLayers returns the config's declared layer names in deterministic order.
func sortedLayers(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Layers))
	for name := range cfg.Layers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// aggregateEdges collapses the flat import list into distinct cross-layer edges, attaching the
// concrete imports that realise each one. Same-layer imports are excluded (a layer does not
// depend on itself).
func aggregateEdges(imports []ImportEdge) []Edge {
	idx := map[string]int{}
	var edges []Edge
	for _, ie := range imports {
		if ie.FromLayer == ie.ToLayer {
			continue
		}
		key := ie.FromLayer + "\x00" + ie.ToLayer
		i, ok := idx[key]
		if !ok {
			i = len(edges)
			idx[key] = i
			edges = append(edges, Edge{From: ie.FromLayer, To: ie.ToLayer})
		}
		edges[i].Imports = append(edges[i].Imports, ie)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return edges
}

// Lint scans root and returns the boundary violations, sorted by file then line. src may be
// nil; when non-nil it attributes each violation to the ADR file that declared the broken
// rule (see internal/adr), leaving json-sourced rules with an empty Source. It reuses the same
// graph walk as BuildGraph, then keeps the edges the config forbids.
func Lint(root string, cfg *config.Config, ruleSrc RuleSource) ([]Violation, error) {
	g, err := BuildGraph(root, cfg)
	if err != nil {
		return nil, err
	}

	var violations []Violation
	for _, ie := range g.Imports {
		if cfg.Allows(ie.FromLayer, ie.ToLayer) {
			continue
		}
		source := ""
		if ruleSrc != nil {
			source = ruleSrc.SourceOf(ie.FromLayer, ie.ToLayer)
		}
		violations = append(violations, Violation{
			File:      ie.File,
			Line:      ie.Line,
			FromLayer: ie.FromLayer,
			ToLayer:   ie.ToLayer,
			Import:    ie.Import,
			Source:    source,
		})
	}
	// g.Imports is already sorted by (File, Line); the filtered slice preserves that order.
	return violations, nil
}
