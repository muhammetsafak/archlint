// SPDX-License-Identifier: MIT

package adr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muhammetsafak/archlint/internal/config"
)

// writeADR writes one ADR markdown file under dir/name and returns dir.
func writeADR(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// baseConfig is the json half: domain may import nothing, db may import domain.
func baseConfig() *config.Config {
	return &config.Config{
		Module: "github.com/acme/app",
		Layers: map[string][]string{
			"domain": {"internal/domain"},
			"db":     {"internal/db"},
		},
		Rules: map[string][]string{
			"domain": {},
			"db":     {"domain"},
		},
	}
}

func TestScan_MissingDirIsNoOp(t *testing.T) {
	root := t.TempDir()
	rs, err := Scan(root, filepath.Join(root, "does-not-exist"))
	if err != nil {
		t.Fatalf("a missing ADR dir must not error: %v", err)
	}
	if !rs.Empty() {
		t.Fatal("a missing ADR dir must yield an empty ruleset")
	}
	cfg := baseConfig()
	if err := rs.MergeInto(cfg); err != nil {
		t.Fatalf("merging an empty ruleset must be a no-op: %v", err)
	}
	if rs.SourceOf("domain", "db") != "" {
		t.Error("an empty ruleset attributes nothing")
	}
}

func TestScan_DirIsAFile(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "adr.md")
	if err := os.WriteFile(f, []byte("# not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(root, f); err == nil {
		t.Fatal("scanning a file (not a directory) must error")
	}
}

func TestScan_ParsesDirectivesAndIgnoresNonFenceText(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	writeADR(t, dir, "0007-layering.md", strings.Join([]string{
		"# ADR 0007: Layering",
		"",
		"Some prose that mentions `allow x -> y` but is NOT in a block — must be ignored.",
		"",
		"```archlint",
		"# domain is the core; it may import nothing internal",
		"layer domain = internal/domain",
		"layer db     = internal/db, internal/dao",
		"allow db -> domain   # the repository depends on the model",
		"deny  domain -> db",
		"```",
		"",
		"```text",
		"allow domain -> db   # in a non-archlint block — ignored",
		"```",
	}, "\n"))

	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(rs.Directives) != 4 {
		t.Fatalf("want 4 directives (2 layer, 1 allow, 1 deny), got %d: %+v", len(rs.Directives), rs.Directives)
	}
	// The reported file path reads like a repo path including the dir name.
	if got := rs.SourceOf("domain", "db"); got != "adr/0007-layering.md" {
		t.Errorf("SourceOf(domain,db) = %q, want adr/0007-layering.md", got)
	}
	if got := rs.SourceOf("db", "domain"); got != "adr/0007-layering.md" {
		t.Errorf("SourceOf(db,domain) = %q (an allow edge should attribute too)", got)
	}
	if got := rs.SourceOf("db", "http"); got != "" {
		t.Errorf("SourceOf of an ungoverned edge = %q, want empty", got)
	}
}

func TestScan_DirectiveErrors(t *testing.T) {
	cases := map[string]string{
		"unknown directive": "```archlint\nbanish domain -> db\n```\n",
		"layer no equals":   "```archlint\nlayer domain internal/domain\n```\n",
		"layer two names":   "```archlint\nlayer a b = x\n```\n",
		"layer no prefixes": "```archlint\nlayer domain =\n```\n",
		"edge no arrow":     "```archlint\nallow domain db\n```\n",
		"edge bad arrow":    "```archlint\nallow domain => db\n```\n",
		"edge too few":      "```archlint\nallow domain ->\n```\n",
		"unterminated":      "```archlint\nallow db -> domain\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "adr")
			writeADR(t, dir, "bad.md", body)
			if _, err := Scan(root, dir); err == nil {
				t.Fatalf("expected an error for %q", name)
			}
		})
	}
}

func TestScan_InlineCommentOnlyLineIsEmptyDirective(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	// A line that is only an inline comment becomes empty after stripping; since the leading
	// '#' makes it a comment line, it is skipped — not an error.
	writeADR(t, dir, "x.md", "```archlint\nallow db -> domain # ok\n   # a stray note\n```\n")
	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatalf("an inline-comment-only line must be skipped, not error: %v", err)
	}
	if len(rs.Directives) != 1 {
		t.Fatalf("want 1 directive, got %d: %+v", len(rs.Directives), rs.Directives)
	}
}

func TestScan_SelfContradictingADR(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	writeADR(t, dir, "x.md", "```archlint\nallow domain -> db\ndeny domain -> db\n```\n")
	_, err := Scan(root, dir)
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("an ADR that both allows and denies an edge must conflict, got %v", err)
	}
}

func TestScan_DenyThenAllowAlsoConflicts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	writeADR(t, dir, "x.md", "```archlint\ndeny domain -> db\nallow domain -> db\n```\n")
	if _, err := Scan(root, dir); err == nil {
		t.Fatal("deny-then-allow must also conflict")
	}
}

