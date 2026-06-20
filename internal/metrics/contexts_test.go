package metrics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muhammetsafak/archlint/internal/arch"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ctxGraph: ordering{domain,app public=app} and billing{ledger,gateway public=gateway}.
//
//	app→domain      same context           OK
//	app→gateway     cross, hits public     OK
//	domain→ledger   cross, hits internal   VIOLATION
//	gateway→ledger  same context           OK
func ctxGraph() *arch.Graph {
	e := func(from, to, file string, line int) arch.Edge {
		return arch.Edge{From: from, To: to,
			Imports: []arch.ImportEdge{{File: file, Line: line, FromLayer: from, ToLayer: to}}}
	}
	return &arch.Graph{
		Layers: []string{"app", "domain", "gateway", "ledger"},
		Edges: []arch.Edge{
			e("app", "domain", "app/s.go", 3),
			e("app", "gateway", "app/s.go", 4),
			e("domain", "ledger", "domain/o.go", 7),
			e("gateway", "ledger", "gateway/g.go", 5),
		},
	}
}

const goodContexts = `{
  "contexts": [
    {"name":"ordering","layers":["domain","app"],"public":["app"],"abstract":["domain"]},
    {"name":"billing","layers":["ledger","gateway"],"public":["gateway"]}
  ]
}`

func TestLoadContexts_DetectsCrossContextBypass(t *testing.T) {
	dir := t.TempDir()
	cs, err := LoadContexts(writeFile(t, dir, "contexts.json", goodContexts))
	if err != nil {
		t.Fatal(err)
	}
	v := cs.Detect(ctxGraph())
	if len(v) != 1 {
		t.Fatalf("want 1 cross-context violation, got %d: %+v", len(v), v)
	}
	got := v[0]
	if got.FromContext != "ordering" || got.ToContext != "billing" ||
		got.FromLayer != "domain" || got.ToLayer != "ledger" {
		t.Errorf("violation = %+v, want ordering/domain → billing/ledger", got)
	}
	if got.Detail == "" {
		t.Errorf("violation should carry provenance")
	}
}

func TestLoadContexts_AbstractLayers(t *testing.T) {
	dir := t.TempDir()
	cs, err := LoadContexts(writeFile(t, dir, "contexts.json", goodContexts))
	if err != nil {
		t.Fatal(err)
	}
	ab := cs.AbstractLayers()
	if !ab["domain"] || len(ab) != 1 {
		t.Errorf("AbstractLayers = %v, want only {domain}", ab)
	}
	// A nil ContextSet yields an empty (non-nil) map and a nil Detect.
	var nilCS *ContextSet
	if len(nilCS.AbstractLayers()) != 0 {
		t.Errorf("nil ContextSet AbstractLayers should be empty")
	}
	if nilCS.Detect(ctxGraph()) != nil {
		t.Errorf("nil ContextSet Detect should be nil")
	}
}

func TestLoadContexts_EmptyPublicClosesContext(t *testing.T) {
	// billing publishes nothing → every inbound cross-context edge is a breach.
	dir := t.TempDir()
	cfg := `{"contexts":[
		{"name":"ordering","layers":["domain","app"],"public":["app"]},
		{"name":"billing","layers":["ledger","gateway"]}
	]}`
	cs, err := LoadContexts(writeFile(t, dir, "contexts.json", cfg))
	if err != nil {
		t.Fatal(err)
	}
	v := cs.Detect(ctxGraph())
	// app→gateway AND domain→ledger now both breach (no public port at all).
	if len(v) != 2 {
		t.Fatalf("a context with no public layers should reject all inbound edges, got %d: %+v", len(v), v)
	}
}

func TestLoadContexts_MissingFileIsNoOp(t *testing.T) {
	cs, err := LoadContexts(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a missing contexts file must not error, got %v", err)
	}
	if cs != nil {
		t.Errorf("a missing contexts file should yield a nil set, got %+v", cs)
	}
}

func TestLoadContexts_ValidationErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"no contexts":            `{"contexts":[]}`,
		"empty name":             `{"contexts":[{"name":"","layers":["a"]}]}`,
		"no layers":              `{"contexts":[{"name":"x","layers":[]}]}`,
		"duplicate context":      `{"contexts":[{"name":"x","layers":["a"]},{"name":"x","layers":["b"]}]}`,
		"overlapping layer":      `{"contexts":[{"name":"x","layers":["a"]},{"name":"y","layers":["a"]}]}`,
		"public not in layers":   `{"contexts":[{"name":"x","layers":["a"],"public":["b"]}]}`,
		"abstract not in layers": `{"contexts":[{"name":"x","layers":["a"],"abstract":["b"]}]}`,
		"bad json":               `{`,
	}
	for name, body := range cases {
		p := writeFile(t, dir, "c.json", body)
		if _, err := LoadContexts(p); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

func TestContextSummary(t *testing.T) {
	withPublic := Context{Name: "ordering", Layers: []string{"domain", "app"}, Public: []string{"app"}}
	if got := withPublic.ContextSummary(); got != "ordering {layers: domain, app; public: app}" {
		t.Errorf("summary = %q", got)
	}
	noPublic := Context{Name: "billing", Layers: []string{"ledger"}}
	if got := noPublic.ContextSummary(); got != "billing {layers: ledger; public: none}" {
		t.Errorf("summary (no public) = %q", got)
	}
}
