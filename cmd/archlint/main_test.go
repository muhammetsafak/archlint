package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muhammetsafak/archlint/internal/arch"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func projectWithViolation(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "architecture.json"),
		`{"module":"github.com/acme/app","layers":{"domain":["internal/domain"],"db":["internal/db"]},"rules":{"domain":[],"db":["domain"]}}`)
	write(t, filepath.Join(root, "internal/domain/bad.go"),
		"package domain\n\nimport \"github.com/acme/app/internal/db\"\n")
	write(t, filepath.Join(root, "internal/db/repo.go"),
		"package db\n")
	return root
}

func TestRunCheck_Violation(t *testing.T) {
	root := projectWithViolation(t)
	if code := runCheck([]string{"--config", filepath.Join(root, "architecture.json"), root}); code != 1 {
		t.Fatalf("a tree with a violation should exit 1, got %d", code)
	}
}

func TestRunCheck_ConfigResolvedInsideDir(t *testing.T) {
	root := projectWithViolation(t)
	// no --config: it must be found inside the scanned dir
	if code := runCheck([]string{root}); code != 1 {
		t.Fatalf("config should resolve inside the scanned dir; got exit %d", code)
	}
}

func TestRunCheck_Clean(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "architecture.json"),
		`{"module":"github.com/acme/app","layers":{"domain":["internal/domain"],"db":["internal/db"]},"rules":{"domain":[],"db":["domain"]}}`)
	write(t, filepath.Join(root, "internal/db/repo.go"),
		"package db\n\nimport \"github.com/acme/app/internal/domain\"\n")
	write(t, filepath.Join(root, "internal/domain/order.go"),
		"package domain\n")
	if code := runCheck([]string{"--config", filepath.Join(root, "architecture.json"), root}); code != 0 {
		t.Fatalf("a clean tree should exit 0, got %d", code)
	}
}

func TestRunCheck_MissingConfig(t *testing.T) {
	if code := runCheck([]string{"--config", filepath.Join(t.TempDir(), "nope.json"), t.TempDir()}); code != 2 {
		t.Fatalf("a missing config should exit 2, got %d", code)
	}
}

func TestRun_Dispatch(t *testing.T) {
	if code := run(nil); code != 2 {
		t.Fatalf("no command → exit 2, got %d", code)
	}
	if code := run([]string{"help"}); code != 0 {
		t.Fatalf("help → exit 0, got %d", code)
	}
	if code := run([]string{"bogus"}); code != 2 {
		t.Fatalf("unknown command → exit 2, got %d", code)
	}
	root := projectWithViolation(t)
	if code := run([]string{"check", "--config", filepath.Join(root, "architecture.json"), root}); code != 1 {
		t.Fatalf("run check on a violation → exit 1, got %d", code)
	}
}

func TestEmitGitHub(t *testing.T) {
	var buf bytes.Buffer
	violations := []arch.Violation{
		{File: "src/domain/bad.ts", Line: 4, FromLayer: "domain", ToLayer: "db", Import: "@/db/repo"},
	}
	emitGitHub(&buf, "service", violations)
	out := buf.String()
	if !strings.Contains(out, "::error file=service/src/domain/bad.ts,line=4,title=archlint::") {
		t.Errorf("missing inline annotation:\n%s", out)
	}
	if !strings.Contains(out, "::notice") {
		t.Errorf("missing summary notice:\n%s", out)
	}

	buf.Reset()
	emitGitHub(&buf, ".", nil)
	if !strings.Contains(buf.String(), "no architecture violations") {
		t.Errorf("clean github output: %s", buf.String())
	}
}

func TestEmitText(t *testing.T) {
	var buf bytes.Buffer
	violations := []arch.Violation{
		{File: "internal/domain/bad.go", Line: 6, FromLayer: "domain", ToLayer: "db", Import: "x/internal/db"},
	}
	emitText(&buf, ".", "architecture.json", 2, violations)
	out := buf.String()
	if !strings.Contains(out, "internal/domain/bad.go:6") || !strings.Contains(out, "How to fix") {
		t.Errorf("text output missing parts:\n%s", out)
	}
}

func TestRunCheck_FormatFlag(t *testing.T) {
	root := projectWithViolation(t)
	cfg := filepath.Join(root, "architecture.json")
	if code := runCheck([]string{"--config", cfg, "--format", "github", root}); code != 1 {
		t.Fatalf("github format on a violation → exit 1, got %d", code)
	}
	if code := runCheck([]string{"--config", cfg, "--format", "xml", root}); code != 2 {
		t.Fatalf("unknown format → exit 2, got %d", code)
	}
}