func TestMergeInto_UnionAndAttribution(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	// The ADR adds a new http layer and an allow edge the json config didn't have.
	writeADR(t, dir, "0008-http.md", strings.Join([]string{
		"```archlint",
		"layer http = internal/http",
		"allow http -> domain",
		"allow http -> db",
		"deny  domain -> db",
		"```",
	}, "\n"))

	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	if err := rs.MergeInto(cfg); err != nil {
		t.Fatalf("merge: %v", err)
	}

	// The new layer and its rule landed.
	if got := cfg.Layers["http"]; len(got) != 1 || got[0] != "internal/http" {
		t.Errorf("http layer = %v", got)
	}
	if !cfg.Allows("http", "domain") || !cfg.Allows("http", "db") {
		t.Error("ADR allow edges for http did not merge")
	}
	// The json edge survives (union).
	if !cfg.Allows("db", "domain") {
		t.Error("json edge db -> domain must survive the merge")
	}
	// The ADR deny is attributable.
	if rs.SourceOf("domain", "db") != "adr/0008-http.md" {
		t.Errorf("deny attribution = %q", rs.SourceOf("domain", "db"))
	}
}

func TestMergeInto_ExtendsExistingLayerPrefixes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	writeADR(t, dir, "x.md", "```archlint\nlayer db = internal/dao\n```\n")
	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	if err := rs.MergeInto(cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.Layers["db"]
	if len(got) != 2 || got[0] != "internal/db" || got[1] != "internal/dao" {
		t.Errorf("db prefixes = %v, want [internal/db internal/dao]", got)
	}
}

func TestMergeInto_NewLayerWithoutEdgeGetsEmptyRule(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	writeADR(t, dir, "x.md", "```archlint\nlayer http = internal/http\n```\n")
	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	if err := rs.MergeInto(cfg); err != nil {
		t.Fatalf("a new layer with no edge must validate (empty rule), got %v", err)
	}
	if r, ok := cfg.Rules["http"]; !ok || len(r) != 0 {
		t.Errorf("http rule = %v (ok=%v), want an empty slice", r, ok)
	}
}

func TestMergeInto_ConflictWithJSONAllow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	// json allows db -> domain; the ADR denies it → contradiction.
	writeADR(t, dir, "0009-conflict.md", "```archlint\ndeny db -> domain\n```\n")
	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	err = rs.MergeInto(baseConfig())
	if err == nil {
		t.Fatal("an ADR deny contradicting a json allow must error")
	}
	if !strings.Contains(err.Error(), "0009-conflict.md") || !strings.Contains(err.Error(), "architecture.json") {
		t.Errorf("conflict error must name the ADR and the json: %v", err)
	}
}

func TestMergeInto_AllowToUndeclaredLayerFailsValidation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	// "http" is never declared as a layer, so allowing an edge to it is invalid.
	writeADR(t, dir, "x.md", "```archlint\nallow db -> http\n```\n")
	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.MergeInto(baseConfig()); err == nil {
		t.Fatal("an allow edge to an undeclared layer must fail revalidation")
	}
}

func TestMergeInto_DuplicateAllowAndSelfEdgeAreIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	// db -> domain is already a json rule; domain -> domain is a self-edge (always allowed).
	writeADR(t, dir, "x.md", "```archlint\nallow db -> domain\nallow domain -> domain\n```\n")
	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	if err := rs.MergeInto(cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Rules["db"]) != 1 {
		t.Errorf("duplicate allow must not grow the rule list: %v", cfg.Rules["db"])
	}
	if _, ok := cfg.Rules["domain"]; ok && len(cfg.Rules["domain"]) != 0 {
		t.Errorf("a self-edge must not be added as a rule: %v", cfg.Rules["domain"])
	}
}

func TestScan_TildeFenceAndExtraInfoString(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	// A ~~~archlint fence with an extra info word, plus a markdown variant extension.
	writeADR(t, dir, "x.markdown", "~~~archlint extra\nallow db -> domain\n~~~\n")
	// "archlintx" must NOT open a block — its directives are ignored.
	writeADR(t, dir, "y.md", "```archlintx\ndeny db -> domain\n```\n")
	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Directives) != 1 || rs.Directives[0].From != "db" {
		t.Fatalf("only the ~~~archlint block should parse: %+v", rs.Directives)
	}
}

func TestScan_DeterministicAcrossFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adr")
	writeADR(t, dir, "b.md", "```archlint\nallow db -> domain\n```\n")
	writeADR(t, dir, "a.md", "```archlint\nlayer http = internal/http\n```\n")
	rs, err := Scan(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Files are scanned in sorted order: a.md (layer) before b.md (allow).
	if rs.Directives[0].File != "adr/a.md" || rs.Directives[0].Kind != KindLayer {
		t.Errorf("expected a.md's layer directive first, got %+v", rs.Directives[0])
	}
}
