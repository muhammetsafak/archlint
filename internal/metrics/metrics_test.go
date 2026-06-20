package metrics

import (
	"math"
	"testing"

	"github.com/muhammetsafak/archlint/internal/arch"
)

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// fixtureGraph builds a known 4-layer graph with hand-computable coupling:
//
//	app    → domain, gateway      (Ce=2, Ca=0, I=1.0)
//	domain → ledger               (Ce=1, Ca=1, I=0.5)  [app imports it]
//	gateway→ ledger               (Ce=1, Ca=1, I=0.5)  [app imports it]
//	ledger →                      (Ce=0, Ca=2, I=0.0)  [domain+gateway import it]
func fixtureGraph() *arch.Graph {
	imp := func(file string, line int, from, to string) arch.ImportEdge {
		return arch.ImportEdge{File: file, Line: line, FromLayer: from, ToLayer: to, Import: to}
	}
	return &arch.Graph{
		Layers: []string{"app", "domain", "gateway", "ledger"},
		Edges: []arch.Edge{
			{From: "app", To: "domain", Imports: []arch.ImportEdge{imp("app/s.go", 3, "app", "domain")}},
			{From: "app", To: "gateway", Imports: []arch.ImportEdge{imp("app/s.go", 4, "app", "gateway")}},
			{From: "domain", To: "ledger", Imports: []arch.ImportEdge{imp("domain/o.go", 7, "domain", "ledger")}},
			{From: "gateway", To: "ledger", Imports: []arch.ImportEdge{imp("gateway/g.go", 5, "gateway", "ledger")}},
		},
	}
}

func couplingByLayer(r *Report) map[string]Coupling {
	m := map[string]Coupling{}
	for _, c := range r.Couplings {
		m[c.Layer] = c
	}
	return m
}

func TestAnalyzeGraph_CouplingMetrics(t *testing.T) {
	r := AnalyzeGraph(fixtureGraph(), Options{MaxInstability: -1})
	by := couplingByLayer(r)

	cases := []struct {
		layer      string
		ca, ce     int
		i, a, dist float64
	}{
		{"app", 0, 2, 1.0, 0.0, 0.0},
		{"domain", 1, 1, 0.5, 0.0, 0.5},
		{"gateway", 1, 1, 0.5, 0.0, 0.5},
		{"ledger", 2, 0, 0.0, 0.0, 1.0},
	}
	for _, c := range cases {
		got := by[c.layer]
		if got.Ca != c.ca || got.Ce != c.ce {
			t.Errorf("%s: Ca/Ce = %d/%d, want %d/%d", c.layer, got.Ca, got.Ce, c.ca, c.ce)
		}
		if !almost(got.Instability, c.i) {
			t.Errorf("%s: I = %.3f, want %.3f", c.layer, got.Instability, c.i)
		}
		if !almost(got.Abstractness, c.a) {
			t.Errorf("%s: A = %.3f, want %.3f", c.layer, got.Abstractness, c.a)
		}
		if !almost(got.Distance, c.dist) {
			t.Errorf("%s: D = %.3f, want %.3f", c.layer, got.Distance, c.dist)
		}
	}
}

func TestAnalyzeGraph_AbstractnessAndDistance(t *testing.T) {
	// Mark domain abstract: A=1, I=0.5 → D = |1+0.5-1| = 0.5 (on the main sequence side).
	r := AnalyzeGraph(fixtureGraph(), Options{Abstract: map[string]bool{"domain": true}, MaxInstability: -1})
	by := couplingByLayer(r)
	if !almost(by["domain"].Abstractness, 1.0) {
		t.Errorf("domain A = %.3f, want 1.0", by["domain"].Abstractness)
	}
	if !almost(by["domain"].Distance, 0.5) {
		t.Errorf("domain D = %.3f, want 0.5", by["domain"].Distance)
	}
}

