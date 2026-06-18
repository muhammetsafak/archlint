package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "architecture.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_LayerOfAndAllows(t *testing.T) {
	p := writeConfig(t, `{
		"module": "github.com/acme/app",
		"layers": {
			"domain": ["internal/domain"],
			"db": ["internal/db"],
			"db_migrations": ["internal/db/migrations"]
		},
		"rules": {
			"domain": [],
			"db": ["domain"],
			"db_migrations": ["db"]
		}
	}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}

	if got := c.LayerOf("internal/domain/order.go"); got != "domain" {
		t.Errorf("LayerOf(domain file) = %q", got)
	}
	if got := c.LayerOf("internal/db/repo.go"); got != "db" {
		t.Errorf("LayerOf(db file) = %q", got)
	}
	// longest-prefix wins: a file under internal/db/migrations is the sub-layer.
	if got := c.LayerOf("internal/db/migrations/0001.go"); got != "db_migrations" {
		t.Errorf("LayerOf(migrations file) = %q, want db_migrations", got)
	}
	if got := c.LayerOf("cmd/app/main.go"); got != "" {
		t.Errorf("LayerOf(ungoverned) = %q, want empty", got)
	}

	if c.Allows("domain", "db") {
		t.Error("domain → db should not be allowed")
	}
	if !c.Allows("db", "domain") {
		t.Error("db → domain should be allowed")
	}
	if !c.Allows("domain", "domain") {
		t.Error("a same-layer import should always be allowed")
	}
}

func TestValidate_Errors(t *testing.T) {
	cases := map[string]string{
		"no layers":           `{"module":"m","layers":{},"rules":{}}`,
		"layer no prefixes":   `{"module":"m","layers":{"a":[]},"rules":{"a":[]}}`,
		"layer no rule":       `{"module":"m","layers":{"a":["x"]},"rules":{}}`,
		"rule unknown target": `{"module":"m","layers":{"a":["x"]},"rules":{"a":["b"]}}`,
		"rule unknown layer":  `{"module":"m","layers":{"a":["x"]},"rules":{"a":[],"z":[]}}`,
		"alias empty dest":    `{"layers":{"a":["x"]},"rules":{"a":[]},"aliases":{"@/":""}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Fatalf("expected a validation error for %q", name)
			}
		})
	}
}

func TestLoad_ModuleOptionalWithAliases(t *testing.T) {
	// A TypeScript-only project: no module, an alias instead.
	c, err := Load(writeConfig(t, `{
		"aliases": {"@/": "src/"},
		"layers": {"domain": ["src/domain"], "db": ["src/db"]},
		"rules": {"domain": [], "db": ["domain"]}
	}`))
	if err != nil {
		t.Fatalf("a module-less config with aliases should load: %v", err)
	}
	if c.Module != "" {
		t.Errorf("module should be empty, got %q", c.Module)
	}
	if c.Aliases["@/"] != "src/" {
		t.Errorf("alias not parsed: %v", c.Aliases)
	}
}

func TestLoad_MissingOrInvalid(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("a missing config should error")
	}
	if _, err := Load(writeConfig(t, `not json`)); err == nil {
		t.Fatal("an invalid config should error")
	}
}
