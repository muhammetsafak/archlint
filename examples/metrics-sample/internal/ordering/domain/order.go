package domain

// The ordering domain. It reaches straight into billing's *internal* ledger instead of going
// through billing's published gateway port — the architecture.json rules permit the edge, but
// `archlint metrics --contexts contexts.json` flags it as a bounded-context breach (and, since
// the two layers are owned by different teams, it counts toward the Conway Mismatch Index).
import "github.com/acme/shop/internal/billing/ledger"

// Order is a placeholder business type.
type Order struct{ ID int }

// Total bypasses billing's public API by calling into the ledger internals directly.
func Total(o Order) int { return ledger.Balance(o.ID) }