func TestAnalyze_IsolatedLayerHasZeroInstability(t *testing.T) {
	// A declared layer with no edges: Ca=Ce=0, I defined as 0 (maximally stable), D=|0+0-1|=1.
	g := &arch.Graph{Layers: []string{"lonely", "app", "domain"},
		Edges: []arch.Edge{{From: "app", To: "domain",
			Imports: []arch.ImportEdge{{File: "a.go", Line: 1, FromLayer: "app", ToLayer: "domain"}}}}}
	r := AnalyzeGraph(g, Options{MaxInstability: -1})
	by := couplingByLayer(r)
	if by["lonely"].Ca != 0 || by["lonely"].Ce != 0 {
		t.Fatalf("lonely should have no coupling, got %+v", by["lonely"])
	}
	if !almost(by["lonely"].Instability, 0) || !almost(by["lonely"].Distance, 1) {
		t.Errorf("lonely I/D = %.2f/%.2f, want 0/1", by["lonely"].Instability, by["lonely"].Distance)
	}
}

func TestDetectSDP_FlagsStableDependingOnUnstable(t *testing.T) {
	// Build a graph where a STABLE layer depends on an UNSTABLE one:
	//   stable (I=0.0 from one inbound, no outbound) ... we need stable to have an OUTBOUND edge,
	//   so: stable→mid, mid→stable makes both have edges. Construct explicitly:
	//   a→b, b→a, b→c. Instabilities:
	//     a: in={b}, out={b}     → I=0.5
	//     b: in={a}, out={a,c}   → I=2/3
	//     c: in={b}, out={}      → I=0.0
	//   Edges and SDP check (I(from) < I(to)):
	//     a→b: 0.5 < 0.667 → VIOLATION
	//     b→a: 0.667 < 0.5 → no
	//     b→c: 0.667 < 0.0 → no
	g := &arch.Graph{Layers: []string{"a", "b", "c"},
		Edges: []arch.Edge{
			{From: "a", To: "b", Imports: []arch.ImportEdge{{File: "a.go", Line: 1, FromLayer: "a", ToLayer: "b"}}},
			{From: "b", To: "a", Imports: []arch.ImportEdge{{File: "b.go", Line: 2, FromLayer: "b", ToLayer: "a"}}},
			{From: "b", To: "c", Imports: []arch.ImportEdge{{File: "b.go", Line: 3, FromLayer: "b", ToLayer: "c"}}},
		}}
	r := AnalyzeGraph(g, Options{MaxInstability: -1})
	if len(r.SDPViolations) != 1 {
		t.Fatalf("want exactly 1 SDP violation, got %d: %+v", len(r.SDPViolations), r.SDPViolations)
	}
	v := r.SDPViolations[0]
	if v.From != "a" || v.To != "b" {
		t.Errorf("SDP edge = %s→%s, want a→b", v.From, v.To)
	}
	if !(v.FromI < v.ToI) {
		t.Errorf("SDP must have FromI < ToI, got %.3f >= %.3f", v.FromI, v.ToI)
	}
	if v.Detail == "" {
		t.Errorf("SDP violation should carry a provenance detail")
	}
}

func TestOverInstable_GateRespectsThreshold(t *testing.T) {
	g := fixtureGraph()
	// app has I=1.0; a ceiling of 0.9 flags it, a negative ceiling disables the gate.
	gated := AnalyzeGraph(g, Options{MaxInstability: 0.9})
	if len(gated.OverInstable) != 1 || gated.OverInstable[0].Layer != "app" {
		t.Fatalf("ceiling 0.9 should flag app, got %+v", gated.OverInstable)
	}
	if gated.MaxInstability() != 0.9 {
		t.Errorf("MaxInstability echo = %.2f, want 0.9", gated.MaxInstability())
	}
	off := AnalyzeGraph(g, Options{MaxInstability: -1})
	if off.OverInstable != nil {
		t.Errorf("a negative ceiling must disable the gate, got %+v", off.OverInstable)
	}
}

func TestEdgeDetail_CapsManySites(t *testing.T) {
	var imps []arch.ImportEdge
	for i := 0; i < 5; i++ {
		imps = append(imps, arch.ImportEdge{File: "f.go", Line: i + 1, FromLayer: "a", ToLayer: "b"})
	}
	d := edgeDetail(arch.Edge{From: "a", To: "b", Imports: imps})
	if want := "f.go:1, f.go:2, f.go:3, +2 more"; d != want {
		t.Errorf("edgeDetail = %q, want %q", d, want)
	}
}
