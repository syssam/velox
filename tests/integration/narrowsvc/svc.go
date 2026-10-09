// Package narrowsvc is an application-style service that uses the generated
// code without naming the root Client or Tx: it takes a runtime.TxRunner and
// builds the entity clients it needs with From. The integration tests drive
// it with a real client, and TestNarrowService_DoesNotImportRoot pins that it
// stays independent of the root package — which is the whole point: a package
// that names the root Client makes the compiler load every entity's client.
package narrowsvc

import (
	"context"
	"errors"

	"github.com/syssam/velox/runtime"
	postclient "github.com/syssam/velox/tests/integration/client/post"
	userclient "github.com/syssam/velox/tests/integration/client/user"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"
)

// Service reads and writes users and posts.
type Service struct {
	db    runtime.TxRunner
	users *userclient.UserClient
}

// New returns a Service over db, the generated root client.
func New(db runtime.TxRunner) *Service {
	return &Service{db: db, users: userclient.From(db)}
}

// UserByEmail returns the user with the given email.
func (s *Service) UserByEmail(ctx context.Context, email string) (*entity.User, error) {
	return s.users.Query().Where(user.EmailField.EQ(email)).Only(ctx)
}

// ErrRejected makes RenameWithPost roll back.
var ErrRejected = errors.New("narrowsvc: rejected")

// RenameWithPost renames the user and adds a post in one transaction; when
// reject is set it returns ErrRejected after both writes, so neither is kept.
func (s *Service) RenameWithPost(ctx context.Context, id int, name, title string, reject bool) error {
	return s.db.RunTx(ctx, func(tx runtime.ConfigProvider) error {
		if err := userclient.From(tx).UpdateOneID(id).SetName(name).Exec(ctx); err != nil {
			return err
		}
		err := postclient.From(tx).Create().
			SetTitle(title).
			SetContent("body").
			SetStatus(post.StatusPublished).
			SetViewCount(0).
			SetAuthorID(id).
			Exec(ctx)
		if err != nil {
			return err
		}
		if reject {
			return ErrRejected
		}
		return nil
	})
}

// Rename renames the user in a transaction the method ends itself, the
// StartTx counterpart of RunTx. With commit unset it rolls back.
func (s *Service) Rename(ctx context.Context, id int, name string, commit bool) error {
	tx, err := s.db.StartTx(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := userclient.From(tx).UpdateOneID(id).SetName(name).Exec(ctx); err != nil {
		return err
	}
	if !commit {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
