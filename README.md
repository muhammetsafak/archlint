# archlint

> Enforce your architecture's import boundaries — deterministically, in CI, from a file.

You drew the layers once: the domain doesn't import infrastructure, the API doesn't reach
into the database directly, modules talk only through their allowed edges. Then, commit by
commit, a synchronous call sneaks in, a helper imports the wrong package, and the diagram on
the wiki quietly stops being true. **archlint** keeps it true.

You declare the layers and their allowed dependencies in an `architecture.json`. archlint
maps every Go, TypeScript, and Python file and every import to a layer and **fails the build
on any import that crosses a forbidden boundary** — no AI, no probabilistic guess, the same
answer every run.

## Install

```sh
go install github.com/muhammetsafak/archlint/cmd/archlint@latest
```

## architecture.json

```json
{
  "module": "github.com/acme/app",
  "layers": {
    "domain": ["internal/domain"],
    "db":     ["internal/db"],
    "http":   ["internal/http"]
  },
  "rules": {
    "domain": [],
    "db":     ["domain"],
    "http":   ["domain", "db"]
  }
}
```

- **`module`** — your Go module path, used to turn an import into a repo-relative path.
- **`layers`** — a layer name → the repo-relative path prefixes that belong to it
  (longest prefix wins, so `internal/db/migrations` can be its own sub-layer).
- **`rules`** — a layer → the layers it is **allowed** to import. A same-layer import is
  always allowed; `[]` means "may import no other layer." Every declared layer needs a rule
  (be explicit on purpose).

Here `domain` may import nothing internal, `db` may import `domain`, and `http` may import
both. Any other internal edge is a violation.

### TypeScript / JavaScript

For TS/JS, drop `module` and declare your path aliases instead. archlint resolves relative
imports against the importing file, and aliases the way your `tsconfig` `paths` would:

```json
{
  "aliases": { "@/": "src/" },
  "layers": { "domain": ["src/domain"], "db": ["src/db"] },
  "rules":  { "domain": [], "db": ["domain"] }
}
```

`.ts .tsx .mts .cts .js .jsx .mjs .cjs` are all scanned; bare (npm) imports are ignored.

### Python

Python needs no `module`: relative imports (`from . import x`, `from ..db import repo`) are
resolved against the importing file's package, and absolute imports map dotted→slash
(`app.db` → `app/db`). For a `src/` layout, add an alias (`{"app/": "src/app/"}`).

A single config can govern a mixed Go + TypeScript + Python repo at once.

## Executable ADRs

Architecture Decision Records are where a team *writes down* why a boundary exists — and then
the `architecture.json` somewhere else is what actually gets enforced. The two drift apart.
archlint lets the ADR **be** the rule: drop a fenced ` ```archlint ` block into an ADR and its
directives are compiled into the rule set, merged with `architecture.json`, and enforced in CI.

````markdown
# ADR 0007: The domain layer is infrastructure-free

The domain holds the business model and may not depend on persistence — so a refactor
can swap the database without touching a single domain type.

```archlint
# declare (or extend) a layer's repo-relative path prefixes
layer domain = internal/domain
layer db     = internal/db, internal/dao

# allowed and forbidden edges (same semantics as architecture.json's rules)
allow db -> domain      # the repository depends on the model
deny  domain -> db      # the model must never reach into persistence
```
````

The directives, one per line inside the block:

| Directive | Meaning |
| --------- | ------- |
| `layer <name> = <prefix>[, <prefix>...]` | declare or **extend** a layer's path prefixes |
| `allow <from> -> <to>` | `<from>` may import `<to>` |
| `deny <from> -> <to>` | `<from>` may **not** import `<to>` (the default — made explicit) |

`#` starts a comment (whole-line or trailing); blank lines are ignored. Prose outside the
` ```archlint ` block — including an `allow`-looking sentence — is never parsed.

Point archlint at the ADR directory with `--adr-dir` (default `docs/adr`); when it doesn't
exist, archlint behaves exactly as before, so adopting this is opt-in.

```sh
archlint check                          # also reads ./docs/adr if present
archlint check --adr-dir .ssot/adr ./svc
```

**How the two sources merge** — the ADR set and `architecture.json` are unioned:

- **layers** — an ADR may add prefixes to an existing layer or introduce a new one.
- **allow** — an edge allowed by *either* source is allowed.
- **deny** — authoritative. If an ADR `deny`s an edge that `architecture.json` (or another
  ADR) `allow`s, archlint **errors** with the ADR `file:line` and the conflicting rule rather
  than silently picking a winner — the whole point is that the record and the gate can't
  disagree.

Every violation reports the ADR it came from, so a failing check points straight at the
decision it broke:

```
internal/domain/bad.go:5  domain → db is not allowed  (import "…/internal/db")  [docs/adr/0007-domain-is-infra-free.md]
```

## Usage

```sh
archlint check                          # uses ./architecture.json, scans .
archlint check ./service                # scans ./service, finds ./service/architecture.json
archlint check --config arch.json ./svc
```

Try the bundled examples, each of which plants a deliberate violation:

```sh
archlint check examples/sample      # Go
# examples/sample/internal/domain/bad.go:5  domain → db is not allowed  (import "…/internal/db")

archlint check examples/ts-sample   # TypeScript (resolves the "@/" alias)
# examples/ts-sample/src/domain/bad.ts:4  domain → db is not allowed  (import "@/db/repo")

archlint check examples/py-sample   # Python (relative import)
# examples/py-sample/app/domain/bad.py:3  domain → db is not allowed  (import "..db.repo")

archlint check examples/adr-sample  # the deny rule lives in an ADR (executable ADR)
# examples/adr-sample/internal/domain/bad.go:6  domain → db is not allowed  (import "…/internal/db")  [docs/adr/0001-domain-is-infra-free.md]
```

### In CI

Use the bundled action — under GitHub Actions, violations show up as **inline annotations on
the PR diff** (file + line) and a failing check blocks the merge:

```yaml
- uses: muhammetsafak/archlint@v0.1.0
  with:
    config: architecture.json   # optional
    dir: .                      # optional
```

Or run the binary directly:

```yaml
- uses: actions/setup-go@v5
  with: { go-version: stable }
- run: go install github.com/muhammetsafak/archlint/cmd/archlint@latest
- run: archlint check
```

`archlint check` auto-detects GitHub Actions and emits `::error` annotations plus a one-line
fix hint; pass `--format text` to force plain output, or `--format github` to force it on.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| `0`  | no boundary violations |
| `1`  | one or more imports cross a forbidden boundary |
| `2`  | usage / config error |

## Scope & limits (honest list)

- **Go, TypeScript/JavaScript, and Python.** Go imports are read with the standard library's
  `go/parser` (exact). TS/JS and Python imports are extracted with regex scanners covering the
  standard forms (`import … from`, `export … from`, `require()`, dynamic `import()`; Python
  `import a.b` and `from … import`, including relative imports) — these are *not* full
  parsers, so an import-like string inside a comment or string literal can be a false positive.
- **Import boundaries, for now.** It governs the dependency graph between layers. Detecting
  a synchronous call where an async one was required, or a direct DB query across a domain
  boundary, comes from correlating runtime traces — a later phase.
- **Dependency-free** (Go stdlib only). Rules come from `architecture.json` and/or
  ` ```archlint ` blocks in your ADRs (see *Executable ADRs*); both are parsed with the
  standard library. JSON today; YAML support is a follow-up (it adds one dependency).
- **Deterministic by design.** The point is a guardrail you can gate CI on: same diff, same
  verdict, no model in the loop.

## License

MIT
