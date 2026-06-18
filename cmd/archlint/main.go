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

	"github.com/muhammetsafak/archlint/internal/arch"
	"github.com/muhammetsafak/archlint/internal/config"
)

const usage = `archlint — enforce architectural import boundaries from architecture.json

Usage:
  archlint check [--config architecture.json] [--format text|github] [dir]
      Exit 1 on any import that crosses a forbidden layer boundary.
      --format defaults to "github" (inline PR annotations) under GitHub Actions, else "text".

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
	format := fs.String("format", "", "output format: text or github (default: github under GitHub Actions, else text)")
	_ = fs.Parse(args)

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	mode := *format
	if mode == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			mode = "github"
		} else {
			mode = "text"
		}
	}
	if mode != "text" && mode != "github" {
		return fail("unknown --format %q (want text or github)", mode)
	}

	// A relative --config is looked up in the working directory first, then inside the
	// scanned dir — so `archlint check ./service` finds `./service/architecture.json`.
	cfgFile := *cfgPath
	if !filepath.IsAbs(cfgFile) {
		if _, err := os.Stat(cfgFile); err != nil {
			cfgFile = filepath.Join(dir, *cfgPath)
		}
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fail("%v", err)
	}
	violations, err := arch.Lint(dir, cfg)
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
		fmt.Fprintf(w, "  %s:%d  %s → %s is not allowed  (import %q)\n",
			v.File, v.Line, v.FromLayer, v.ToLayer, v.Import)
	}
	fmt.Fprintf(w, "\nHow to fix: %s.\n", remediation)
}

// emitGitHub writes GitHub Actions workflow commands, which render as inline annotations on
// the PR diff (one ::error per violation, with file/line), plus a summary ::notice.
func emitGitHub(w io.Writer, dir string, violations []arch.Violation) {
	for _, v := range violations {
		file := filepath.ToSlash(filepath.Join(dir, v.File))
		fmt.Fprintf(w, "::error file=%s,line=%d,title=archlint::%s → %s is not allowed (import %q)\n",
			file, v.Line, v.FromLayer, v.ToLayer, v.Import)
	}
	if len(violations) == 0 {
		fmt.Fprintln(w, "archlint: no architecture violations.")
		return
	}
	fmt.Fprintf(w, "::notice title=archlint::%d boundary violation(s) — fix: %s.\n",
		len(violations), remediation)
}

func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "archlint: "+format+"\n", args...)
	return 2
}
