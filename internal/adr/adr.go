// SPDX-License-Identifier: MIT

// Package adr makes an Architecture Decision Record the enforced rule — an "executable ADR".
// It scans a directory of ADR markdown files for fenced ```archlint blocks, parses the
// boundary directives inside them (layer/allow/deny), and merges the result into the
// architecture.json config so an ADR-declared edge participates in linting exactly like a
// json rule — and a violation can be traced back to the ADR file that forbade it.
//
// Convention (one directive per line, inside a fenced ```archlint block):
//
//	# comments and blank lines are ignored
//	layer domain = internal/domain          # declare/extend a layer's path prefixes
//	layer db     = internal/db, internal/dao # comma-separated prefixes
//	allow db -> domain                       # db may import domain
//	deny  domain -> db                       # domain may NOT import db (explicit denial)
//
// The decision record literally carries the rule, so reviewing the ADR and enforcing it can
// no longer drift apart.
package adr

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muhammetsafak/archlint/internal/config"
)

// fenceLang is the info-string that opens an executable-ADR block: ```archlint.
const fenceLang = "archlint"

// Directive is one parsed line from an ADR's ```archlint block, tagged with where it came
// from so a merge conflict or violation can name the exact ADR file and line.
type Directive struct {
	File string // repo-relative path of the ADR file
	Line int    // 1-based line within the ADR file
	Kind Kind   // layer, allow, or deny

	// For Kind==KindLayer: the layer name and its path prefixes.
	Layer    string
	Prefixes []string

	// For Kind==KindAllow / KindDeny: the directed edge from -> to.
	From string
	To   string
}

// Kind is the type of an ADR directive.
type Kind int

const (
	// KindLayer declares (or extends) a layer's repo-relative path prefixes.
	KindLayer Kind = iota
	// KindAllow permits a from -> to import edge.
	KindAllow
	// KindDeny forbids a from -> to import edge (the default, made explicit).
	KindDeny
)

// Ruleset is everything parsed out of an ADR directory: the directives (in file/line order)
// and an index from an allowed/denied edge to the ADR location that declared it. The edge
// index powers both conflict detection at merge time and violation attribution at lint time.
type Ruleset struct {
	Directives []Directive
	// allows / denies map "from\x00to" → the ADR Directive that declared that edge.
	allows map[string]Directive
	denies map[string]Directive
}

func edgeKey(from, to string) string { return from + "\x00" + to }

// Scan walks dir, reads every markdown file, and parses the ```archlint blocks it finds. ADR
// file paths in the result are reported relative to root (the scanned project dir) so a
// violation's source reads like every other path archlint prints — repo-relative — falling
// back to a path relative to dir's parent, then the raw path. A missing dir is not an error:
// it yields an empty Ruleset, so pointing archlint at a repo without ADRs is a no-op.
func Scan(root, dir string) (*Ruleset, error) {
	rs := &Ruleset{
		allows: map[string]Directive{},
		denies: map[string]Directive{},
	}

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return rs, nil // no ADR directory → no ADR rules (backward compatible)
		}
		return nil, fmt.Errorf("adr: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("adr: %s is not a directory", dir)
	}

	var files []string
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(p)); ext == ".md" || ext == ".markdown" {
			files = append(files, p)
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("adr: scan %s: %w", dir, walkErr)
	}
	// Deterministic order: same directory, same merge/conflict verdict every run.
	sort.Strings(files)

	for _, p := range files {
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil, fmt.Errorf("adr: read %s: %w", p, rerr)
		}
		rel := relTo(root, dir, p)
		dirs, perr := parseFile(rel, src)
		if perr != nil {
			return nil, perr
		}
		for _, d := range dirs {
			if err := rs.add(d); err != nil {
				return nil, err
			}
		}
	}
	return rs, nil
}

