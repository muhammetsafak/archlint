package arch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muhammetsafak/archlint/internal/config"
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

// project builds a sample tree and its config: domain may import nothing internal; db may
// import domain. It plants one violation (domain importing db) plus several legal imports.
func project(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()

	// domain: a clean file (external import only) and the violating file.
	write(t, filepath.Join(root, "internal/domain/order.go"),
		"package domain\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n")
	write(t, filepath.Join(root, "internal/domain/bad.go"),
		"package domain\n\nimport \"github.com/acme/app/internal/db\"\n") // VIOLATION
	// db: a legal import of domain.
	write(t, filepath.Join(root, "internal/db/repo.go"),
		"package db\n\nimport \"github.com/acme/app/internal/domain\"\n")
	// cmd: ungoverned — its forbidden-looking import must be ignored.
	write(t, filepath.Join(root, "cmd/app/main.go"),
		"package main\n\nimport \"github.com/acme/app/internal/db\"\n")

	cfg := &config.Config{
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
	return root, cfg
}

func TestLint_FlagsTheBoundaryViolationOnly(t *testing.T) {
	root, cfg := project(t)

	violations, err := Lint(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("want exactly 1 violation, got %d: %+v", len(violations), violations)
	}
	v := violations[0]
	if v.File != "internal/domain/bad.go" {
		t.Errorf("file = %q", v.File)
	}
	if v.FromLayer != "domain" || v.ToLayer != "db" {
		t.Errorf("layers = %s → %s, want domain → db", v.FromLayer, v.ToLayer)
	}
	if v.Import != "github.com/acme/app/internal/db" {
		t.Errorf("import = %q", v.Import)
	}
	if v.Line <= 0 {
		t.Errorf("line = %d", v.Line)
	}
}

func TestLint_SkipsSpecialCases(t *testing.T) {
	root := t.TempDir()
	// A governed file importing: stdlib (external), the module root itself, and an
	// ungoverned internal path — none of which is a violation.
	write(t, filepath.Join(root, "internal/domain/ok.go"),
		"package domain\n\nimport (\n\t\"fmt\"\n\t\"github.com/acme/app\"\n\t\"github.com/acme/app/internal/util\"\n)\n\nvar _ = fmt.Sprint\n")
	// An unparseable file must be skipped, not crash the lint.
	write(t, filepath.Join(root, "internal/domain/broken.go"),
		"package domain\n\nimport (\n")
	// Anything under vendor/ is skipped wholesale (exercises the SkipDir branch).
	write(t, filepath.Join(root, "vendor/x/v.go"),
		"package x\n\nimport \"github.com/acme/app/internal/db\"\n")

	cfg := &config.Config{
		Module: "github.com/acme/app",
		Layers: map[string][]string{"domain": {"internal/domain"}, "db": {"internal/db"}},
		Rules:  map[string][]string{"domain": {}, "db": {"domain"}},
	}
	v, err := Lint(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 0 {
		t.Fatalf("external / module-root / ungoverned / vendored imports must not violate, got %+v", v)
	}
}

func TestLint_TypeScript(t *testing.T) {
	root := t.TempDir()
	// domain may import nothing internal; db may import domain.
	write(t, filepath.Join(root, "src/domain/order.ts"),
		"export interface Order { id: number }\n")
	// VIOLATION (relative): domain importing db.
	write(t, filepath.Join(root, "src/domain/bad.ts"),
		"import { connect } from '../db/repo';\nexport const x = connect;\n")
	// VIOLATION (alias): domain importing db via @/.
	write(t, filepath.Join(root, "src/domain/bad-alias.ts"),
		"import { connect } from '@/db/repo';\nexport const y = connect;\n")
	// legal: db importing domain.
	write(t, filepath.Join(root, "src/db/repo.ts"),
		"import { Order } from '../domain/order';\nexport function connect(): Order | null { return null; }\n")
	// external/bare import: ignored.
	write(t, filepath.Join(root, "src/db/ext.ts"),
		"import React from 'react';\nexport default React;\n")

	cfg := &config.Config{
		Aliases: map[string]string{"@/": "src/"},
		Layers:  map[string][]string{"domain": {"src/domain"}, "db": {"src/db"}},
		Rules:   map[string][]string{"domain": {}, "db": {"domain"}},
	}
	v, err := Lint(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 2 {
		t.Fatalf("want 2 TS violations (relative + alias), got %d: %+v", len(v), v)
	}
	// Sorted by file: "bad-alias.ts" sorts before "bad.ts".
	if v[0].File != "src/domain/bad-alias.ts" || v[0].FromLayer != "domain" || v[0].ToLayer != "db" {
		t.Errorf("v[0] = %+v", v[0])
	}
	if v[1].File != "src/domain/bad.ts" || v[1].Import != "../db/repo" {
		t.Errorf("v[1] = %+v", v[1])
	}
}

func TestLint_Python(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "app/domain/order.py"),
		"from dataclasses import dataclass\n\n@dataclass\nclass Order:\n    id: int\n")
	// VIOLATION: domain reaching up into db via a relative import.
	write(t, filepath.Join(root, "app/domain/bad.py"),
		"from ..db.repo import find_order\n")
	// legal: db importing domain.
	write(t, filepath.Join(root, "app/db/repo.py"),
		"from ..domain.order import Order\n\n\ndef find_order(i):\n    return None\n")

	cfg := &config.Config{
		Layers: map[string][]string{"domain": {"app/domain"}, "db": {"app/db"}},
		Rules:  map[string][]string{"domain": {}, "db": {"domain"}},
	}
	v, err := Lint(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 {
		t.Fatalf("want 1 Python violation, got %d: %+v", len(v), v)
	}
	if v[0].File != "app/domain/bad.py" || v[0].FromLayer != "domain" || v[0].ToLayer != "db" {
		t.Errorf("v[0] = %+v", v[0])
	}
}

func TestLint_CleanTreeHasNoViolations(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "internal/db/repo.go"),
		"package db\n\nimport \"github.com/acme/app/internal/domain\"\n")
	write(t, filepath.Join(root, "internal/domain/order.go"),
		"package domain\n\nimport \"strings\"\n\nvar _ = strings.TrimSpace\n")
	cfg := &config.Config{
		Module: "github.com/acme/app",
		Layers: map[string][]string{"domain": {"internal/domain"}, "db": {"internal/db"}},
		Rules:  map[string][]string{"domain": {}, "db": {"domain"}},
	}

	violations, err := Lint(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("clean tree should have no violations, got %+v", violations)
	}
}
