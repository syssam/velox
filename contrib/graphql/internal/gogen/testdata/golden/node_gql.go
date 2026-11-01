package gen

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion6

// Node returns the node with the global ID id, or nil when no row has its
// key. It reads the ID's type, decodes the key as that type's, and looks
// the row up with one query on that entity, through the DB veloxgql.DBFromContext
// reads from ctx, reading only the columns the field's selection needs; its
// edges batch (veloxgql.Batch). A column the selection does not need is
// zero in the node, except the key, the foreign keys and the tenant: an
// application's resolver must not read one it did not select. An ID of no
// Node type fails with veloxgql.ErrWrongIDType and a malformed one with
// veloxgql.ErrBadID; neither runs a query.
func Node(ctx context.Context, id string) (veloxgql.Noder, error) {
	typ, err := veloxgql.IDType(id)
	if err != nil {
		return nil, err
	}
	switch typ {
	case "Category":
		k, err := veloxgql.DecodeID[int64](id, "Category")
		if err != nil {
			return nil, err
		}
		db, err := veloxgql.DBFromContext(ctx)
		if err != nil {
			return nil, err
		}
		q := db.Query[Category]()
		if fields, ok := gqlimpl.Selected(ctx, "Category"); ok {
			q = gqlimpl.Narrow(q, categoryColumnsGQL(fields))
		}
		x, err := q.Where(Categories.ID.EQ(k)).One(ctx)
		switch {
		case velox.IsNotFound(err):
			return nil, nil
		case err != nil:
			return nil, err
		}
		veloxgql.Batch(ctx, []*Category{x})
		return x, nil
	case "Group":
		k, err := veloxgql.DecodeID[int64](id, "Group")
		if err != nil {
			return nil, err
		}
		db, err := veloxgql.DBFromContext(ctx)
		if err != nil {
			return nil, err
		}
		q := db.Query[Group]()
		if fields, ok := gqlimpl.Selected(ctx, "Group"); ok {
			q = gqlimpl.Narrow(q, groupColumnsGQL(fields))
		}
		x, err := q.Where(Groups.ID.EQ(k)).One(ctx)
		switch {
		case velox.IsNotFound(err):
			return nil, nil
		case err != nil:
			return nil, err
		}
		veloxgql.Batch(ctx, []*Group{x})
		return x, nil
	case "Post":
		k, err := veloxgql.DecodeID[int64](id, "Post")
		if err != nil {
			return nil, err
		}
		db, err := veloxgql.DBFromContext(ctx)
		if err != nil {
			return nil, err
		}
		q := db.Query[Post]()
		if fields, ok := gqlimpl.Selected(ctx, "Post"); ok {
			q = gqlimpl.Narrow(q, postColumnsGQL(fields))
		}
		x, err := q.Where(Posts.ID.EQ(k)).One(ctx)
		switch {
		case velox.IsNotFound(err):
			return nil, nil
		case err != nil:
			return nil, err
		}
		veloxgql.Batch(ctx, []*Post{x})
		return x, nil
	case "Account":
		k, err := veloxgql.DecodeID[int64](id, "Account")
		if err != nil {
			return nil, err
		}
		db, err := veloxgql.DBFromContext(ctx)
		if err != nil {
			return nil, err
		}
		q := db.Query[User]()
		if fields, ok := gqlimpl.Selected(ctx, "Account"); ok {
			q = gqlimpl.Narrow(q, userColumnsGQL(fields))
		}
		x, err := q.Where(Users.ID.EQ(k)).One(ctx)
		switch {
		case velox.IsNotFound(err):
			return nil, nil
		case err != nil:
			return nil, err
		}
		veloxgql.Batch(ctx, []*User{x})
		return x, nil
	}
	return nil, gqlimpl.InputErrorf("Node: %w: the ID is of no Node type", veloxgql.ErrWrongIDType)
}

