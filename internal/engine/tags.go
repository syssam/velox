package engine

import (
	"context"
	"slices"
)

// Tag is a string a context carries to every statement run with it, for the
// hooks (Statement.Tags) and the SQL comment.
type Tag struct{ Key, Value string }

// tagsKey is the context key of the WithTags tags.
type tagsKey struct{}

// WithTags returns ctx with the tags kv (key, value, key, value, …) added: a
// key already on ctx keeps its place and takes the new value; a new key is
// appended. A last key with no value takes the value "", as WithTags runs on a
// request's path, where a mistake should cost a tag and not the request; no
// strings return ctx unchanged.
func WithTags(ctx context.Context, kv ...string) context.Context {
	if len(kv)%2 != 0 {
		kv = append(slices.Clip(kv), "")
	}
	if len(kv) == 0 {
		return ctx
	}
	// A new slice, never the parent's: contexts derived from the parent
	// keep the tags they had.
	old := TagsFrom(ctx)
	tags := make([]Tag, len(old), len(old)+len(kv)/2)
	copy(tags, old)
	for i := 0; i < len(kv); i += 2 {
		k, v := kv[i], kv[i+1]
		if j := slices.IndexFunc(tags, func(t Tag) bool { return t.Key == k }); j >= 0 {
			tags[j].Value = v
		} else {
			tags = append(tags, Tag{k, v})
		}
	}
	return context.WithValue(ctx, tagsKey{}, tags)
}

// TagsFrom returns the context's tags; nil when none. The slice is shared by
// every statement run with the context, and is never changed in place.
func TagsFrom(ctx context.Context) []Tag {
	t, _ := ctx.Value(tagsKey{}).([]Tag)
	return t
}
