//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"

	"example.com/tmp/mixins/softdel"
)

// Project lists every kind of mixin, and declares a Policy of its own (the
// golden Config binds it): its sources are the mixins', in Mixin order,
// then its own.
type Project struct{ schema.Schema }

func (Project) Mixin() []schema.Mixin {
	return []schema.Mixin{mixin.Time{}, InOrg{}, &Audited{By: "ops"}, softdel.Mixin{}}
}

func (Project) Fields() []schema.Field { return []schema.Field{field.String("name")} }

// Label is in an organization, and declares nothing itself.
type Label struct{ schema.Schema }

func (Label) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

func (Label) Fields() []schema.Field { return []schema.Field{field.String("name")} }

// Owner has no behaviour.
type Owner struct{ schema.Schema }

func (Owner) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

func (Owner) Fields() []schema.Field { return []schema.Field{field.String("name")} }
