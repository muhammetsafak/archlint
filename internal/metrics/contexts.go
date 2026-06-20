// SPDX-License-Identifier: MIT

package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/muhammetsafak/archlint/internal/arch"
)

// Context is a DDD bounded context: a named group of layers that owns its model and exposes a
// narrow public surface (an API/port). Other contexts may import only the public layers; any
// import that lands on a non-public ("internal") layer of another context bypasses the port —
// a boundary breach.
type Context struct {
	Name string `json:"name"`
	// Layers are the layer names that belong to this context.
	Layers []string `json:"layers"`
	// Public names the subset of Layers that form the context's API/port — the only layers
	// another context may import. A layer in this context but absent from Public is internal.
	// An empty Public means the context publishes nothing: every inbound cross-context import
	// is a breach (declare a port to open it).
	Public []string `json:"public,omitempty"`
	// Abstract optionally marks layers of this context as abstract for the A metric; it is a
	// convenience so contexts.json can also carry abstractness. Names must be in Layers.
	Abstract []string `json:"abstract,omitempty"`
}

// ContextSet is a parsed contexts.json: a list of bounded contexts over the same layer names
// the architecture.json declares.
type ContextSet struct {
	Contexts []Context `json:"contexts"`

	// derived indexes (built by index()):
	contextOf map[string]string          // layer -> context name
	publicOf  map[string]map[string]bool // context -> set of its public layers
}

// ContextViolation is a cross-context import that bypasses the target context's public API: a
// layer in context A imports a non-public layer of context B.
type ContextViolation struct {
	FromContext string
	ToContext   string
	FromLayer   string
	ToLayer     string
	Detail      string // "file:line" provenance, as for SDP
}

// LoadContexts reads and validates a contexts.json at path. A missing file is not an error: it
// returns (nil, nil) so the bounded-context analysis is simply skipped (opt-in).
func LoadContexts(path string) (*ContextSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no contexts declared → no bounded-context analysis
		}
		return nil, fmt.Errorf("contexts: read %s: %w", path, err)
	}
	var cs ContextSet
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("contexts: parse %s: %w", path, err)
	}
	if err := cs.validate(); err != nil {
		return nil, err
	}
	cs.index()
	return &cs, nil
}

func (cs *ContextSet) validate() error {
	if len(cs.Contexts) == 0 {
		return fmt.Errorf("contexts: at least one context is required")
	}
	seenLayer := map[string]string{} // layer -> owning context, to catch overlaps
	seenContext := map[string]bool{}
	for _, c := range cs.Contexts {
		if c.Name == "" {
			return fmt.Errorf("contexts: a context has an empty name")
		}
		if seenContext[c.Name] {
			return fmt.Errorf("contexts: duplicate context %q", c.Name)
		}
		seenContext[c.Name] = true
		if len(c.Layers) == 0 {
			return fmt.Errorf("contexts: context %q has no layers", c.Name)
		}
		inThis := map[string]bool{}
		for _, l := range c.Layers {
			if owner, dup := seenLayer[l]; dup {
				return fmt.Errorf("contexts: layer %q is in both %q and %q (contexts must not overlap)", l, owner, c.Name)
			}
			seenLayer[l] = c.Name
			inThis[l] = true
		}
		for _, p := range c.Public {
			if !inThis[p] {
				return fmt.Errorf("contexts: context %q lists public layer %q that is not one of its layers", c.Name, p)
			}
		}
		for _, a := range c.Abstract {
			if !inThis[a] {
				return fmt.Errorf("contexts: context %q lists abstract layer %q that is not one of its layers", c.Name, a)
			}
		}
	}
	return nil
}

func (cs *ContextSet) index() {
	cs.contextOf = map[string]string{}
	cs.publicOf = map[string]map[string]bool{}
	for _, c := range cs.Contexts {
		cs.publicOf[c.Name] = map[string]bool{}
		for _, l := range c.Layers {
			cs.contextOf[l] = c.Name
		}
		for _, p := range c.Public {
			cs.publicOf[c.Name][p] = true
		}
	}
}

// AbstractLayers returns the union of every context's Abstract list, for feeding Options.Abstract
// so contexts.json can double as the abstractness declaration.
func (cs *ContextSet) AbstractLayers() map[string]bool {
	out := map[string]bool{}
	if cs == nil {
		return out
	}
	for _, c := range cs.Contexts {
		for _, a := range c.Abstract {
			out[a] = true
		}
	}
	return out
}

// Detect flags cross-context imports that bypass the target context's public API. An import
// from layer X to layer Y is a breach when X and Y are in different declared contexts and Y is
// not in the target context's Public set. Layers in no declared context are ignored (the
// contexts.json need not cover every layer). Results are sorted by (FromContext, ToContext,
// FromLayer, ToLayer).
func (cs *ContextSet) Detect(g *arch.Graph) []ContextViolation {
	if cs == nil {
		return nil
	}
	var out []ContextViolation
	for _, e := range g.Edges {
		fromCtx, ok1 := cs.contextOf[e.From]
		toCtx, ok2 := cs.contextOf[e.To]
		if !ok1 || !ok2 || fromCtx == toCtx {
			continue // same context, or one side is uncontexted
		}
		if cs.publicOf[toCtx][e.To] {
			continue // imports the published port — allowed
		}
		out = append(out, ContextViolation{
			FromContext: fromCtx,
			ToContext:   toCtx,
			FromLayer:   e.From,
			ToLayer:     e.To,
			Detail:      edgeDetail(e),
		})
	}
	sort.Slice(out, func(i, j int) bool { return ctxLess(out[i], out[j]) })
	return out
}

func ctxLess(a, b ContextViolation) bool {
	for _, pair := range [][2]string{
		{a.FromContext, b.FromContext},
		{a.ToContext, b.ToContext},
		{a.FromLayer, b.FromLayer},
		{a.ToLayer, b.ToLayer},
	} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return false
}

// ContextSummary is a one-line human description of a context for the report header.
func (c Context) ContextSummary() string {
	pub := "none"
	if len(c.Public) > 0 {
		pub = strings.Join(c.Public, ", ")
	}
	return fmt.Sprintf("%s {layers: %s; public: %s}", c.Name, strings.Join(c.Layers, ", "), pub)
}
