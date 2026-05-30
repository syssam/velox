// Package op defines the typed operation model for the parity harness.
//
// A Program is an ordered list of operations. The index of a Create* op in the
// program is its "creation handle": later ops reference created entities by that
// handle (an int), never by a raw database id. All three executors (the
// reference model in package model, plus the velox and ent executors added in
// A3) interpret the same Program and key entities by creation handle, so the
// handle is the stable join key the comparator uses.
package op

import (
	"fmt"
	"reflect"
	"strings"
)

// Op is a single operation in a Program. The unexported isOp marker keeps the
// set of operations closed to this package.
type Op interface {
	isOp()
}

// Program is an ordered, replayable list of operations.
type Program []Op

// OrderTerm is one sort key in a pagination request. Field is a column name such
// as "view_count" or "created_at".
type OrderTerm struct {
	Field string
	Desc  bool
}

// --- Create ops. The op's program index is the created entity's handle. ---

// CreateAuthor creates an author entity.
type CreateAuthor struct {
	Name   string
	Age    int
	Role   string
	Bio    *string
	Labels []string
}

// CreatePost creates a post owned by the author at AuthorRef.
type CreatePost struct {
	Title     string
	Status    string
	ViewCount int
	Labels    []string
	AuthorRef int
}

// CreateComment creates a comment on the post at PostRef by the author at AuthorRef.
type CreateComment struct {
	Content   string
	Labels    []string
	PostRef   int
	AuthorRef int
}

// CreateTag creates a tag entity.
type CreateTag struct {
	Name string
}

// --- Edge / mutation ops. ---

// AddTagToPost attaches the tag at TagRef to the post at PostRef.
type AddTagToPost struct {
	PostRef int
	TagRef  int
}

// SetPostLabels replaces the JSON labels of the post at PostRef.
type SetPostLabels struct {
	PostRef int
	Labels  []string
}

// AppendPostLabels concatenates labels onto the post at PostRef.
type AppendPostLabels struct {
	PostRef int
	Labels  []string
}

// UpdatePostViewCount sets the view_count of the post at PostRef.
type UpdatePostViewCount struct {
	PostRef   int
	ViewCount int
}

// DeletePost deletes the post at PostRef.
type DeletePost struct {
	PostRef int
}

// --- Query / read ops. ---

// QueryPostsByStatus returns live posts matching Status in insertion order.
type QueryPostsByStatus struct {
	Status string
}

// CountPosts returns the live post count.
type CountPosts struct{}

// SumViewCount returns the sum of live posts' view_count.
type SumViewCount struct{}

// LoadAuthorPosts returns the live posts owned by the author at AuthorRef.
type LoadAuthorPosts struct {
	AuthorRef int
}

// PaginatePosts performs Relay cursor pagination over live posts. Cursors
// (AfterRef/BeforeRef) are creation handles, not opaque bytes.
type PaginatePosts struct {
	First     *int
	Last      *int
	AfterRef  *int
	BeforeRef *int
	OrderBy   []OrderTerm
}

func (CreateAuthor) isOp()        {}
func (CreatePost) isOp()          {}
func (CreateComment) isOp()       {}
func (CreateTag) isOp()           {}
func (AddTagToPost) isOp()        {}
func (SetPostLabels) isOp()       {}
func (AppendPostLabels) isOp()    {}
func (UpdatePostViewCount) isOp() {}
func (DeletePost) isOp()          {}
func (QueryPostsByStatus) isOp()  {}
func (CountPosts) isOp()          {}
func (SumViewCount) isOp()        {}
func (LoadAuthorPosts) isOp()     {}
func (PaginatePosts) isOp()       {}

// Format renders a Program as a replayable, human-readable listing: one line per
// op, "<index>: <TypeName>{<key:value ...>}". It reflects each op's fields,
// emitting scalars always (so a zero AuthorRef still appears) and skipping nil
// pointers and empty slices. Pointer values are dereferenced.
func Format(prog Program) string {
	var b strings.Builder
	for i, o := range prog {
		b.WriteString(fmt.Sprintf("%d: %s\n", i, formatOp(o)))
	}
	return b.String()
}

func formatOp(o Op) string {
	v := reflect.ValueOf(o)
	t := v.Type()
	var fields []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		s, ok := formatField(fv)
		if !ok {
			continue
		}
		fields = append(fields, f.Name+":"+s)
	}
	return fmt.Sprintf("%s{%s}", t.Name(), strings.Join(fields, " "))
}

// formatField renders one field value, reporting ok=false for fields that should
// be omitted (nil pointers, empty slices).
func formatField(fv reflect.Value) (string, bool) {
	switch fv.Kind() {
	case reflect.Pointer:
		if fv.IsNil() {
			return "", false
		}
		return fmt.Sprintf("%v", fv.Elem().Interface()), true
	case reflect.Slice:
		if fv.Len() == 0 {
			return "", false
		}
		return fmt.Sprintf("%v", fv.Interface()), true
	default:
		return fmt.Sprintf("%v", fv.Interface()), true
	}
}
