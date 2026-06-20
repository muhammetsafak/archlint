// Command archlint enforces architectural import boundaries declared in an
// architecture.json: it maps every Go, TypeScript, and Python file and its imports to a
// layer and fails the build on any import that crosses a forbidden boundary. Deterministic,
// dependency-free, git-native — the "executable architecture" gate that runs in CI.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/muhammetsafak/archlint/internal/adr"
	"github.com/muhammetsafak/archlint/internal/arch"
	"github.com/muhammetsafak/archlint/internal/config"
	"github.com/muhammetsafak/archlint/internal/metrics"
)

const usage = `archlint — enforce architectural import boundaries from architecture.json

Usage:
  archlint check [--config architecture.json] [--adr-dir docs/adr] [--format text|github] [dir]
      Exit 1 on any import that crosses a forbidden layer boundary.
      --format defaults to "github" (inline PR annotations) under GitHub Actions, else "text".
      --adr-dir points at a directory of ADR markdown files whose ` + "```archlint" + ` blocks
        declare extra layer/allow/deny rules that merge with architecture.json (executable
        ADR). When the directory is absent, behavior is unchanged.

  archlint metrics [--config architecture.json] [--adr-dir docs/adr] [--format text|github]
                   [--contexts contexts.json] [--teams teams.json]
                   [--max-instability F] [--fail-on-violation] [dir]
      Report coupling + Conway/DDD signals on the same import graph (read-only; never
      changes a lint verdict). Computes per-layer afferent/efferent coupling, instability
      I=Ce/(Ca+Ce), abstractness/distance, and flags Stable-Dependencies-Principle breaches.
      With --contexts, flags cross-context imports that bypass a context's public API.
      With --teams (or teams.json), reports the Conway Mismatch Index; absent ownership is a
      no-op, never an error. Exit 1 when a gated breach is found (see --fail-on-violation,
      --max-instability); exit 0 otherwise.

architecture.json declares layers (as repo-relative path prefixes) and the dependency
directions allowed between them. archlint maps every Go, TypeScript, and Python file and its
imports to a layer and reports the ones that break the rules.`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:])
	case "metrics":
		return runMetrics(args[1:])
	case "-h", "--help", "help":
		fmt.Println(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "archlint: unknown command %q\n\n%s\n", args[0], usage)
		return 2
	}
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	cfgPath := fs.String("config", "architecture.json", "path to the architecture config")
	adrDir := fs.String("adr-dir", "docs/adr", "directory of ADR markdown files whose ```archlint blocks add rules (skipped if absent)")
	format := fs.String("format", "", "output format: text or github (default: github under GitHub Actions, else text)")
	_ = fs.Parse(args)

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	mode, ok := resolveFormat(*format)
	if !ok {
		return fail("unknown --format %q (want text or github)", *format)
	}

	cfg, cfgFile, rules, ferr := loadConfigAndADRs(dir, *cfgPath, *adrDir)
	if ferr != "" {
		return fail("%s", ferr)
	}

	violations, err := arch.Lint(dir, cfg, rules)
	if err != nil {
		return fail("lint: %v", err)
	}

	if mode == "github" {
		emitGitHub(os.Stdout, dir, violations)
	} else {
		emitText(os.Stdout, dir, cfgFile, len(cfg.Layers), violations)
	}
	if len(violations) > 0 {
		return 1
	}
	return 0
}

// remediation is the one-line fix guidance shared by both output formats.
const remediation = "invert the dependency (depend on an interface the lower layer owns), " +
	"move the shared code into a layer both may import, or — if the edge is intentional — " +
	"allow it in architecture.json"