// Nodes returns the nodes with the global IDs ids, in their order, with nil
// for an ID no row has. It decodes every ID first, and then looks up each
// type's nodes with one query, through the DB veloxgql.DBFromContext reads from
// ctx; the query reads at most one row for each of its IDs, whatever the
// DB's WithMaxRows, and only the columns the field's selection needs. The
// nodes of a type are one batch for their edges (veloxgql.Batch). A column
// the selection does not need is zero in a node, except the key, the foreign
// keys and the tenant: an application's resolver must not read one it did
// not select.
// It fails as Node does on an ID, before any query runs,
// and with veloxgql.ErrPageTooLarge on more IDs than the MaxPage of
// veloxgql.LimitsFromContext(ctx).
func Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error) {
	if most := veloxgql.LimitsFromContext(ctx).MaxPage; len(ids) > most {
		return nil, gqlimpl.InputErrorf("Nodes: %w: %d IDs, and the most is %d", veloxgql.ErrPageTooLarge, len(ids), most)
	}
	// The keys of each type, and the indexes in ids of their IDs.
	var (
		keysCategory []int64
		atCategory   []int
		keysGroup    []int64
		atGroup      []int
		keysPost     []int64
		atPost       []int
		keysAccount  []int64
		atAccount    []int
	)
	for i, id := range ids {
		typ, err := veloxgql.IDType(id)
		if err != nil {
			return nil, err
		}
		switch typ {
		case "Category":
			k, err := veloxgql.DecodeID[int64](id, "Category")
			if err != nil {
				return nil, err
			}
			keysCategory = append(keysCategory, k)
			atCategory = append(atCategory, i)
		case "Group":
			k, err := veloxgql.DecodeID[int64](id, "Group")
			if err != nil {
				return nil, err
			}
			keysGroup = append(keysGroup, k)
			atGroup = append(atGroup, i)
		case "Post":
			k, err := veloxgql.DecodeID[int64](id, "Post")
			if err != nil {
				return nil, err
			}
			keysPost = append(keysPost, k)
			atPost = append(atPost, i)
		case "Account":
			k, err := veloxgql.DecodeID[int64](id, "Account")
			if err != nil {
				return nil, err
			}
			keysAccount = append(keysAccount, k)
			atAccount = append(atAccount, i)
		default:
			return nil, gqlimpl.InputErrorf("Nodes: %w: the ID is of no Node type", veloxgql.ErrWrongIDType)
		}
	}
	out := make([]veloxgql.Noder, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if len(keysCategory) > 0 {
		q := db.Query[Category]()
		if fields, ok := gqlimpl.Selected(ctx, "Category"); ok {
			q = gqlimpl.Narrow(q, categoryColumnsGQL(fields))
		}
		rows, err := q.Where(Categories.ID.In(keysCategory...)).Unbounded().MaxRows(len(keysCategory)).All(ctx)
		if err != nil {
			return nil, err
		}
		by := make(map[int64]*Category, len(rows))
		batch := make([]*Category, len(rows))
		for i := range rows {
			by[rows[i].ID] = &rows[i]
			batch[i] = &rows[i]
		}
		veloxgql.Batch(ctx, batch)
		for j, k := range keysCategory {
			if x := by[k]; x != nil {
				out[atCategory[j]] = x
			}
		}
	}
	if len(keysGroup) > 0 {
		q := db.Query[Group]()
		if fields, ok := gqlimpl.Selected(ctx, "Group"); ok {
			q = gqlimpl.Narrow(q, groupColumnsGQL(fields))
		}
		rows, err := q.Where(Groups.ID.In(keysGroup...)).Unbounded().MaxRows(len(keysGroup)).All(ctx)
		if err != nil {
			return nil, err
		}
		by := make(map[int64]*Group, len(rows))
		batch := make([]*Group, len(rows))
		for i := range rows {
			by[rows[i].ID] = &rows[i]
			batch[i] = &rows[i]
		}
		veloxgql.Batch(ctx, batch)
		for j, k := range keysGroup {
			if x := by[k]; x != nil {
				out[atGroup[j]] = x
			}
		}
	}
	if len(keysPost) > 0 {
		q := db.Query[Post]()
		if fields, ok := gqlimpl.Selected(ctx, "Post"); ok {
			q = gqlimpl.Narrow(q, postColumnsGQL(fields))
		}
		rows, err := q.Where(Posts.ID.In(keysPost...)).Unbounded().MaxRows(len(keysPost)).All(ctx)
		if err != nil {
			return nil, err
		}
		by := make(map[int64]*Post, len(rows))
		batch := make([]*Post, len(rows))
		for i := range rows {
			by[rows[i].ID] = &rows[i]
			batch[i] = &rows[i]
		}
		veloxgql.Batch(ctx, batch)
		for j, k := range keysPost {
			if x := by[k]; x != nil {
				out[atPost[j]] = x
			}
		}
	}
	if len(keysAccount) > 0 {
		q := db.Query[User]()
		if fields, ok := gqlimpl.Selected(ctx, "Account"); ok {
			q = gqlimpl.Narrow(q, userColumnsGQL(fields))
		}
		rows, err := q.Where(Users.ID.In(keysAccount...)).Unbounded().MaxRows(len(keysAccount)).All(ctx)
		if err != nil {
			return nil, err
		}
		by := make(map[int64]*User, len(rows))
		batch := make([]*User, len(rows))
		for i := range rows {
			by[rows[i].ID] = &rows[i]
			batch[i] = &rows[i]
		}
		veloxgql.Batch(ctx, batch)
		for j, k := range keysAccount {
			if x := by[k]; x != nil {
				out[atAccount[j]] = x
			}
		}
	}
	return out, nil
}
