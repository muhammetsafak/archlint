package ledger

// The billing ledger: internal to the billing context (not part of its published port).
func Balance(account int) int { return account * 100 }

// Post records a charge against an account.
func Post(account, amount int) {}
