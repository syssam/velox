// Package narrow is the "narrow path" of shape 15: application-style code
// that never names the generated root package. It takes a runtime.TxRunner
// and builds the entity clients it needs with From. matrix_test.go drives it
// with a real client and pins, with go list -deps, that it stays independent
// of the root package.
package narrow

import (
	"context"
	"errors"

	"github.com/syssam/velox/runtime"

	"example.com/matrix/velox/author"
	articleclient "example.com/matrix/velox/client/article"
	authorclient "example.com/matrix/velox/client/author"
	"example.com/matrix/velox/entity"
)

// ErrRejected makes RenameWithArticle roll back.
var ErrRejected = errors.New("narrow: rejected")

// AuthorByName reads through a client built with From.
func AuthorByName(ctx context.Context, db runtime.TxRunner, name string) (*entity.Author, error) {
	return authorclient.From(db).Query().Where(author.NameField.EQ(name)).Only(ctx)
}

// RenameWithArticle renames the author and adds an article in one RunTx
// transaction; with reject set it returns ErrRejected after both writes.
func RenameWithArticle(ctx context.Context, db runtime.TxRunner, id int, name, title string, reject bool) error {
	return db.RunTx(ctx, func(tx runtime.ConfigProvider) error {
		if err := authorclient.From(tx).UpdateOneID(id).SetName(name).Exec(ctx); err != nil {
			return err
		}
		if err := articleclient.From(tx).Create().SetTitle(title).SetAuthorID(id).Exec(ctx); err != nil {
			return err
		}
		if reject {
			return ErrRejected
		}
		return nil
	})
}

// Rename renames the author in a StartTx transaction it ends itself; with
// commit unset it rolls back.
func Rename(ctx context.Context, db runtime.TxRunner, id int, name string, commit bool) error {
	tx, err := db.StartTx(ctx)
	if err != nil {
		return err
	}
	if err := authorclient.From(tx).UpdateOneID(id).SetName(name).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if !commit {
		return tx.Rollback()
	}
	return tx.Commit()
}