// emitText writes the human-readable report.
func emitText(w io.Writer, dir, cfgFile string, layerCount int, violations []arch.Violation) {
	fmt.Fprintf(w, "Scanned %s against %s — %d layer(s).\n", dir, cfgFile, layerCount)
	if len(violations) == 0 {
		fmt.Fprintln(w, "No architecture violations.")
		return
	}
	fmt.Fprintf(w, "\n%d boundary violation(s):\n", len(violations))
	for _, v := range violations {
		fmt.Fprintf(w, "  %s:%d  %s → %s is not allowed  (import %q)%s\n",
			v.File, v.Line, v.FromLayer, v.ToLayer, v.Import, sourceSuffix(v.Source))
	}
	fmt.Fprintf(w, "\nHow to fix: %s.\n", remediation)
}

// emitGitHub writes GitHub Actions workflow commands, which render as inline annotations on
// the PR diff (one ::error per violation, with file/line), plus a summary ::notice.
func emitGitHub(w io.Writer, dir string, violations []arch.Violation) {
	for _, v := range violations {
		file := filepath.ToSlash(filepath.Join(dir, v.File))
		fmt.Fprintf(w, "::error file=%s,line=%d,title=archlint::%s → %s is not allowed (import %q)%s\n",
			file, v.Line, v.FromLayer, v.ToLayer, v.Import, sourceSuffix(v.Source))
	}
	if len(violations) == 0 {
		fmt.Fprintln(w, "archlint: no architecture violations.")
		return
	}
	fmt.Fprintf(w, "::notice title=archlint::%d boundary violation(s) — fix: %s.\n",
		len(violations), remediation)
}

// sourceSuffix annotates a violation with the ADR that declared the broken rule, for
// traceability — "" when the rule came from architecture.json.
func sourceSuffix(source string) string {
	if source == "" {
		return ""
	}
	return fmt.Sprintf("  [%s]", source)
}

// resolveFormat turns the --format flag into a concrete mode, defaulting to github under
// GitHub Actions (inline annotations) and text otherwise. ok is false for an unknown value.
func resolveFormat(flagVal string) (string, bool) {
	mode := flagVal
	if mode == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			mode = "github"
		} else {
			mode = "text"
		}
	}
	if mode != "text" && mode != "github" {
		return "", false
	}
	return mode, true
}

// resolvePath resolves a possibly-relative config path the way archlint does everywhere: a
// relative path is looked up in the working directory first, then inside the scanned dir — so
// `archlint <cmd> ./service` finds `./service/<name>`. Absolute paths pass through.
func resolvePath(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(p); err != nil {
		return filepath.Join(dir, p)
	}
	return p
}

// loadConfigAndADRs loads architecture.json and merges any executable-ADR rules — the shared
// front half of both `check` and `metrics`. On failure it returns a non-empty error string
// (the caller turns it into a usage-error exit), so behaviour matches the original inline code.
func loadConfigAndADRs(dir, cfgPath, adrDir string) (*config.Config, string, *adr.Ruleset, string) {
	cfgFile := resolvePath(dir, cfgPath)
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, "", nil, err.Error()
	}

	// Executable ADRs: same resolution as --config, so `archlint <cmd> ./service` finds
	// `./service/docs/adr`. An absent directory yields an empty ruleset (backward compatible).
	rules, err := adr.Scan(dir, resolvePath(dir, adrDir))
	if err != nil {
		return nil, "", nil, err.Error()
	}
	if err := rules.MergeInto(cfg); err != nil {
		return nil, "", nil, err.Error()
	}
	return cfg, cfgFile, rules, ""
}

