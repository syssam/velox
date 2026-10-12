package shapes

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	client "github.com/syssam/velox/tests/orm/shapes/velox"
	"github.com/syssam/velox/tests/orm/shapes/velox/article"
	"github.com/syssam/velox/tests/orm/shapes/velox/author"
	"github.com/syssam/velox/tests/orm/shapes/velox/entity"
	"github.com/syssam/velox/tests/orm/shapes/velox/topic"
)

// u.QueryPosts(ex) is the one way to query an edge from a row: a query of
// the target, on whatever it is given, with the target's own conditions on
// top.
func TestEntityQueries(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	ann := must(client.Author.Create(db).Set(author.Name.Set("ann"), author.Age.Set(40)).Save(ctx))
	go1 := must(client.Topic.Create(db).Set(topic.Name.Set("go")).Save(ctx))
	sql := must(client.Topic.Create(db).Set(topic.Name.Set("sql")).Save(ctx))
	a1 := must(client.Article.Create(db).Set(article.Title.Set("a1"), article.Author.Set(ann.ID), article.Topics.Add(go1.ID, sql.ID)).Save(ctx))
	a2 := must(client.Article.Create(db).Set(article.Title.Set("a2"), article.Author.Set(ann.ID)).Save(ctx))

	// To one, to many, many to many and its inverse.
	assert.Equal(t, ann.ID, must(a1.QueryAuthor(db).Only(ctx)).ID)
	assert.Equal(t, []int{a1.ID, a2.ID}, must(ann.QueryArticles(db).OrderBy(article.ID.Asc()).IDs(ctx)))
	assert.Equal(t, []string{"go", "sql"}, topicNames(must(a1.QueryTopics(db).OrderBy(topic.Name.Asc()).All(ctx))))
	assert.Equal(t, 1, must(go1.QueryArticles(db).Where(article.Title.EQ("a1")).Count(ctx)))

	// The entity holds no connection: the method runs on what it is given.
	rolledBack := db.RunTx(ctx, func(tx *velox.Tx) error {
		must(client.Article.Create(tx).Set(article.Title.Set("a3"), article.Author.Set(ann.ID)).Save(ctx))
		assert.Equal(t, 3, must(ann.QueryArticles(tx).Count(ctx)))
		return assert.AnError
	})
	require.ErrorIs(t, rolledBack, assert.AnError)
	assert.Equal(t, 2, must(ann.QueryArticles(db).Count(ctx)))
	_, err := ann.QueryArticles(nil).All(ctx)
	require.ErrorIs(t, err, velox.ErrNoExecutor)
	// The row it starts from needs only its key.
	assert.Equal(t, 2, must((&entity.Author{ID: ann.ID}).QueryArticles(db).Count(ctx)))
}
