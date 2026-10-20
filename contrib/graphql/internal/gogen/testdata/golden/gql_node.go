package gen

import (
	"context"
	"errors"
	"fmt"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// Node returns the node with the global ID id, or nil when no row has its
// key. It reads the ID's type, decodes the key as that type's, and looks
// the row up with one query on that entity, through the DB veloxgql.DBFrom
// reads from ctx. An ID of no Node type fails with veloxgql.ErrWrongIDType
// and a malformed one with veloxgql.ErrBadID; neither runs a query.
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
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		x, err := db.Query[Category]().Where(Categories.ID.EQ(k)).One(ctx)
		switch {
		case errors.Is(err, velox.ErrNotFound):
			return nil, nil
		case err != nil:
			return nil, err
		}
		return x, nil
	case "Group":
		k, err := veloxgql.DecodeID[int64](id, "Group")
		if err != nil {
			return nil, err
		}
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		x, err := db.Query[Group]().Where(Groups.ID.EQ(k)).One(ctx)
		switch {
		case errors.Is(err, velox.ErrNotFound):
			return nil, nil
		case err != nil:
			return nil, err
		}
		return x, nil
	case "Post":
		k, err := veloxgql.DecodeID[int64](id, "Post")
		if err != nil {
			return nil, err
		}
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		x, err := db.Query[Post]().Where(Posts.ID.EQ(k)).One(ctx)
		switch {
		case errors.Is(err, velox.ErrNotFound):
			return nil, nil
		case err != nil:
			return nil, err
		}
		return x, nil
	case "Account":
		k, err := veloxgql.DecodeID[int64](id, "Account")
		if err != nil {
			return nil, err
		}
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		x, err := db.Query[User]().Where(Users.ID.EQ(k)).One(ctx)
		switch {
		case errors.Is(err, velox.ErrNotFound):
			return nil, nil
		case err != nil:
			return nil, err
		}
		return x, nil
	}
	return nil, fmt.Errorf("node: %w: the ID is of no Node type", veloxgql.ErrWrongIDType)
}

// Nodes returns the nodes with the global IDs ids, in their order, with nil
// for an ID no row has. It decodes every ID first, and then looks up each
// type's nodes with one query, through the DB veloxgql.DBFrom reads from
// ctx. It fails as Node does on an ID, before any query runs, and with
// veloxgql.ErrPageTooLarge on more than veloxgql.MaxPage IDs.
func Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error) {
	if len(ids) > veloxgql.MaxPage {
		return nil, fmt.Errorf("nodes: %w: %d IDs, and the most is %d", veloxgql.ErrPageTooLarge, len(ids), veloxgql.MaxPage)
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
			return nil, fmt.Errorf("node: %w: the ID is of no Node type", veloxgql.ErrWrongIDType)
		}
	}
	out := make([]veloxgql.Noder, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	if len(keysCategory) > 0 {
		rows, err := db.Query[Category]().Where(Categories.ID.In(keysCategory...)).All(ctx)
		if err != nil {
			return nil, err
		}
		by := make(map[int64]*Category, len(rows))
		for i := range rows {
			by[rows[i].ID] = &rows[i]
		}
		for j, k := range keysCategory {
			if x := by[k]; x != nil {
				out[atCategory[j]] = x
			}
		}
	}
	if len(keysGroup) > 0 {
		rows, err := db.Query[Group]().Where(Groups.ID.In(keysGroup...)).All(ctx)
		if err != nil {
			return nil, err
		}
		by := make(map[int64]*Group, len(rows))
		for i := range rows {
			by[rows[i].ID] = &rows[i]
		}
		for j, k := range keysGroup {
			if x := by[k]; x != nil {
				out[atGroup[j]] = x
			}
		}
	}
	if len(keysPost) > 0 {
		rows, err := db.Query[Post]().Where(Posts.ID.In(keysPost...)).All(ctx)
		if err != nil {
			return nil, err
		}
		by := make(map[int64]*Post, len(rows))
		for i := range rows {
			by[rows[i].ID] = &rows[i]
		}
		for j, k := range keysPost {
			if x := by[k]; x != nil {
				out[atPost[j]] = x
			}
		}
	}
	if len(keysAccount) > 0 {
		rows, err := db.Query[User]().Where(Users.ID.In(keysAccount...)).All(ctx)
		if err != nil {
			return nil, err
		}
		by := make(map[int64]*User, len(rows))
		for i := range rows {
			by[rows[i].ID] = &rows[i]
		}
		for j, k := range keysAccount {
			if x := by[k]; x != nil {
				out[atAccount[j]] = x
			}
		}
	}
	return out, nil
}