// relTo reports p as a path the reader can locate: relative to the scanned project root when
// p lives under it (e.g. "docs/adr/0007-...md"), else relative to the ADR dir's parent, else
// the raw path. This keeps an ADR-sourced violation's attribution consistent with the
// repo-relative file paths archlint prints everywhere else.
func relTo(root, dir, p string) string {
	for _, base := range []string{root, filepath.Dir(filepath.Clean(dir))} {
		if base == "" {
			continue
		}
		if rel, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(p)
}

// add records a directive and, for edges, indexes it — rejecting a self-contradiction within
// the ADRs themselves (the same edge both allowed and denied).
func (rs *Ruleset) add(d Directive) error {
	rs.Directives = append(rs.Directives, d)
	switch d.Kind {
	case KindAllow:
		k := edgeKey(d.From, d.To)
		if prev, ok := rs.denies[k]; ok {
			return conflictErr(d, prev, "allowed here but denied")
		}
		rs.allows[k] = d
	case KindDeny:
		k := edgeKey(d.From, d.To)
		if prev, ok := rs.allows[k]; ok {
			return conflictErr(d, prev, "denied here but allowed")
		}
		rs.denies[k] = d
	}
	return nil
}

func conflictErr(a, b Directive, what string) error {
	return fmt.Errorf("adr: conflicting directives for %s -> %s: %s at %s:%d (see %s:%d)",
		a.From, a.To, what, a.File, a.Line, b.File, b.Line)
}

// parseFile extracts the directives from every ```archlint block in one markdown file.
func parseFile(rel string, src []byte) ([]Directive, error) {
	var out []Directive
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	inBlock := false
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		if !inBlock {
			if isFenceOpen(trimmed) {
				inBlock = true
			}
			continue
		}
		// inside an ```archlint block
		if isFenceClose(trimmed) {
			inBlock = false
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		d, err := parseDirective(rel, lineNo, trimmed)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("adr: read %s: %w", rel, err)
	}
	if inBlock {
		return nil, fmt.Errorf("adr: %s: unterminated ```%s block", rel, fenceLang)
	}
	return out, nil
}

// isFenceOpen reports whether a line opens an executable-ADR block: ```archlint (with any
// surrounding whitespace, and tolerating ~~~ fences and trailing block info).
func isFenceOpen(trimmed string) bool {
	body, ok := fenceBody(trimmed)
	if !ok {
		return false
	}
	// The first info-word must be exactly "archlint"; "archlint extra" still opens (the rest
	// of the info string is ignored), but "archlintx" does not.
	fields := strings.Fields(body)
	return len(fields) > 0 && fields[0] == fenceLang
}

// isFenceClose reports whether a line is a bare closing fence (no info string).
func isFenceClose(trimmed string) bool {
	body, ok := fenceBody(trimmed)
	return ok && strings.TrimSpace(body) == ""
}

// fenceBody returns the text after a ``` or ~~~ fence marker, or ok=false if the line is not
// a fence.
func fenceBody(trimmed string) (string, bool) {
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, marker) {
			return strings.TrimSpace(trimmed[len(marker):]), true
		}
	}
	return "", false
}

// parseDirective parses one non-comment line inside an ```archlint block.
func parseDirective(file string, line int, text string) (Directive, error) {
	// Strip an inline trailing comment ("allow a -> b  # note").
	if i := strings.Index(text, "#"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Directive{}, fmt.Errorf("adr: %s:%d: empty directive", file, line)
	}

	switch fields[0] {
	case "layer":
		return parseLayer(file, line, text)
	case "allow", "deny":
		return parseEdge(file, line, fields)
	default:
		return Directive{}, fmt.Errorf("adr: %s:%d: unknown directive %q (want layer, allow, or deny)",
			file, line, fields[0])
	}
}

// parseLayer parses: layer <name> = <prefix>[, <prefix>...]
func parseLayer(file string, line int, text string) (Directive, error) {
	eq := strings.Index(text, "=")
	if eq < 0 {
		return Directive{}, fmt.Errorf("adr: %s:%d: layer directive needs '=' (layer <name> = <prefix>[, ...])",
			file, line)
	}
	head := strings.Fields(strings.TrimSpace(text[:eq]))
	if len(head) != 2 { // "layer" + name
		return Directive{}, fmt.Errorf("adr: %s:%d: layer directive needs exactly one name before '='", file, line)
	}
	name := head[1]

	var prefixes []string
	for _, raw := range strings.Split(text[eq+1:], ",") {
		p := strings.TrimSpace(raw)
		if p != "" {
			prefixes = append(prefixes, filepath.ToSlash(p))
		}
	}
	if len(prefixes) == 0 {
		return Directive{}, fmt.Errorf("adr: %s:%d: layer %q has no path prefixes", file, line, name)
	}
	return Directive{File: file, Line: line, Kind: KindLayer, Layer: name, Prefixes: prefixes}, nil
}

