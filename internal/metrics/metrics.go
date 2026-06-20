// SPDX-License-Identifier: MIT

// Package metrics computes package-coupling and organisational signals on the inter-layer
// dependency graph archlint already builds (see internal/arch.BuildGraph). It is a read-only
// analysis layered on top of the same edges the linter checks — nothing here changes a lint
// verdict; it surfaces structural risk the boundary rules alone can't express.
//
// Three families of signal, all opt-in and backward-compatible:
//
//   - Coupling (Robert C. Martin's metrics): afferent coupling Ca, efferent coupling Ce,
//     instability I = Ce/(Ca+Ce), abstractness A, and distance from the main sequence
//     D = |A + I − 1|. The Stable-Dependencies Principle (SDP) is checked as an edge L→M with
//     I(L) < I(M): a more-stable layer depending on a less-stable one.
//   - Bounded contexts (DDD): contexts group layers; a cross-context import that lands on a
//     layer the target context did not publish as an API/port is flagged as a boundary breach
//     (see contexts.go).
//   - Conway Mismatch Index: how often a layer→layer import edge crosses a team-ownership
//     boundary (see conway.go).
package metrics

import "sort"

// Coupling is the per-layer coupling profile.
type Coupling struct {
	Layer string
	// Ca (afferent) — the number of distinct other layers that import this layer (incoming).
	Ca int
	// Ce (efferent) — the number of distinct other layers this layer imports (outgoing).
	Ce int
	// Instability I = Ce/(Ca+Ce) in [0,1]; 0 = maximally stable (only depended upon), 1 =
	// maximally unstable (only depends on others). A layer with no edges has I=0 by convention.
	Instability float64
	// Abstractness A in [0,1]: the fraction of the layer that is abstract. archlint has no
	// per-symbol abstractness data, so A is taken from an explicit declaration (a context's
	// "abstract" layers, see Options) and is 1.0 or 0.0 — documented as a coarse proxy.
	Abstractness float64
	// Distance D = |A + I − 1|: distance from the "main sequence" (the A+I=1 line of balanced
	// design). 0 is ideal; near 1 is the zone of pain (stable+concrete) or uselessness
	// (unstable+abstract).
	Distance float64
}

// SDPViolation is an edge L→M where the importer L is more stable than its dependency M
// (I(L) < I(M)) — a Stable-Dependencies-Principle breach: stable code should not hang off
// less-stable code, because the unstable side will drag changes into the stable one.
type SDPViolation struct {
	From   string
	To     string
	FromI  float64
	ToI    float64
	Detail string // the concrete imports realising the edge, "file:line" joined for the report
}

// Options tunes the analysis. The zero value is valid: no abstract layers, no instability gate.
type Options struct {
	// Abstract is the set of layer names declared abstract (A=1.0); all others are A=0.0.
	Abstract map[string]bool
	// MaxInstability, when >= 0, is the per-layer instability ceiling: a layer whose I exceeds
	// it is reported (and can gate CI). A negative value disables the instability gate.
	MaxInstability float64
}

// Report is the full metrics result for one graph.
type Report struct {
	Couplings      []Coupling     // per-layer, sorted by layer name
	SDPViolations  []SDPViolation // sorted by (From, To)
	OverInstable   []Coupling     // layers whose I exceeds Options.MaxInstability, when gated
	maxInstability float64        // echoed for the report; <0 means the gate was off
}

// layerGraph is the cross-layer adjacency derived from arch edges: for each layer, the set of
// layers it imports (out) and the set that import it (in). Distinct-layer counts are Ce and Ca.
type layerGraph struct {
	layers []string
	out    map[string]map[string]bool
	in     map[string]map[string]bool
}

// edge is the minimal shape metrics needs from an arch.Edge: a distinct From→To layer pair.
// The caller adapts arch.Edge (and its underlying imports, for SDP detail) into these.
type edge struct {
	From, To string
	Detail   string // human-readable provenance for the edge (e.g. "file:line, file:line")
}

