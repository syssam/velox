package veloxotel

import "github.com/syssam/velox"

// AroundOf returns the statement hook alone, for tests of it without the
// rest of Options.
func AroundOf(cfg Config) velox.Around { return newAdapter(cfg).around }

// TxAroundOf returns the transaction hook alone.
func TxAroundOf(cfg Config) velox.TxAround { return newAdapter(cfg).txAround }
