// Package config loads and validates an architecture.json: the declared layers (as
// repo-relative path prefixes) and the allowed dependency directions between them. It is
// the "architecture as code" the linter enforces — a small, explicit contract a human
// writes once and CI checks on every commit.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is a parsed architecture.json.
type Config struct {
	// Module is the Go module path, used to turn a Go import path into a repo-relative
	// one. Required only to govern Go imports; optional for TypeScript-only projects.
	Module string `json:"module,omitempty"`
	// Aliases maps TypeScript/JavaScript path-alias prefixes to repo-relative directories
	// (e.g. {"@/": "src/"}), mirroring tsconfig `paths`. Optional.
	Aliases map[string]string `json:"aliases,omitempty"`
	// Layers maps a layer name to the repo-relative path prefixes that belong to it.
	Layers map[string][]string `json:"layers"`
	// Rules maps a layer to the layers it is allowed to import (a same-layer import is
	// always allowed; an empty list means "may import no other layer").
	Rules map[string][]string `json:"rules"`
}

// Load reads, parses, and validates the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	// Module is optional (only Go needs it); a TS-only project declares aliases instead.
	if len(c.Layers) == 0 {
		return fmt.Errorf("config: at least one layer is required")
	}
	for alias, dest := range c.Aliases {
		if alias == "" || dest == "" {
			return fmt.Errorf("config: alias %q has an empty key or destination", alias)
		}
	}
	for name, prefixes := range c.Layers {
		if len(prefixes) == 0 {
			return fmt.Errorf("config: layer %q has no path prefixes", name)
		}
	}
	// Every layer must declare a rule (force explicitness), and every rule target must be
	// a real layer.
	for layer := range c.Layers {
		targets, ok := c.Rules[layer]
		if !ok {
			return fmt.Errorf("config: layer %q has no rule entry — declare what it may import, or []", layer)
		}
		for _, t := range targets {
			if _, known := c.Layers[t]; !known {
				return fmt.Errorf("config: rule for %q references unknown layer %q", layer, t)
			}
		}
	}
	for layer := range c.Rules {
		if _, ok := c.Layers[layer]; !ok {
			return fmt.Errorf("config: rule declared for unknown layer %q", layer)
		}
	}
	return nil
}

// LayerOf returns the layer a repo-relative path belongs to (longest-prefix match), or ""
// when the path is in no declared layer. Layer names are scanned in sorted order so ties
// resolve deterministically.
func (c *Config) LayerOf(repoPath string) string {
	repoPath = strings.TrimPrefix(filepath.ToSlash(repoPath), "./")

	names := make([]string, 0, len(c.Layers))
	for name := range c.Layers {
		names = append(names, name)
	}
	sort.Strings(names)

	best, bestLen := "", -1
	for _, name := range names {
		for _, p := range c.Layers[name] {
			p = strings.TrimSuffix(filepath.ToSlash(p), "/")
			if repoPath == p || strings.HasPrefix(repoPath, p+"/") {
				if len(p) > bestLen {
					best, bestLen = name, len(p)
				}
			}
		}
	}
	return best
}

// Allows reports whether a file in the `from` layer may import the `to` layer.
func (c *Config) Allows(from, to string) bool {
	if from == to {
		return true
	}
	for _, t := range c.Rules[from] {
		if t == to {
			return true
		}
	}
	return false
}
