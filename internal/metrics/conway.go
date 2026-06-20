// SPDX-License-Identifier: MIT

package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/muhammetsafak/archlint/internal/arch"
)

// TeamMap is the org's ownership of layers, parsed from a teams.json:
//
//	{ "teams": { "payments": ["domain", "ledger"], "platform": ["db", "http"] } }
//
// Each layer should be owned by at most one team. A layer owned by no team is "unowned" and is
// excluded from the Conway computation (the index is only defined over owned edges).
type TeamMap struct {
	Teams map[string][]string `json:"teams"`

	ownerOf map[string]string // layer -> team (derived)
}

// ConwayReport is the Conway Mismatch analysis. The index quantifies how much the code's
// dependency structure cuts across the org's team structure — Conway's law says the two tend to
// mirror each other, so a high index flags a design that fights the org chart (or vice versa).
type ConwayReport struct {
	// Index is the global Conway Mismatch Index: of all distinct cross-layer import edges whose
	// *both* endpoints are owned by a team, the fraction that cross a team boundary. In [0,1];
	// 0 = every dependency stays within one team, 1 = every dependency crosses teams. NaN-free:
	// when there are no owned edges, Index is 0 and HasEdges is false.
	Index    float64
	HasEdges bool // false when no edge has both endpoints owned (index is not meaningful)

	OwnedEdges    int // |E|: distinct cross-layer edges with both endpoints owned
	CrossingEdges int // |X|: of those, the ones whose endpoints belong to different teams

	// PerLayer is the per-layer mismatch: for each owned layer, the fraction of its owned
	// incident edges (in or out) that cross a team boundary. Sorted by layer name.
	PerLayer []LayerMismatch
}

// LayerMismatch is one layer's local Conway mismatch.
type LayerMismatch struct {
	Layer         string
	Team          string
	IncidentEdges int     // owned edges touching this layer (in or out)
	CrossingEdges int     // of those, the ones crossing a team boundary
	Mismatch      float64 // CrossingEdges / IncidentEdges, 0 when none
}

// LoadTeams reads and validates a teams.json at path. A missing file is not an error: it
// returns (nil, nil) so the Conway analysis is skipped (graceful degradation — never errors
// when ownership is simply absent).
func LoadTeams(path string) (*TeamMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no ownership mapping → skip Conway
		}
		return nil, fmt.Errorf("teams: read %s: %w", path, err)
	}
	var tm TeamMap
	if err := json.Unmarshal(data, &tm); err != nil {
		return nil, fmt.Errorf("teams: parse %s: %w", path, err)
	}
	if err := tm.validate(); err != nil {
		return nil, err
	}
	tm.index()
	return &tm, nil
}

func (tm *TeamMap) validate() error {
	if len(tm.Teams) == 0 {
		return fmt.Errorf("teams: at least one team is required")
	}
	owner := map[string]string{}
	for team, layers := range tm.Teams {
		if team == "" {
			return fmt.Errorf("teams: a team has an empty name")
		}
		if len(layers) == 0 {
			return fmt.Errorf("teams: team %q owns no layers", team)
		}
		for _, l := range layers {
			if prev, dup := owner[l]; dup {
				return fmt.Errorf("teams: layer %q is owned by both %q and %q (a layer must have one owner)", l, prev, team)
			}
			owner[l] = team
		}
	}
	return nil
}

func (tm *TeamMap) index() {
	tm.ownerOf = map[string]string{}
	for team, layers := range tm.Teams {
		for _, l := range layers {
			tm.ownerOf[l] = team
		}
	}
}

// OwnerOf returns the team that owns a layer, or "" when it is unowned.
func (tm *TeamMap) OwnerOf(layer string) string {
	if tm == nil {
		return ""
	}
	return tm.ownerOf[layer]
}

// Conway computes the Conway Mismatch Index over a graph's distinct cross-layer edges. A nil
// TeamMap (no ownership mapping present) yields a nil report — the caller skips Conway output,
// never errors.
func (tm *TeamMap) Conway(g *arch.Graph) *ConwayReport {
	if tm == nil {
		return nil
	}

	// Per-layer accumulators, keyed by layer; only owned layers are tracked.
	type acc struct {
		team     string
		incident int
		crossing int
	}
	per := map[string]*acc{}
	ensure := func(layer string) *acc {
		a, ok := per[layer]
		if !ok {
			a = &acc{team: tm.ownerOf[layer]}
			per[layer] = a
		}
		return a
	}

	owned, crossing := 0, 0
	for _, e := range g.Edges {
		ft, ok1 := tm.ownerOf[e.From]
		tt, ok2 := tm.ownerOf[e.To]
		if !ok1 || !ok2 {
			continue // an unowned endpoint → edge excluded from the index
		}
		owned++
		isCross := ft != tt

		af := ensure(e.From)
		at := ensure(e.To)
		af.incident++
		at.incident++
		if isCross {
			crossing++
			af.crossing++
			at.crossing++
		}
	}

	rep := &ConwayReport{
		HasEdges:      owned > 0,
		OwnedEdges:    owned,
		CrossingEdges: crossing,
	}
	if owned > 0 {
		rep.Index = float64(crossing) / float64(owned)
	}

	layers := make([]string, 0, len(per))
	for l := range per {
		layers = append(layers, l)
	}
	sort.Strings(layers)
	for _, l := range layers {
		a := per[l]
		m := 0.0
		if a.incident > 0 {
			m = float64(a.crossing) / float64(a.incident)
		}
		rep.PerLayer = append(rep.PerLayer, LayerMismatch{
			Layer:         l,
			Team:          a.team,
			IncidentEdges: a.incident,
			CrossingEdges: a.crossing,
			Mismatch:      m,
		})
	}
	return rep
}
