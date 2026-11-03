package schema

import (
	"context"
	"errors"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/hooks/auth"
	"github.com/syssam/velox/examples/hooks/gen/entity"
)

// Hooks declares Task's hooks, typed with the generated entity.Task. gen
// binds them, and velox calls Hooks once, at the first use of entity.Task. The first hook listed is the outermost. On limits a hook to
// the kinds of write it names; the others skip it.
func (Task) Hooks() []velox.Hook[entity.Task] {
	return []velox.Hook[entity.Task]{
		velox.On(auth.CreatedBy(func(t *entity.Task) *string { return &t.CreatedBy }), velox.OpInsert),
		velox.On(deriveSlug, velox.OpInsert),
		velox.On(renamed, velox.OpUpdate),
		velox.On(onlyDone, velox.OpDelete),
	}
}

// deriveSlug refuses a task with no title and derives Slug from Title, in each
// row the caller passed: Insert and InsertAll hand the hook all their rows.
func deriveSlug(next velox.Mutator[entity.Task]) velox.Mutator[entity.Task] {
	return velox.MutateFunc[entity.Task](func(ctx context.Context, m *velox.Mutation[entity.Task]) error {
		for _, x := range m.Rows() {
			if x.Title == "" {
				return errors.New("a task needs a title")
			}
			x.Slug = slug(x.Title)
		}
		return next.Mutate(ctx, m)
	})
}

// renamed sees the assignments of an Update or a Save through the Mutation:
// a handle's ValueIn reads one, typed, and Set adds or replaces one. It
// derives the new slug before the statement is sent, and when the statement
// succeeded it writes an Audit row. Under SQL(ctx) that write is refused
// with velox.ErrDryRun, so printing the statement records nothing; the hook
// needs no check for it.
func renamed(next velox.Mutator[entity.Task]) velox.Mutator[entity.Task] {
	return velox.MutateFunc[entity.Task](func(ctx context.Context, m *velox.Mutation[entity.Task]) error {
		title, ok := entity.Tasks.Title.ValueIn(m)
		if !ok {
			return next.Mutate(ctx, m) // an update of other columns
		}
		if title == "" {
			return errors.New("a task needs a title")
		}
		m.Set(entity.Tasks.Slug.Set(slug(title)))
		if err := next.Mutate(ctx, m); err != nil {
			return err
		}
		// m.DB() is the DB the write runs on: the transaction's when the
		// caller is in db.Tx, so the audit row commits or rolls back with the
		// update. Outside a transaction it is the database, and the two are
		// separate statements.
		audit := entity.Audit{Actor: auth.UserFromContext(ctx), Message: "renamed a task to " + title}
		return m.DB().Insert(&audit).Exec(ctx)
	})
}

// onlyDone narrows a delete to the finished tasks, even one that deletes
// every row.
func onlyDone(next velox.Mutator[entity.Task]) velox.Mutator[entity.Task] {
	return velox.MutateFunc[entity.Task](func(ctx context.Context, m *velox.Mutation[entity.Task]) error {
		m.Where(entity.Tasks.Done.EQ(true))
		return next.Mutate(ctx, m)
	})
}

func slug(title string) string { return strings.ReplaceAll(strings.ToLower(title), " ", "-") }
