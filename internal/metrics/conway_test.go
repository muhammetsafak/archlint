package metrics

import (
	"math"
	"path/filepath"
	"testing"
)

const goodTeams = `{"teams":{"commerce":["domain","app"],"platform":["ledger","gateway"]}}`

func TestLoadTeams_ConwayIndexOnFixture(t *testing.T) {
	dir := t.TempDir()
	tm, err := LoadTeams(writeFile(t, dir, "teams.json", goodTeams))
	if err != nil {
		t.Fatal(err)
	}
	// Same 4-edge graph as ctxGraph: app→domain, app→gateway, domain→ledger, gateway→ledger.
	// commerce={domain,app}, platform={ledger,gateway}. Crossing edges:
	//   app(commerce)→domain(commerce)   : same team
	//   app(commerce)→gateway(platform)  : CROSS
	//   domain(commerce)→ledger(platform): CROSS
	//   gateway(platform)→ledger(platform): same team
	// → 2 of 4 owned edges cross → index 0.5.
	rep := tm.Conway(ctxGraph())
	if rep == nil {
		t.Fatal("Conway report should not be nil when teams are present")
	}
	if !rep.HasEdges || rep.OwnedEdges != 4 || rep.CrossingEdges != 2 {
		t.Fatalf("owned/crossing = %d/%d (hasEdges=%v), want 4/2", rep.OwnedEdges, rep.CrossingEdges, rep.HasEdges)
	}
	if math.Abs(rep.Index-0.5) > 1e-9 {
		t.Errorf("Conway index = %.3f, want 0.5", rep.Index)
	}
	// Every layer here touches exactly 2 owned edges, 1 of which crosses → mismatch 0.5.
	for _, l := range rep.PerLayer {
		if l.IncidentEdges != 2 || l.CrossingEdges != 1 || math.Abs(l.Mismatch-0.5) > 1e-9 {
			t.Errorf("layer %s mismatch = %+v, want 1/2 cross", l.Layer, l)
		}
		if l.Team == "" {
			t.Errorf("layer %s should carry its owning team", l.Layer)
		}
	}
}

func TestConway_UnownedLayersExcluded(t *testing.T) {
	dir := t.TempDir()
	// Only domain and ledger are owned; app and gateway are not — so edges touching app/gateway
	// are excluded from the index. The single fully-owned edge is domain→ledger (crosses).
	tm, err := LoadTeams(writeFile(t, dir, "t.json",
		`{"teams":{"commerce":["domain"],"platform":["ledger"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	rep := tm.Conway(ctxGraph())
	if rep.OwnedEdges != 1 || rep.CrossingEdges != 1 {
		t.Fatalf("owned/crossing = %d/%d, want 1/1 (only domain→ledger fully owned)", rep.OwnedEdges, rep.CrossingEdges)
	}
	if math.Abs(rep.Index-1.0) > 1e-9 {
		t.Errorf("index = %.3f, want 1.0", rep.Index)
	}
}

func TestConway_NoOwnedEdges(t *testing.T) {
	dir := t.TempDir()
	// A team that owns a layer not present on any edge endpoint pair → no owned edges.
	tm, err := LoadTeams(writeFile(t, dir, "t.json", `{"teams":{"x":["nonexistent"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	rep := tm.Conway(ctxGraph())
	if rep.HasEdges {
		t.Errorf("no owned edges should set HasEdges=false, got %+v", rep)
	}
	if rep.Index != 0 {
		t.Errorf("no owned edges → index 0, got %.3f", rep.Index)
	}
}

func TestConway_NilTeamMapIsNoOp(t *testing.T) {
	var tm *TeamMap
	if tm.Conway(ctxGraph()) != nil {
		t.Errorf("a nil TeamMap must yield a nil Conway report (graceful skip)")
	}
	if tm.OwnerOf("anything") != "" {
		t.Errorf("a nil TeamMap OwnerOf should be empty")
	}
}

func TestLoadTeams_MissingFileIsNoOp(t *testing.T) {
	tm, err := LoadTeams(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a missing teams file must not error, got %v", err)
	}
	if tm != nil {
		t.Errorf("a missing teams file should yield a nil map (skip Conway), got %+v", tm)
	}
}

func TestLoadTeams_ValidationErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"no teams":        `{"teams":{}}`,
		"empty team name": `{"teams":{"":["a"]}}`,
		"team no layers":  `{"teams":{"x":[]}}`,
		"layer two teams": `{"teams":{"x":["a"],"y":["a"]}}`,
		"bad json":        `{`,
	}
	for name, body := range cases {
		p := writeFile(t, dir, "t.json", body)
		if _, err := LoadTeams(p); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

func TestOwnerOf(t *testing.T) {
	dir := t.TempDir()
	tm, err := LoadTeams(writeFile(t, dir, "t.json", goodTeams))
	if err != nil {
		t.Fatal(err)
	}
	if tm.OwnerOf("domain") != "commerce" {
		t.Errorf("OwnerOf(domain) = %q, want commerce", tm.OwnerOf("domain"))
	}
	if tm.OwnerOf("unknown") != "" {
		t.Errorf("OwnerOf(unknown) should be empty")
	}
}
