package domain

// Order is a domain entity. The domain layer is the core of the app and depends on nothing
// else inside it — a constraint declared in docs/adr/0001-domain-is-infra-free.md, not in
// architecture.json.
type Order struct {
	ID     int
	Amount float64
}