// runMetrics computes and reports the coupling + Conway/DDD signals. It reuses the same config,
// ADR merge, and import graph as `check`, then layers the metrics on top. Exit semantics mirror
// archlint's: 2 on a usage/config error, 1 when a gated breach is found, 0 otherwise.
func runMetrics(args []string) int {
	fs := flag.NewFlagSet("metrics", flag.ExitOnError)
	cfgPath := fs.String("config", "architecture.json", "path to the architecture config")
	adrDir := fs.String("adr-dir", "docs/adr", "directory of ADR markdown files whose ```archlint blocks add rules (skipped if absent)")
	format := fs.String("format", "", "output format: text or github (default: github under GitHub Actions, else text)")
	contextsPath := fs.String("contexts", "contexts.json", "path to a bounded-context map (skipped if absent)")
	teamsPath := fs.String("teams", "teams.json", "path to a team-ownership map for the Conway index (skipped if absent)")
	maxInstability := fs.Float64("max-instability", -1, "fail when any layer's instability exceeds this 0..1 ceiling (negative disables the gate)")
	failOnViolation := fs.Bool("fail-on-violation", true, "exit 1 on a bounded-context breach or SDP violation")
	_ = fs.Parse(args)

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	mode, ok := resolveFormat(*format)
	if !ok {
		return fail("unknown --format %q (want text or github)", *format)
	}

	cfg, _, _, ferr := loadConfigAndADRs(dir, *cfgPath, *adrDir)
	if ferr != "" {
		return fail("%s", ferr)
	}

	// Optional bounded-context and ownership maps: absent files are a graceful no-op, not an
	// error, so `metrics` runs on a repo that declares neither.
	contexts, err := metrics.LoadContexts(resolvePath(dir, *contextsPath))
	if err != nil {
		return fail("%v", err)
	}
	teams, err := metrics.LoadTeams(resolvePath(dir, *teamsPath))
	if err != nil {
		return fail("%v", err)
	}

	graph, err := arch.BuildGraph(dir, cfg)
	if err != nil {
		return fail("metrics: %v", err)
	}

	report := metrics.AnalyzeGraph(graph, metrics.Options{
		Abstract:       contexts.AbstractLayers(),
		MaxInstability: *maxInstability,
	})
	ctxViolations := contexts.Detect(graph)
	conway := teams.Conway(graph)

	if mode == "github" {
		emitMetricsGitHub(os.Stdout, dir, report, ctxViolations, conway)
	} else {
		emitMetricsText(os.Stdout, dir, report, contexts, ctxViolations, conway)
	}

	// Gating: SDP breaches and context violations fail the build only when opted in; the
	// instability gate fails whenever it is set (>=0) and exceeded. The default run (no gate,
	// no contexts) therefore never fails — metrics is observational unless asked to enforce.
	breach := false
	if *failOnViolation && (len(report.SDPViolations) > 0 || len(ctxViolations) > 0) {
		breach = true
	}
	if len(report.OverInstable) > 0 {
		breach = true
	}
	if breach {
		return 1
	}
	return 0
}

// emitMetricsText writes the human-readable metrics report.
func emitMetricsText(w io.Writer, dir string, r *metrics.Report, cs *metrics.ContextSet, ctxV []metrics.ContextViolation, conway *metrics.ConwayReport) {
	fmt.Fprintf(w, "Coupling metrics for %s\n\n", dir)
	fmt.Fprintf(w, "  %-16s %3s %3s %6s %6s %6s\n", "layer", "Ca", "Ce", "I", "A", "D")
	for _, c := range r.Couplings {
		fmt.Fprintf(w, "  %-16s %3d %3d %6.2f %6.2f %6.2f\n",
			c.Layer, c.Ca, c.Ce, c.Instability, c.Abstractness, c.Distance)
	}
	fmt.Fprintln(w, "\n  Ca=afferent (incoming) Ce=efferent (outgoing) I=Ce/(Ca+Ce) A=abstractness D=|A+I-1|")

	if r.MaxInstability() >= 0 {
		if len(r.OverInstable) == 0 {
			fmt.Fprintf(w, "\nInstability gate (<= %.2f): OK.\n", r.MaxInstability())
		} else {
			fmt.Fprintf(w, "\n%d layer(s) over the instability ceiling %.2f:\n", len(r.OverInstable), r.MaxInstability())
			for _, c := range r.OverInstable {
				fmt.Fprintf(w, "  %s  I=%.2f\n", c.Layer, c.Instability)
			}
		}
	}

	if len(r.SDPViolations) > 0 {
		fmt.Fprintf(w, "\n%d Stable-Dependencies-Principle violation(s) (a stable layer depends on a less-stable one):\n", len(r.SDPViolations))
		for _, v := range r.SDPViolations {
			fmt.Fprintf(w, "  %s (I=%.2f) → %s (I=%.2f)  at %s\n", v.From, v.FromI, v.To, v.ToI, v.Detail)
		}
	}

	if cs != nil {
		fmt.Fprintf(w, "\nBounded contexts: %d declared.\n", len(cs.Contexts))
		if len(ctxV) == 0 {
			fmt.Fprintln(w, "No cross-context API violations.")
		} else {
			fmt.Fprintf(w, "%d cross-context violation(s) (an import bypasses the target context's public API):\n", len(ctxV))
			for _, v := range ctxV {
				fmt.Fprintf(w, "  %s/%s → %s/%s  is not a published port  at %s\n",
					v.FromContext, v.FromLayer, v.ToContext, v.ToLayer, v.Detail)
			}
		}
	}

	emitConwayText(w, conway)
}

