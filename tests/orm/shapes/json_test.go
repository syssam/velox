package shapes

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	client "github.com/syssam/velox/tests/orm/shapes/velox"
	"github.com/syssam/velox/tests/orm/shapes/velox/entity"
	"github.com/syssam/velox/tests/orm/shapes/velox/renamed"
)

// The JSON of an entity is keyed by the schema's field names, whatever the
// columns are called; a tag declared in the schema is used as written; a
// sensitive field reaches neither JSON nor a log line.
func TestJSON_KeysTagsAndSensitive(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	r := must(client.Renamed.Create(db).Set(
		renamed.Title.Set("t"), renamed.Count.Set(3), renamed.Secret.Set("hunter2"), renamed.Alias.Set("nick"),
	).Save(ctx))

	b, err := json.Marshal(r)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, "t", got["title"], "the key is the field's name, not its column ttl")
	assert.EqualValues(t, 3, got["count"])
	assert.Equal(t, "a", got["kind"])
	assert.Equal(t, "nick", got["aka"], "the json key of the schema's own tag")
	assert.Contains(t, got, "id")
	for _, absent := range []string{"ttl", "cnt", "knd", "seen", "secret", "alias"} {
		assert.NotContains(t, got, absent)
	}
	assert.NotContains(t, string(b), "hunter2")

	// The tag of the schema, whole.
	f, _ := reflect.TypeFor[entity.Renamed]().FieldByName("Alias")
	assert.Equal(t, "aka,attr", f.Tag.Get("xml"))
	assert.Equal(t, "aka,omitempty", f.Tag.Get("json"))

	// Printed, a sensitive field is named and not shown; the others are.
	for _, s := range []string{fmt.Sprint(r), fmt.Sprintf("%v", r), fmt.Sprintf("%+v", r), r.String()} {
		assert.NotContains(t, s, "hunter2")
		assert.Contains(t, s, "secret=<sensitive>")
		assert.Contains(t, s, "title=t")
	}

	// The column names are what the database sees.
	assert.Equal(t, "ttl", renamed.ColumnTitle)
	assert.Equal(t, "t", rawValue[string](t, db, "SELECT ttl FROM "+renamed.Table))
}
