package domain

// bad.go is the planted violation: the domain layer reaches into the db (infrastructure)
// layer, which architecture.json forbids ("domain": []). `archlint check` flags this; a
// plain compiler would not.
import "github.com/acme/app/internal/db"

var _ = db.Connect