// parseEdge parses: allow|deny <from> -> <to>
func parseEdge(file string, line int, fields []string) (Directive, error) {
	// Accept "allow a -> b" (4 tokens) — the arrow must be a standalone "->".
	if len(fields) != 4 || fields[2] != "->" {
		return Directive{}, fmt.Errorf("adr: %s:%d: %s directive must be '%s <from> -> <to>'",
			file, line, fields[0], fields[0])
	}
	from, to := fields[1], fields[3]
	if from == "" || to == "" {
		return Directive{}, fmt.Errorf("adr: %s:%d: %s directive has an empty layer name", file, line, fields[0])
	}
	kind := KindAllow
	if fields[0] == "deny" {
		kind = KindDeny
	}
	return Directive{File: file, Line: line, Kind: kind, From: from, To: to}, nil
}

// SourceOf returns the ADR file that allowed or denied a from -> to edge, or "" when no ADR
// directive governs it. arch.Lint uses this to attribute a violation to the ADR that forbade
// the edge.
func (rs *Ruleset) SourceOf(from, to string) string {
	k := edgeKey(from, to)
	if d, ok := rs.denies[k]; ok {
		return d.File
	}
	if d, ok := rs.allows[k]; ok {
		return d.File
	}
	return ""
}

// Empty reports whether the Ruleset contributes nothing (no ADR directory, or no directives).
func (rs *Ruleset) Empty() bool { return len(rs.Directives) == 0 }

// MergeInto folds the ADR rules into cfg in place. Layers union by name (an ADR may add
// prefixes to an existing layer or introduce a new one). Allow edges union with the json
// rules. A direct conflict — the json rules permit an edge an ADR explicitly denies — is a
// hard error naming the ADR file:line and the json rule, because the two sources of truth
// disagree and silently picking one would defeat the point of an executable ADR.
//
// Precedence summary:
//   - layers:  json ∪ ADR (union of path prefixes)
//   - allow:   json ∪ ADR (an edge allowed by either source is allowed)
//   - deny:    authoritative — an ADR `deny` that contradicts a json `allow` is an error,
//     not a silent override, so the contradiction is surfaced and fixed at one source.
func (rs *Ruleset) MergeInto(cfg *config.Config) error {
	if rs.Empty() {
		return nil
	}

	// 1) Conflict check first, so we never half-merge before failing.
	for _, d := range rs.Directives {
		if d.Kind == KindDeny && cfg.Allows(d.From, d.To) && d.From != d.To {
			// json's rule list explicitly permits an edge this ADR denies.
			return fmt.Errorf(
				"adr: %s:%d denies %s -> %s, but architecture.json allows it — "+
					"remove %q from the %q rule in architecture.json, or drop the ADR deny",
				d.File, d.Line, d.From, d.To, d.To, d.From)
		}
	}

	// 2) Layers: union prefixes by name.
	if cfg.Layers == nil {
		cfg.Layers = map[string][]string{}
	}
	for _, d := range rs.Directives {
		if d.Kind != KindLayer {
			continue
		}
		cfg.Layers[d.Layer] = unionStrings(cfg.Layers[d.Layer], d.Prefixes)
	}

	// 3) Allow edges: union into the rule list. Skip self-edges (always allowed) and
	//    duplicates. A new rule for a previously-ruleless layer is created on demand.
	if cfg.Rules == nil {
		cfg.Rules = map[string][]string{}
	}
	for _, d := range rs.Directives {
		if d.Kind != KindAllow || d.From == d.To {
			continue
		}
		if !contains(cfg.Rules[d.From], d.To) {
			cfg.Rules[d.From] = append(cfg.Rules[d.From], d.To)
		}
	}

	// 4) Every declared layer must carry a rule entry (config's explicitness invariant). A
	//    layer an ADR introduced with no allow edge gets an empty rule ("imports nothing").
	for name := range cfg.Layers {
		if _, ok := cfg.Rules[name]; !ok {
			cfg.Rules[name] = []string{}
		}
	}

	// 5) Validate the merged result through config's own invariants (every rule target is a
	//    real layer, etc.). An ADR that allows a -> b where b is undeclared is caught here.
	return cfg.Revalidate()
}

func unionStrings(a, b []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
