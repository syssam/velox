package gen

import (
	"context"
	"errors"
	"strings"

	"github.com/syssam/velox2"
)

// Hooks declares Task's hooks. velox-gen binds it into Task's metadata, and
// velox calls it once, on a nil *Task. The first hook listed is the outermost.
// On limits a hook to the kinds of write it names; the others skip it.
func (*Task) Hooks() []velox.Hook[Task] {
	return []velox.Hook[Task]{
		velox.On(createdBy(func(t *Task) *string { return &t.CreatedBy }), velox.OpInsert),
		velox.On(deriveSlug, velox.OpInsert),
		velox.On(renamed, velox.OpUpdate),
		velox.On(onlyDone, velox.OpDelete),
	}
}

// deriveSlug refuses a task with no title and derives Slug from Title, in each
// row the caller passed: Insert and InsertAll hand the hook all their rows.
func deriveSlug(next velox.Mutator[Task]) velox.Mutator[Task] {
	return velox.MutateFunc[Task](func(ctx context.Context, m *velox.Mutation[Task]) error {
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
// Value reads one, Set adds or replaces one. It derives the new slug before
// the statement is sent, and when the statement succeeded it writes an Audit
// row.
func renamed(next velox.Mutator[Task]) velox.Mutator[Task] {
	return velox.MutateFunc[Task](func(ctx context.Context, m *velox.Mutation[Task]) error {
		v, ok := m.Value(Tasks.Title)
		if !ok {
			return next.Mutate(ctx, m) // an update of other columns
		}
		title, _ := v.(string)
		if title == "" {
			return errors.New("a task needs a title")
		}
		m.Set(Tasks.Slug.Set(slug(title)))
		if err := next.Mutate(ctx, m); err != nil {
			return err
		}
		// SQL(ctx) runs hooks too, without sending anything: a hook with an
		// effect outside the statement checks IsDryRun.
		if velox.IsDryRun(ctx) {
			return nil
		}
		// m.DB() is the DB the write runs on: the transaction's when the
		// caller is in db.Tx, so the audit row commits or rolls back with the
		// update. Outside a transaction it is the database, and the two are
		// separate statements.
		audit := Audit{Actor: userOf(ctx), Message: "renamed a task to " + title}
		return m.DB().Insert(&audit).Exec(ctx)
	})
}

// onlyDone narrows a delete to the finished tasks, even one that deletes
// every row.
func onlyDone(next velox.Mutator[Task]) velox.Mutator[Task] {
	return velox.MutateFunc[Task](func(ctx context.Context, m *velox.Mutation[Task]) error {
		m.Where(Tasks.Done.EQ(true))
		return next.Mutate(ctx, m)
	})
}

func slug(title string) string { return strings.ReplaceAll(strings.ToLower(title), " ", "-") }
