package mixin_test

import (
	"testing"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/mixin"
)

var _ schema.Mixin = mixin.Time{}

func TestTime(t *testing.T) {
	fs := mixin.Time{}.Fields()
	if len(fs) != 2 {
		t.Fatalf("%d fields", len(fs))
	}
	c, u := fs[0].Descriptor(), fs[1].Descriptor()
	if c.Name != "created_at" || !c.Immutable || c.DefaultFunc == nil && c.Default == nil || c.Err != nil {
		t.Fatalf("created_at %+v", c)
	}
	if u.Name != "updated_at" || u.UpdateDefault == nil || u.Err != nil {
		t.Fatalf("updated_at %+v", u)
	}
	if len(mixin.CreateTime{}.Fields()) != 1 || len(mixin.UpdateTime{}.Fields()) != 1 {
		t.Fatal("CreateTime and UpdateTime add one field each")
	}
	if (mixin.Schema{}).Edges() != nil {
		t.Fatal("an empty mixin declares nothing")
	}
}