// buildLayerGraph indexes the distinct cross-layer edges into in/out adjacency sets.
func buildLayerGraph(layers []string, edges []edge) *layerGraph {
	lg := &layerGraph{
		layers: append([]string(nil), layers...),
		out:    map[string]map[string]bool{},
		in:     map[string]map[string]bool{},
	}
	for _, l := range layers {
		lg.out[l] = map[string]bool{}
		lg.in[l] = map[string]bool{}
	}
	for _, e := range edges {
		if e.From == e.To {
			continue
		}
		// A layer may appear on an edge without being a declared layer only if the graph and
		// the config disagree; guard so adjacency always has an initialised set.
		if lg.out[e.From] == nil {
			lg.out[e.From] = map[string]bool{}
		}
		if lg.in[e.To] == nil {
			lg.in[e.To] = map[string]bool{}
		}
		lg.out[e.From][e.To] = true
		lg.in[e.To][e.From] = true
	}
	sort.Strings(lg.layers)
	return lg
}

// instability computes I = Ce/(Ca+Ce); a layer with no coupling at all is defined as I=0
// (maximally stable: there is nothing to destabilise it).
func instability(ca, ce int) float64 {
	total := ca + ce
	if total == 0 {
		return 0
	}
	return float64(ce) / float64(total)
}

// distance computes D = |A + I − 1|, the distance from the main sequence.
func distance(a, i float64) float64 {
	d := a + i - 1
	if d < 0 {
		return -d
	}
	return d
}

// analyzeCoupling produces the per-layer coupling profiles from a layer graph.
func analyzeCoupling(lg *layerGraph, opts Options) []Coupling {
	out := make([]Coupling, 0, len(lg.layers))
	for _, l := range lg.layers {
		ca := len(lg.in[l])
		ce := len(lg.out[l])
		i := instability(ca, ce)
		a := 0.0
		if opts.Abstract[l] {
			a = 1.0
		}
		out = append(out, Coupling{
			Layer:        l,
			Ca:           ca,
			Ce:           ce,
			Instability:  i,
			Abstractness: a,
			Distance:     distance(a, i),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Layer < out[j].Layer })
	return out
}

// detectSDP flags edges that break the Stable-Dependencies Principle: I(from) < I(to). The
// instability of each layer is read from the computed couplings.
func detectSDP(lg *layerGraph, couplings []Coupling, edges []edge) []SDPViolation {
	iByLayer := map[string]float64{}
	for _, c := range couplings {
		iByLayer[c.Layer] = c.Instability
	}
	// Collapse edges to distinct From→To, keeping the first Detail seen for the report.
	seen := map[string]string{}
	var keys []string
	for _, e := range edges {
		if e.From == e.To {
			continue
		}
		k := e.From + "\x00" + e.To
		if _, ok := seen[k]; !ok {
			seen[k] = e.Detail
			keys = append(keys, k)
		}
	}

	var out []SDPViolation
	for _, k := range keys {
		var from, to string
		for j := 0; j < len(k); j++ {
			if k[j] == 0 {
				from, to = k[:j], k[j+1:]
				break
			}
		}
		fi, ti := iByLayer[from], iByLayer[to]
		if fi < ti {
			out = append(out, SDPViolation{From: from, To: to, FromI: fi, ToI: ti, Detail: seen[k]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// overInstable returns the layers whose instability exceeds opts.MaxInstability, or nil when
// the gate is disabled (MaxInstability < 0).
func overInstable(couplings []Coupling, opts Options) []Coupling {
	if opts.MaxInstability < 0 {
		return nil
	}
	var out []Coupling
	for _, c := range couplings {
		if c.Instability > opts.MaxInstability {
			out = append(out, c)
		}
	}
	return out
}

// Analyze runs the coupling analysis over a set of distinct cross-layer edges. layers is the
// full declared layer set (so a layer with zero edges still appears with Ca=Ce=0). This is the
// low-level entry; AnalyzeGraph adapts an arch.Graph onto it.
func Analyze(layers []string, edges []edge, opts Options) *Report {
	lg := buildLayerGraph(layers, edges)
	couplings := analyzeCoupling(lg, opts)
	return &Report{
		Couplings:      couplings,
		SDPViolations:  detectSDP(lg, couplings, edges),
		OverInstable:   overInstable(couplings, opts),
		maxInstability: opts.MaxInstability,
	}
}

// MaxInstability echoes the gate the report was computed with (<0 means no gate).
func (r *Report) MaxInstability() float64 { return r.maxInstability }
