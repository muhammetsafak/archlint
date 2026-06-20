# ADR 0001: The domain layer is infrastructure-free

## Status

Accepted.

## Context

The domain holds the business model. If it depends on persistence, a database swap or a
schema change ripples into the core of the app — so we forbid the edge.

`architecture.json` here only knows the `domain` layer. The `db` layer and the boundary that
makes it a violation are declared *in this record*: the decision IS the enforced rule.

## Decision

```archlint
# declare the db layer (architecture.json doesn't know it)
layer db = internal/db

# the model must never reach into persistence
deny domain -> db
```

## Consequence

`archlint check examples/adr-sample` flags `internal/domain/bad.go`, attributing the
violation to this file — try editing the `deny` line and watch the verdict change.
