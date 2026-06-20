package domain

// bad.go is the planted violation: the domain layer reaches into db (infrastructure). The
// forbidding rule lives in docs/adr/0001-domain-is-infra-free.md — an "executable ADR" — so
// `archlint check examples/adr-sample` flags this and names the ADR as the source.
import "github.com/acme/app/internal/db"

var _ = db.Connect
