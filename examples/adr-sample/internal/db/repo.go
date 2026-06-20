package db

// Connect is the infrastructure seam the domain must not import (see the ADR).
func Connect() error { return nil }
