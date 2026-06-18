package lang

import (
	"testing"

	"github.com/muhammetsafak/archlint/internal/config"
)

func TestGo_ImportsAndResolve(t *testing.T) {
	src := []byte("package x\n\nimport (\n\t\"fmt\"\n\t\"github.com/acme/app/internal/db\"\n)\n")
	imps, err := goLang{}.Imports("x.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(imps) != 2 {
		t.Fatalf("want 2 imports, got %d (%v)", len(imps), imps)
	}

	cfg := &config.Config{Module: "github.com/acme/app"}
	if got := (goLang{}).Resolve("github.com/acme/app/internal/db", "", cfg); got != "internal/db" {
		t.Errorf("resolve internal = %q, want internal/db", got)
	}
	if got := (goLang{}).Resolve("fmt", "", cfg); got != "" {
		t.Errorf("resolve external = %q, want empty", got)
	}
	if got := (goLang{}).Resolve("github.com/acme/app", "", cfg); got != "" {
		t.Errorf("resolve module root = %q, want empty", got)
	}
	if got := (goLang{}).Resolve("github.com/acme/app/internal/db", "", &config.Config{}); got != "" {
		t.Errorf("no module → unresolvable, got %q", got)
	}
}

func TestFor(t *testing.T) {
	if l := For("a.go"); l == nil || l.Name() != "go" {
		t.Error("'.go' should map to the go language")
	}
	if l := For("a.ts"); l == nil || l.Name() != "typescript" {
		t.Error("'.ts' should map to the typescript language")
	}
	if l := For("a.py"); l == nil || l.Name() != "python" {
		t.Error("'.py' should map to the python language")
	}
	for _, ext := range []string{"a.tsx", "a.mts", "a.cts", "a.js", "a.jsx", "a.mjs", "a.cjs", "a.pyi"} {
		if For(ext) == nil {
			t.Errorf("%s should have a language", ext)
		}
	}
	if For("a.txt") != nil {
		t.Error("a.txt should be unsupported")
	}
}
