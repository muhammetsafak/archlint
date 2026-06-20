package gateway

// The billing gateway is billing's public port: the one layer other contexts may import.
import "github.com/acme/shop/internal/billing/ledger"

// Charge is the published entry point; it posts to the internal ledger.
func Charge(amount int) error {
	ledger.Post(0, amount)
	return nil
}
