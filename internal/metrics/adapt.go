// SPDX-License-Identifier: MIT

package metrics

import (
	"fmt"
	"strings"

	"github.com/muhammetsafak/archlint/internal/arch"
)

// edgesFromGraph adapts an arch.Graph's distinct cross-layer edges into the metrics edge shape,
// attaching a compact "file:line" provenance string (capped to keep reports readable).
func edgesFromGraph(g *arch.Graph) []edge {
	out := make([]edge, 0, len(g.Edges))
	for _, e := range g.Edges {
		out = append(out, edge{From: e.From, To: e.To, Detail: edgeDetail(e)})
	}
	return out
}

// edgeDetail renders up to three "file:line" sites for an edge, with a "+N more" suffix when
// the dependency is realised by many imports — enough to locate it without flooding the report.
func edgeDetail(e arch.Edge) string {
	const max = 3
	var sites []string
	for i, ie := range e.Imports {
		if i >= max {
			sites = append(sites, fmt.Sprintf("+%d more", len(e.Imports)-max))
			break
		}
		sites = append(sites, fmt.Sprintf("%s:%d", ie.File, ie.Line))
	}
	return strings.Join(sites, ", ")
}

// AnalyzeGraph runs the full coupling analysis on an arch.Graph. It is the package's main entry
// for callers that already built the graph (cmd/archlint), keeping the low-level Analyze free
// of an arch dependency for unit testing on synthetic edges.
func AnalyzeGraph(g *arch.Graph, opts Options) *Report {
	return Analyze(g.Layers, edgesFromGraph(g), opts)
}
