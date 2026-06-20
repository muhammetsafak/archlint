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

## Coupling metrics & Conway/DDD analysis (`archlint metrics`)

`archlint check` answers a yes/no question: did any import cross a forbidden boundary? Some
architectural risk is more graduated — a layer everything depends on is *expensive* to change
even when no rule forbids it; a context reached into through the back door is fragile even when
the layer rule "allows" the edge; a dependency that crosses three team boundaries will be slow
to ship even when it compiles. `archlint metrics` computes these signals **on the same import
graph** `check` already builds, prints them, and can gate CI on a breach — without ever changing
a lint verdict. It is **opt-in and backward-compatible**: a default `check` run is unchanged, and
`metrics` with no extra config is a read-only report.

```sh
archlint metrics                                   # coupling table for .
archlint metrics --contexts contexts.json ./svc    # + bounded-context boundary checks
archlint metrics --teams teams.json --max-instability 0.8
```

### 1. Coupling metrics (Robert C. Martin)

For every declared layer, on the cross-layer dependency graph:

| Metric | Formula | Meaning |
| ------ | ------- | ------- |
| **Ca** (afferent) | count of distinct layers that import this layer | who depends on me (incoming) |
| **Ce** (efferent) | count of distinct layers this layer imports | who I depend on (outgoing) |
| **I** (instability) | `I = Ce / (Ca + Ce)` | 0 = maximally stable (only depended upon), 1 = maximally unstable. A layer with no edges is `I = 0` by definition. |
| **A** (abstractness) | declared (see below) | fraction of the layer that is abstract: `1.0` or `0.0` |
| **D** (distance) | `D = \|A + I − 1\|` | distance from the "main sequence" (`A + I = 1`); `0` is ideal, near `1` is the zone of pain (stable + concrete) or uselessness (unstable + abstract) |

archlint has no per-symbol abstract/concrete data, so **A is a coarse, explicit proxy**: a layer
listed in a context's `"abstract"` array (see below) gets `A = 1.0`; every other layer is
`A = 0.0`. D is reported so you can spot a heavily-depended-upon layer (`I → 0`) that is also
concrete (`A = 0`, so `D → 1`) — exactly the layer a refactor will hurt.

**Stable-Dependencies Principle (SDP).** An edge `L → M` is flagged when `I(L) < I(M)`: a
*more-stable* layer depending on a *less-stable* one. Stable code should not hang off volatile
code, because the volatile side drags churn into the stable one. SDP breaches gate CI under
`--fail-on-violation` (on by default).

The **instability gate** is separate and explicit: `--max-instability F` (in `0..1`) fails the
run when any layer's `I` exceeds `F`. It is **off unless you pass it** (the default is `-1`).

### 2. Bounded contexts (DDD) — `contexts.json`

A bounded context groups layers and publishes a narrow **public API/port**: the only layers
another context is allowed to import. An import that lands on a *non-public* (internal) layer of
another context bypasses the port — a boundary breach, even when the layer rule permits the edge.

```json
{
  "contexts": [
    { "name": "ordering", "layers": ["domain", "app"], "public": ["app"], "abstract": ["domain"] },
    { "name": "billing",  "layers": ["ledger", "gateway"], "public": ["gateway"] }
  ]
}
```

- **`layers`** — the layer names (same names as `architecture.json`) that belong to the context.
  Contexts must not overlap (a layer has at most one context); layers in no context are ignored.
- **`public`** — the subset of `layers` other contexts may import. A layer in the context but
  absent from `public` is **internal**. An **empty `public` closes the context**: every inbound
  cross-context import is a breach (declare a port to open it).
- **`abstract`** — optional; marks layers abstract for the `A` metric (so `contexts.json` doubles
  as the abstractness declaration).

A cross-context import to a public layer is fine; to an internal layer it is reported (and gates
CI under `--fail-on-violation`):

```
ordering/domain → billing/ledger  is not a published port  at internal/ordering/domain/order.go:7
```

### 3. Conway Mismatch Index — `teams.json`

Conway's law says a system's structure mirrors the org that built it. The **Conway Mismatch
Index (CMI)** quantifies how much the *code's* dependencies cut across the *org's* team
boundaries. Declare which team owns each layer:

```json
{ "teams": { "commerce": ["domain", "app"], "platform": ["ledger", "gateway"] } }
```

