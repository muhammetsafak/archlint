package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muhammetsafak/archlint/internal/arch"
	"github.com/muhammetsafak/archlint/internal/metrics"
)

// metricsProject builds a 4-layer Go tree under two bounded contexts owned by two teams, with a
// planted cross-context bypass (ordering/domain → billing/ledger, billing's internal layer).
// The architecture.json *allows* that edge, so `check` is clean — only `metrics --contexts`
// flags it, proving metrics catches what the boundary rules permit. Returns the root.
func metricsProject(t *testing.T, withContexts, withTeams bool) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "architecture.json"), `{
		"module":"github.com/acme/shop",
		"layers":{"domain":["internal/ordering/domain"],"app":["internal/ordering/app"],
		          "ledger":["internal/billing/ledger"],"gateway":["internal/billing/gateway"]},
		"rules":{"domain":["ledger"],"app":["domain","gateway"],"ledger":[],"gateway":["ledger"]}
	}`)
	if withContexts {
		write(t, filepath.Join(root, "contexts.json"), `{"contexts":[
			{"name":"ordering","layers":["domain","app"],"public":["app"],"abstract":["domain"]},
			{"name":"billing","layers":["ledger","gateway"],"public":["gateway"]}
		]}`)
	}
	if withTeams {
		write(t, filepath.Join(root, "teams.json"),
			`{"teams":{"commerce":["domain","app"],"platform":["ledger","gateway"]}}`)
	}
	// domain bypasses billing's port:
	write(t, filepath.Join(root, "internal/ordering/domain/order.go"),
		"package domain\n\nimport \"github.com/acme/shop/internal/billing/ledger\"\n\nfunc Total(id int) int { return ledger.Balance(id) }\n")
	write(t, filepath.Join(root, "internal/ordering/app/service.go"),
		"package app\n\nimport (\n\t\"github.com/acme/shop/internal/billing/gateway\"\n\t\"github.com/acme/shop/internal/ordering/domain\"\n)\n\nfunc Checkout() error { _ = domain.Total; return gateway.Charge(0) }\n")
	write(t, filepath.Join(root, "internal/billing/ledger/ledger.go"),
		"package ledger\n\nfunc Balance(a int) int { return a }\nfunc Post(a, b int) {}\n")
	write(t, filepath.Join(root, "internal/billing/gateway/gateway.go"),
		"package gateway\n\nimport \"github.com/acme/shop/internal/billing/ledger\"\n\nfunc Charge(n int) error { ledger.Post(0, n); return nil }\n")
	return root
}

func TestRunMetrics_ContextViolationFailsByDefault(t *testing.T) {
	root := metricsProject(t, true, true)
	// check is clean (rules allow domain→ledger): metrics is what catches the context bypass.
	if code := runCheck([]string{root}); code != 0 {
		t.Fatalf("check should be clean (layer rules permit the edge), got exit %d", code)
	}
	if code := runMetrics([]string{root}); code != 1 {
		t.Fatalf("a cross-context bypass must fail metrics by default, got exit %d", code)
	}
}

func TestRunMetrics_FailOnViolationFalseExitsZero(t *testing.T) {
	root := metricsProject(t, true, true)
	if code := runMetrics([]string{"--fail-on-violation=false", root}); code != 0 {
		t.Fatalf("--fail-on-violation=false should not fail on a context breach, got exit %d", code)
	}
}

func TestRunMetrics_NoContextsNoTeamsIsCleanNoOp(t *testing.T) {
	// No contexts.json, no teams.json present: metrics still runs (coupling only) and, with no
	// gate and no context rules, never fails.
	root := metricsProject(t, false, false)
	if code := runMetrics([]string{root}); code != 0 {
		t.Fatalf("metrics with no contexts/teams must be a clean no-op, got exit %d", code)
	}
}

func TestRunMetrics_InstabilityGate(t *testing.T) {
	root := metricsProject(t, false, false)
	// app has I=1.0; a ceiling below it must fail even without contexts/teams.
	if code := runMetrics([]string{"--max-instability", "0.9", root}); code != 1 {
		t.Fatalf("an exceeded instability ceiling must exit 1, got %d", code)
	}
	// A generous ceiling passes.
	if code := runMetrics([]string{"--max-instability", "1.0", root}); code != 0 {
		t.Fatalf("a ceiling of 1.0 should pass, got %d", code)
	}
}

