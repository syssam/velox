package codegen

import (
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// The schemas of the doc comment tests.
type DcFlag struct{ schema.Schema }

func (DcFlag) Fields() []schema.Field {
	return []schema.Field{
		field.Bool("open").Default(true),
		field.Int64("price").Default(250),
		field.Int64("zero").Default(0),
		field.String("plan").Default("free"),
		field.String("empty").Default(""),
		field.Enum("tier").Values("basic", "pro").Default("basic"),
		field.Float("score").Nillable().Default(1.5),
		field.Time("at").Default(time.Now),
		field.Strings("tags").Default([]string{"new"}),
		field.String("note").Comment("The note on the flag.").Default("none"),
		field.String("plain"),
		field.String("token").Sensitive(),
	}
}

type DcPost struct{ schema.Schema }

func (DcPost) Fields() []schema.Field {
	return []schema.Field{
		field.String("title").Comment("Title is the post's headline."),
		field.String("body").Comment("The text of the post."),
		field.String("link").Comment("HTTP address of the post."),
		field.String("lead").Comment("The first lines.\nShown in lists."),
		field.Int64("author_id"),
	}
}

func (DcPost) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("author", DcUser.Type).Ref("posts").Unique().Required().Field("author_id"),
		edge.From("tags", DcTag.Type).Ref("posts"),
	}
}

type DcUser struct{ schema.Schema }

func (DcUser) Edges() []schema.Edge {
	return []schema.Edge{edge.To("posts", DcPost.Type), edge.To("notes", DcNote.Type)}
}

type DcTag struct{ schema.Schema }

func (DcTag) Edges() []schema.Edge { return []schema.Edge{edge.To("posts", DcPost.Type)} }

// DcNote's key of the edge notes is velox's own, and nullable.
type DcNote struct{ schema.Schema }

// rendered generates the schemas ss and returns the files by name.
func rendered(t *testing.T, ss ...schema.Interface) map[string]string {
	t.Helper()
	files, err := Generate(mustBuild(t, ss...), Config{})
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, f := range files {
		m[f.Name] = string(f.Data)
	}
	return m
}

// A field whose default replaces its zero value in an insert says so, in
// the struct and on its handle, with the setter that stores the zero; a
// default that is the zero value replaces nothing a value field holds. A
// Nillable field names SetNull, which stores NULL (ADR 0124).
func TestDefaultDoc(t *testing.T) {
	o := mustLower(t, Config{}, DcFlag{})
	want := map[string]string{
		"Open":  "An insert of the zero value stores the default, true; DcFlags.Open.Set(false) in the insert stores false.",
		"Price": "An insert of the zero value stores the default, 250; DcFlags.Price.Set(0) in the insert stores 0.",
		"Zero":  "",
		"Plan":  `An insert of the zero value stores the default, "free"; DcFlags.Plan.Set("") in the insert stores "".`,
		"Empty": "",
		"Tier":  "An insert of the zero value stores the default, DcFlagTierBasic.",
		"Score": "An insert of nil stores the default, 1.5; DcFlags.Score.SetNull() in the insert stores NULL.",
		"At":    "An insert of the zero value stores the value of its default function.",
		"Tags":  `An insert of the zero value stores the default, []string{"new"}.`,
		"Plain": "",
		"ID":    "",
	}
	for _, f := range o.entity("DcFlag").Fields {
		if w, ok := want[f.GoName]; ok && f.DefaultDoc != w {
			t.Errorf("%s: %q, want %q", f.GoName, f.DefaultDoc, w)
		}
	}
	src := rendered(t, DcFlag{})["dc_flag.go"]
	for _, w := range []string{
		"\t// An insert of the zero value stores the default, true; DcFlags.Open.Set(false) in the insert stores false.\n\tOpen bool",
		"\t// Open is the column open.\n\t// An insert of the zero value stores the default, true; DcFlags.Open.Set(false) in the insert stores false.\n\tOpen velox.Col",
		"\t// The note on the flag.\n\t// An insert of the zero value stores the default, \"none\"; DcFlags.Note.Set(\"\") in the insert stores \"\".\n\tNote ",
		"\t// Note is the column note: the note on the flag.\n\t// An insert",
		"\t// Plain is the column plain.\n\tPlain velox.StringCol",
	} {
		if !strings.Contains(src, w) {
			t.Errorf("dc_flag.go lacks %q", w)
		}
	}
}

// A handle's doc comment reuses the schema's comment on its field: as it
// is when it starts with the field's name, after the column's name
// otherwise, and on its own line when it has several. A relation's says
// what writes the edge.
func TestHandleDoc(t *testing.T) {
	src := rendered(t, DcPost{}, DcUser{}, DcTag{}, DcNote{})
	for file, ws := range map[string][]string{
		"dc_post.go": {
			"\t// Title is the post's headline.\n\tTitle velox.StringCol",
			"\t// Body is the column body: the text of the post.\n\tBody velox.StringCol",
			"\t// Link is the column link: HTTP address of the post.\n\tLink velox.StringCol",
			"\t// Lead is the column lead.\n\t//\n\t// The first lines.\n\t// Shown in lists.\n\tLead velox.StringCol",
			"\t// AuthorID is the column author_id.\n\tAuthorID velox.NumberCol",
			"// Author is the edge author, to DcUser: With loads it, Has and HasWith test it, and Set writes it.",
			"// Tags is the edge tags, to DcTag: With loads it, Has and HasWith test it, and Add, Remove and Clear write it.",
		},
		"dc_user.go": {
			"// Posts is the edge posts, to DcPost: With loads it, Has and HasWith test it, and DcPost.AuthorID, set on the DcPost, writes it.",
			"// Notes is the edge notes, to DcNote: With loads it, Has and HasWith test it, and Add, Remove and Clear write it.",
		},
	} {
		for _, w := range ws {
			if !strings.Contains(src[file], w) {
				t.Errorf("%s lacks %q", file, w)
			}
		}
	}
}

// String's doc comment names what it leaves out, and a Descriptor's what it
// holds.
func TestStringAndDescriptorDoc(t *testing.T) {
	src := rendered(t, DcFlag{}, DcPost{}, DcUser{}, DcTag{}, DcNote{})
	for file, ws := range map[string][]string{
		"dc_flag.go": {
			"// String returns the entity's fields except the sensitive one.\n",
			"// dcFlagDescriptor describes DcFlag to velox: its table, columns and checks.\n",
		},
		"dc_note.go": {
			"// String returns the entity's fields except the foreign key velox added.\n",
			"// dcNoteDescriptor describes DcNote to velox: its table and columns.\n",
		},
		"dc_post.go": {
			"// String returns the entity's fields.\n",
			"// dcPostDescriptor describes DcPost to velox: its table, columns and edges.\n",
		},
	} {
		for _, w := range ws {
			if !strings.Contains(src[file], w) {
				t.Errorf("%s lacks %q", file, w)
			}
		}
	}
	if got := andList([]string{"a", "b", "c"}); got != "a, b and c" {
		t.Errorf("andList: %q", got)
	}
}