// emitConwayText writes the Conway section, or a one-line skip note when no ownership mapping
// was supplied (graceful degradation).
func emitConwayText(w io.Writer, conway *metrics.ConwayReport) {
	if conway == nil {
		fmt.Fprintln(w, "\nConway Mismatch Index: skipped (no teams mapping).")
		return
	}
	if !conway.HasEdges {
		fmt.Fprintln(w, "\nConway Mismatch Index: n/a (no cross-layer edge has both endpoints owned).")
		return
	}
	fmt.Fprintf(w, "\nConway Mismatch Index: %.2f  (%d of %d owned cross-layer edges cross a team boundary)\n",
		conway.Index, conway.CrossingEdges, conway.OwnedEdges)
	for _, l := range conway.PerLayer {
		fmt.Fprintf(w, "  %-16s team=%-12s mismatch=%.2f  (%d/%d incident edges cross)\n",
			l.Layer, l.Team, l.Mismatch, l.CrossingEdges, l.IncidentEdges)
	}
}

// emitMetricsGitHub writes GitHub Actions workflow commands: one ::error per gated breach
// (instability, SDP, context) so they surface in the PR check, plus ::notice summaries for the
// coupling table and the Conway index. There is no file/line for a layer-level signal, so the
// annotations are repo-level notices/errors rather than inline diff annotations.
func emitMetricsGitHub(w io.Writer, _ string, r *metrics.Report, ctxV []metrics.ContextViolation, conway *metrics.ConwayReport) {
	for _, c := range r.Couplings {
		fmt.Fprintf(w, "::notice title=archlint metrics::%s Ca=%d Ce=%d I=%.2f A=%.2f D=%.2f\n",
			c.Layer, c.Ca, c.Ce, c.Instability, c.Abstractness, c.Distance)
	}
	for _, c := range r.OverInstable {
		fmt.Fprintf(w, "::error title=archlint instability::%s I=%.2f exceeds the ceiling %.2f\n",
			c.Layer, c.Instability, r.MaxInstability())
	}
	for _, v := range r.SDPViolations {
		fmt.Fprintf(w, "::error title=archlint SDP::%s (I=%.2f) depends on less-stable %s (I=%.2f) — at %s\n",
			v.From, v.FromI, v.To, v.ToI, v.Detail)
	}
	for _, v := range ctxV {
		fmt.Fprintf(w, "::error title=archlint context::%s/%s → %s/%s bypasses %s's public API — at %s\n",
			v.FromContext, v.FromLayer, v.ToContext, v.ToLayer, v.ToContext, v.Detail)
	}
	if conway == nil {
		fmt.Fprintln(w, "::notice title=archlint conway::skipped (no teams mapping)")
	} else if conway.HasEdges {
		fmt.Fprintf(w, "::notice title=archlint conway::Mismatch Index %.2f (%d/%d owned edges cross a team)\n",
			conway.Index, conway.CrossingEdges, conway.OwnedEdges)
	} else {
		fmt.Fprintln(w, "::notice title=archlint conway::n/a (no owned cross-layer edges)")
	}
}

func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "archlint: "+format+"\n", args...)
	return 2
}