func TestRunMetrics_GitHubFormat(t *testing.T) {
	root := metricsProject(t, true, true)
	if code := runMetrics([]string{"--format", "github", root}); code != 1 {
		t.Fatalf("github format on a violation → exit 1, got %d", code)
	}
}

func TestRunMetrics_BadFormat(t *testing.T) {
	root := metricsProject(t, false, false)
	if code := runMetrics([]string{"--format", "xml", root}); code != 2 {
		t.Fatalf("unknown --format → exit 2, got %d", code)
	}
}

func TestRunMetrics_MissingConfigExitsTwo(t *testing.T) {
	if code := runMetrics([]string{"--config", filepath.Join(t.TempDir(), "nope.json"), t.TempDir()}); code != 2 {
		t.Fatalf("a missing config → exit 2, got %d", code)
	}
}

func TestRunMetrics_BadContextsExitsTwo(t *testing.T) {
	root := metricsProject(t, false, false)
	write(t, filepath.Join(root, "contexts.json"), `{"contexts":[{"name":"x","layers":[]}]}`)
	if code := runMetrics([]string{root}); code != 2 {
		t.Fatalf("an invalid contexts.json → exit 2, got %d", code)
	}
}

func TestRunMetrics_BadTeamsExitsTwo(t *testing.T) {
	root := metricsProject(t, false, false)
	write(t, filepath.Join(root, "teams.json"), `{"teams":{}}`)
	if code := runMetrics([]string{root}); code != 2 {
		t.Fatalf("an invalid teams.json → exit 2, got %d", code)
	}
}

func TestRun_DispatchesMetrics(t *testing.T) {
	root := metricsProject(t, true, true)
	if code := run([]string{"metrics", root}); code != 1 {
		t.Fatalf("run metrics on a context breach → exit 1, got %d", code)
	}
}

func TestEmitMetricsText_NoTeamsShowsSkip(t *testing.T) {
	var buf bytes.Buffer
	report := metrics.Analyze([]string{"a"}, nil, metrics.Options{MaxInstability: -1})
	emitMetricsText(&buf, ".", report, nil, nil, nil)
	out := buf.String()
	if !strings.Contains(out, "Conway Mismatch Index: skipped") {
		t.Errorf("a nil Conway report should print the skip note:\n%s", out)
	}
	if !strings.Contains(out, "Ca=afferent") {
		t.Errorf("text report should carry the legend:\n%s", out)
	}
}

// buildGraphFixture builds a small graph so the emitter branches (instability breach, SDP
// breach, context breach, populated Conway) are all exercised directly against the
// io.Writer-based emitters — no os.Stdout plumbing needed.
func buildGraphFixture() *arch.Graph {
	e := func(from, to string) arch.Edge {
		return arch.Edge{From: from, To: to,
			Imports: []arch.ImportEdge{{File: from + ".go", Line: 1, FromLayer: from, ToLayer: to}}}
	}
	// a↔b, b→c gives an SDP edge (a I=0.5 → b I=0.667) and an over-instability layer (b > 0.5).
	return &arch.Graph{
		Layers: []string{"a", "b", "c"},
		Edges:  []arch.Edge{e("a", "b"), e("b", "a"), e("b", "c")},
	}
}

