package schema

import (
	"context"
	"errors"
	"strings"

	"github.com/syssam/velox"
)

// Hooks declares Task's hooks. The first listed is the outermost; CreatedBy's,
// a mixin's, runs before them. Before runs a function before the write, for
// the kinds of write it names.
func (Task) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(deriveSlug, velox.OpInsert),
		velox.Before(renamed, velox.OpUpdate),
		velox.Before(onlyDone, velox.OpDelete),
	}
}

// deriveSlug refuses a task with no title and derives slug from title, in each
// row the caller passed: Insert and InsertAll hand the hook all their rows.
func deriveSlug(_ context.Context, m *velox.Mutation) error {
	for r := range m.Rows() {
		title, _ := r.Value("title").(string)
		if title == "" {
			return errors.New("a task needs a title")
		}
		if err := r.Set("slug", slug(title)); err != nil {
			return err
		}
	}
	return nil
}

// renamed derives the new slug when an update sets the title.
func renamed(_ context.Context, m *velox.Mutation) error {
	v, ok := m.Value("title")
	if !ok {
		return nil // an update of other columns
	}
	title, _ := v.(string)
	if title == "" {
		return errors.New("a task needs a title")
	}
	return m.Set("slug", slug(title))
}

// onlyDone narrows a delete to the finished tasks, even one that deletes
// every row.
func onlyDone(_ context.Context, m *velox.Mutation) error {
	m.Where(velox.C("done").EQ(true))
	return nil
}

func slug(title string) string { return strings.ReplaceAll(strings.ToLower(title), " ", "-") }
