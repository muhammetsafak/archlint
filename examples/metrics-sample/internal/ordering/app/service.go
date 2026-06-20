package app

// The ordering application layer wires the domain to billing — and it goes through billing's
// published gateway port, the correct way to cross a context boundary.
import (
	"github.com/acme/shop/internal/billing/gateway"
	"github.com/acme/shop/internal/ordering/domain"
)

// Checkout charges an order via billing's public gateway.
func Checkout(o domain.Order) error { return gateway.Charge(domain.Total(o)) }