func TestRunCheck_AutoGitHubFormat(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	root := projectWithViolation(t)
	if code := runCheck([]string{"--config", filepath.Join(root, "architecture.json"), root}); code != 1 {
		t.Fatalf("auto github format → exit 1, got %d", code)
	}
}

// projectWithADRRule builds a tree whose architecture.json governs only one layer, and whose
// ADR declares the *other* layer plus the deny that makes the planted import a violation —
// proving an ADR-sourced rule participates in linting.
func projectWithADRRule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// json knows only the domain layer (no db, no deny edge): on its own, no violation.
	write(t, filepath.Join(root, "architecture.json"),
		`{"module":"github.com/acme/app","layers":{"domain":["internal/domain"]},"rules":{"domain":[]}}`)
	// The ADR introduces the db layer and the deny; the decision record IS the rule.
	write(t, filepath.Join(root, "docs/adr/0001-layering.md"),
		"# ADR 0001\n\n```archlint\nlayer db = internal/db\ndeny domain -> db\n```\n")
	write(t, filepath.Join(root, "internal/domain/bad.go"),
		"package domain\n\nimport \"github.com/acme/app/internal/db\"\n") // VIOLATION via ADR
	write(t, filepath.Join(root, "internal/db/repo.go"), "package db\n")
	return root
}

func TestRunCheck_ADRSourcedViolation(t *testing.T) {
	root := projectWithADRRule(t)
	cfg := filepath.Join(root, "architecture.json")
	// Without the ADR dir, nothing governs db → clean.
	if code := runCheck([]string{"--config", cfg, "--adr-dir", filepath.Join(root, "nope"), root}); code != 0 {
		t.Fatalf("without ADRs the tree is clean, got exit %d", code)
	}
	// With the ADR dir, the deny is enforced → violation.
	if code := runCheck([]string{"--config", cfg, "--adr-dir", filepath.Join(root, "docs/adr"), root}); code != 1 {
		t.Fatalf("an ADR-sourced deny must be enforced, got exit %d", code)
	}
}

func TestRunCheck_ADRDefaultDirResolvedInsideScannedDir(t *testing.T) {
	root := projectWithADRRule(t)
	// No --adr-dir flag: the default "docs/adr" must resolve inside the scanned dir.
	if code := runCheck([]string{root}); code != 1 {
		t.Fatalf("default adr-dir should resolve inside the scanned dir, got exit %d", code)
	}
}

func TestRunCheck_ADRConflictExitsTwo(t *testing.T) {
	root := t.TempDir()
	// json allows db -> domain; the ADR denies it → an irreconcilable conflict (exit 2).
	write(t, filepath.Join(root, "architecture.json"),
		`{"module":"github.com/acme/app","layers":{"domain":["internal/domain"],"db":["internal/db"]},"rules":{"domain":[],"db":["domain"]}}`)
	write(t, filepath.Join(root, "docs/adr/0001.md"),
		"```archlint\ndeny db -> domain\n```\n")
	if code := runCheck([]string{root}); code != 2 {
		t.Fatalf("an ADR/json conflict must exit 2, got %d", code)
	}
}

func TestRunCheck_ADRParseErrorExitsTwo(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "architecture.json"),
		`{"module":"github.com/acme/app","layers":{"domain":["internal/domain"]},"rules":{"domain":[]}}`)
	write(t, filepath.Join(root, "docs/adr/bad.md"), "```archlint\nbanish a -> b\n```\n")
	if code := runCheck([]string{root}); code != 2 {
		t.Fatalf("a malformed ADR directive must exit 2, got %d", code)
	}
}

func TestEmitText_ADRSource(t *testing.T) {
	var buf bytes.Buffer
	violations := []arch.Violation{
		{File: "internal/domain/bad.go", Line: 3, FromLayer: "domain", ToLayer: "db",
			Import: "x/internal/db", Source: "docs/adr/0001.md"},
	}
	emitText(&buf, ".", "architecture.json", 2, violations)
	if !strings.Contains(buf.String(), "[docs/adr/0001.md]") {
		t.Errorf("text output must attribute the ADR source:\n%s", buf.String())
	}
}

func TestEmitGitHub_ADRSource(t *testing.T) {
	var buf bytes.Buffer
	violations := []arch.Violation{
		{File: "internal/domain/bad.go", Line: 3, FromLayer: "domain", ToLayer: "db",
			Import: "x/internal/db", Source: "docs/adr/0001.md"},
	}
	emitGitHub(&buf, ".", violations)
	if !strings.Contains(buf.String(), "[docs/adr/0001.md]") {
		t.Errorf("github output must attribute the ADR source:\n%s", buf.String())
	}
}
