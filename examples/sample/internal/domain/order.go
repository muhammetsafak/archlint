package domain

// Order is a domain entity. The domain layer is the core of the app and depends on
// nothing else inside it (see architecture.json: "domain": []).
type Order struct {
	ID     int
	Amount float64
}