func TestEmitMetricsText_AllSections(t *testing.T) {
	var buf bytes.Buffer
	g := buildGraphFixture()
	r := metrics.AnalyzeGraph(g, metrics.Options{MaxInstability: 0.5})
	dir := t.TempDir()
	cs, err := metrics.LoadContexts(writeJSON(t, dir, "c.json",
		`{"contexts":[{"name":"front","layers":["a"]},{"name":"back","layers":["b","c"],"public":["c"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	tm, err := metrics.LoadTeams(writeJSON(t, dir, "t.json", `{"teams":{"x":["a"],"y":["b","c"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	emitMetricsText(&buf, ".", r, cs, cs.Detect(g), tm.Conway(g))
	out := buf.String()
	for _, want := range []string{
		"over the instability ceiling",            // instability gate breached
		"Stable-Dependencies-Principle violation", // SDP breach
		"cross-context violation",                 // DDD breach (a→b bypasses back's port)
		"Conway Mismatch Index:",                  // Conway populated
		"team=x",                                  // per-layer Conway row
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestEmitMetricsGitHub_AllAnnotations(t *testing.T) {
	var buf bytes.Buffer
	g := buildGraphFixture()
	r := metrics.AnalyzeGraph(g, metrics.Options{MaxInstability: 0.5})
	dir := t.TempDir()
	cs, _ := metrics.LoadContexts(writeJSON(t, dir, "c.json",
		`{"contexts":[{"name":"front","layers":["a"]},{"name":"back","layers":["b","c"],"public":["c"]}]}`))
	tm, _ := metrics.LoadTeams(writeJSON(t, dir, "t.json", `{"teams":{"x":["a"],"y":["b","c"]}}`))
	emitMetricsGitHub(&buf, ".", r, cs.Detect(g), tm.Conway(g))
	out := buf.String()
	for _, want := range []string{
		"::notice title=archlint metrics::",        // coupling table
		"::error title=archlint instability::",     // instability breach
		"::error title=archlint SDP::",             // SDP breach
		"::error title=archlint context::",         // context breach
		"::notice title=archlint conway::Mismatch", // Conway index
	} {
		if !strings.Contains(out, want) {
			t.Errorf("github output missing %q:\n%s", want, out)
		}
	}
}

func TestEmitMetricsGitHub_NoTeamsAndEmptyConway(t *testing.T) {
	var buf bytes.Buffer
	r := metrics.Analyze([]string{"a"}, nil, metrics.Options{MaxInstability: -1})
	emitMetricsGitHub(&buf, ".", r, nil, nil)
	if !strings.Contains(buf.String(), "::notice title=archlint conway::skipped") {
		t.Errorf("nil Conway → skip notice:\n%s", buf.String())
	}

	buf.Reset()
	// An owned-but-edgeless graph yields a HasEdges=false Conway report (the n/a branch).
	dir := t.TempDir()
	tm, _ := metrics.LoadTeams(writeJSON(t, dir, "t.json", `{"teams":{"x":["zzz"]}}`))
	g := &arch.Graph{Layers: []string{"a"}, Edges: nil}
	emitMetricsGitHub(&buf, ".", r, nil, tm.Conway(g))
	if !strings.Contains(buf.String(), "n/a (no owned cross-layer edges)") {
		t.Errorf("edgeless owned Conway → n/a notice:\n%s", buf.String())
	}
}

func TestEmitMetricsText_InstabilityGateOKAndNoContextViolations(t *testing.T) {
	var buf bytes.Buffer
	g := &arch.Graph{Layers: []string{"a", "b"},
		Edges: []arch.Edge{{From: "a", To: "b",
			Imports: []arch.ImportEdge{{File: "a.go", Line: 1, FromLayer: "a", ToLayer: "b"}}}}}
	r := metrics.AnalyzeGraph(g, metrics.Options{MaxInstability: 1.0})
	dir := t.TempDir()
	// a→b targets b which is public → no context violation; gate 1.0 is met.
	cs, _ := metrics.LoadContexts(writeJSON(t, dir, "c.json",
		`{"contexts":[{"name":"f","layers":["a"]},{"name":"k","layers":["b"],"public":["b"]}]}`))
	tm, _ := metrics.LoadTeams(writeJSON(t, dir, "t.json", `{"teams":{"x":["a"],"y":["b"]}}`))
	emitMetricsText(&buf, ".", r, cs, cs.Detect(g), tm.Conway(g))
	out := buf.String()
	if !strings.Contains(out, "Instability gate (<= 1.00): OK.") {
		t.Errorf("met gate should print OK:\n%s", out)
	}
	if !strings.Contains(out, "No cross-context API violations.") {
		t.Errorf("clean contexts should print the no-violations line:\n%s", out)
	}
}

func TestEmitConwayText_NA(t *testing.T) {
	var buf bytes.Buffer
	dir := t.TempDir()
	tm, _ := metrics.LoadTeams(writeJSON(t, dir, "t.json", `{"teams":{"x":["zzz"]}}`))
	g := &arch.Graph{Layers: []string{"a"}, Edges: nil}
	emitConwayText(&buf, tm.Conway(g))
	if !strings.Contains(buf.String(), "n/a (no cross-layer edge") {
		t.Errorf("edgeless owned Conway text should be n/a:\n%s", buf.String())
	}
}

// writeJSON writes a JSON fixture and returns its path.
func writeJSON(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	write(t, p, body)
	return p
}
