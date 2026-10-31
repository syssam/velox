// Package testrepo is an application's repository layer, for the tests of
// velox.WithCallerSkip: it runs statements on behalf of its callers.
package testrepo

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// FirstInvoice reads one invoice.
func FirstInvoice(ctx context.Context, db *velox.DB) error {
	_, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx)
	return err
}