Let **E** be the set of distinct cross-layer import edges whose **both** endpoints' layers are
owned by some team, and **X ⊆ E** the subset whose two endpoints belong to **different** teams.
Then:

```
CMI = |X| / |E|                  (0 when |E| = 0)
```

`CMI = 0` means every code dependency stays inside one team; `CMI = 1` means every dependency
crosses a team boundary. A per-layer mismatch is reported too: for each owned layer, the fraction
of its owned incident edges (in or out) that cross a team boundary — surfacing the modules whose
dependencies fight the org chart.

A layer owned by no team is **excluded** from the index (CMI is only defined over owned edges).
When **no ownership mapping is present** (`teams.json` absent and no `--teams`), Conway is simply
**skipped** — never an error. `CODEOWNERS` ingestion is a planned follow-up; `teams.json` is the
default today.

### Flags, defaults, and exit code

| Flag | Default | Effect |
| ---- | ------- | ------ |
| `--config` | `architecture.json` | the layer/rule config (resolved like `check`'s) |
| `--adr-dir` | `docs/adr` | executable-ADR rules merge first, exactly as in `check` |
| `--contexts` | `contexts.json` | bounded-context map; **absent ⇒ no context analysis** |
| `--teams` | `teams.json` | team-ownership map; **absent ⇒ Conway skipped** |
| `--max-instability` | `-1` (off) | fail when any layer's `I` exceeds this `0..1` ceiling |
| `--fail-on-violation` | `true` | exit `1` on an SDP or bounded-context breach |
| `--format` | auto | `text`, or `github` (inline `::error`/`::notice`) — auto-`github` under Actions |

Exit codes match `check`'s convention: **`2`** on a usage/config error, **`1`** when a *gated*
breach is found (an over-instability layer, or — under `--fail-on-violation` — an SDP or context
violation), **`0`** otherwise. The default run with no contexts, no teams, and no gate is purely
observational and always exits `0`.

```sh
archlint metrics examples/metrics-sample
# Coupling table (Ca/Ce/I/A/D per layer), a cross-context bypass
# (ordering/domain → billing/ledger), and a Conway Mismatch Index of 0.50.
# `check` on the same tree is clean — the layer rules permit the edge; metrics is what catches it.
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

archlint metrics examples/metrics-sample  # coupling + DDD + Conway (check on this tree is clean)
# ordering/domain → billing/ledger  is not a published port  ...  /  Conway Mismatch Index: 0.50
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

Both `check` and `metrics` share one convention:

| Code | `check` | `metrics` |
| ---- | ------- | --------- |
| `0`  | no boundary violations | no gated breach |
| `1`  | one or more imports cross a forbidden boundary | a gated breach (over-instability layer, or — under `--fail-on-violation` — an SDP or bounded-context violation) |
| `2`  | usage / config error | usage / config error |

## Scope & limits (honest list)

- **Go, TypeScript/JavaScript, and Python.** Go imports are read with the standard library's
  `go/parser` (exact). TS/JS and Python imports are extracted with regex scanners covering the
  standard forms (`import … from`, `export … from`, `require()`, dynamic `import()`; Python
  `import a.b` and `from … import`, including relative imports) — these are *not* full
  parsers, so an import-like string inside a comment or string literal can be a false positive.
- **Import boundaries, plus structural metrics.** `check` governs the dependency graph between
  layers; `archlint metrics` adds coupling (Ca/Ce/I/A/D), bounded-context, and Conway signals on
  that same graph (see *Coupling metrics & Conway/DDD analysis*). Both work on the static import
  graph: detecting a synchronous call where an async one was required, or a direct DB query
  across a domain boundary, comes from correlating runtime traces — a later phase. Abstractness
  (`A`) is a declared proxy, not symbol-level analysis; team ownership is a `teams.json`
  (`CODEOWNERS` ingestion is a follow-up).
- **Dependency-free** (Go stdlib only). Rules come from `architecture.json` and/or
  ` ```archlint ` blocks in your ADRs (see *Executable ADRs*); both are parsed with the
  standard library. JSON today; YAML support is a follow-up (it adds one dependency).
- **Deterministic by design.** The point is a guardrail you can gate CI on: same diff, same
  verdict, no model in the loop.

## License

MIT
