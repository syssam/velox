package migrate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/syssam/velox2"
)

func TestSnapshotRoundTrip(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		s := full()
		s.Dialect = d
		b1, err := s.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var got Schema
		if err := got.UnmarshalJSON(b1); err != nil {
			t.Fatalf("%v: %v\n%s", d, err, b1)
		}
		if !got.Equal(s) {
			t.Errorf("%v: the round trip changed the schema:\n%s", d, b1)
		}
		b2, _ := got.MarshalJSON()
		if !bytes.Equal(b1, b2) {
			t.Errorf("%v: the bytes differ:\n%s\n%s", d, b1, b2)
		}
		b3, _ := s.MarshalJSON()
		if !bytes.Equal(b1, b3) {
			t.Errorf("%v: two marshals differ", d)
		}
	}
}

func TestSnapshotShape(t *testing.T) {
	s := &Schema{
		Dialect: velox.Postgres,
		Tables: []Table{{
			Name:       "users",
			Columns:    []Column{{Name: "id", Type: "bigint", Identity: true, Sequence: "users_id_seq"}},
			PrimaryKey: []string{"id"}, PrimaryKeyName: "users_pkey",
			Indexes:     []Index{{Name: "users_email_unique", Columns: []string{"email"}, Unique: true}},
			ForeignKeys: []ForeignKey{{Name: "users_org_id_fkey", Columns: []string{"org_id"}, RefTable: TableRef{Name: "orgs"}, RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
			Checks:      []Check{{Name: "c", Expr: "x"}},
			Comment:     "k", Collation: "C",
		}},
		Enums: []Enum{{Name: "users_role", Values: []string{"viewer", "admin"}}},
	}
	want := `{"version":1,"dialect":"postgres","tables":[{"name":"users","columns":[{"name":"id","type":"bigint","identity":true,"sequence":"users_id_seq"}],"primary_key":["id"],"primary_key_name":"users_pkey","indexes":[{"name":"users_email_unique","columns":["email"],"unique":true}],"foreign_keys":[{"name":"users_org_id_fkey","columns":["org_id"],"ref_table":{"name":"orgs"},"ref_columns":["id"],"on_delete":"CASCADE"}],"checks":[{"name":"c","expr":"x"}],"comment":"k","collation":"C"}],"enums":[{"name":"users_role","values":["viewer","admin"]}]}`
	got, err := s.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// json.Marshal reaches the same method, and its output stays compact.
	viaStd, err := json.Marshal(s)
	if err != nil || string(viaStd) != want {
		t.Errorf("json.Marshal: %s, %v", viaStd, err)
	}
	empty, _ := (&Schema{Dialect: velox.SQLite}).MarshalJSON()
	if string(empty) != `{"version":1,"dialect":"sqlite"}` {
		t.Errorf("empty: %s", empty)
	}
}

func TestSnapshotSorted(t *testing.T) {
	s := &Schema{
		Dialect: velox.MySQL,
		Tables: []Table{
			{Name: "b", Indexes: []Index{{Name: "y"}, {Name: "x"}}},
			{Name: "a", Columns: []Column{{Name: "z", Type: "int"}, {Name: "a", Type: "int"}}},
		},
	}
	before := s.clone()
	b, err := s.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"name":"a","columns":[{"name":"z","type":"int"},{"name":"a","type":"int"}]`) ||
		strings.Index(string(b), `"name":"a"`) > strings.Index(string(b), `"name":"b"`) ||
		strings.Index(string(b), `"name":"x"`) > strings.Index(string(b), `"name":"y"`) {
		t.Errorf("not sorted, or columns reordered:\n%s", b)
	}
	if s.Tables[0].Name != "b" || s.Tables[0].Indexes[0].Name != "y" || !s.Equal(before) {
		t.Error("MarshalJSON changed the schema")
	}
}

func TestSnapshotOmitsPos(t *testing.T) {
	b, err := full().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"where":"org_id > 0"`) {
		t.Errorf("a predicate is escaped: %s", b)
	}
	for _, w := range []string{`"pos"`, `"Pos"`, "schema/invoice.go"} {
		if strings.Contains(string(b), w) {
			t.Errorf("%q in %s", w, b)
		}
	}
	var s Schema
	if err := s.UnmarshalJSON([]byte(`{"version":1,"dialect":"sqlite","tables":[{"name":"t","pos":"x:1","columns":[{"name":"c","type":"integer","Pos":"x:2"}]}]}`)); err != nil {
		t.Fatal(err)
	}
	if s.Tables[0].Pos != "" || s.Tables[0].Columns[0].Pos != "" {
		t.Errorf("Pos was read: %+v", s.Tables[0])
	}
}

func TestSnapshotSortsOnRead(t *testing.T) {
	var s Schema
	in := `{"version":1,"dialect":"postgres","tables":[{"name":"b"},{"name":"a"}],"enums":[{"name":"y","values":["1"]},{"name":"x","values":["2"]}]}`
	if err := s.UnmarshalJSON([]byte(in)); err != nil {
		t.Fatal(err)
	}
	if s.Tables[0].Name != "a" || s.Enums[0].Name != "x" {
		t.Errorf("%+v", s)
	}
}

func TestSnapshotRefusesVersion(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`{"version":2,"dialect":"postgres"}`, "migrate: schema.json is version 2; this velox reads version 1"},
		{`{"version":0,"dialect":"postgres"}`, "migrate: schema.json is version 0; this velox reads version 1"},
		{`{"dialect":"postgres"}`, "migrate: schema.json is version 0; this velox reads version 1"},
		{`{"version":1,"dialect":"oracle"}`, `migrate: schema.json has the unknown dialect "oracle"`},
		{`{"version":1}`, `migrate: schema.json has the unknown dialect ""`},
	} {
		var s Schema
		err := s.UnmarshalJSON([]byte(c.in))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v, want %q", c.in, err, c.want)
		}
		if _, ok := err.(*Error); !ok {
			t.Errorf("%s: %T, want *Error", c.in, err)
		}
	}
	var s Schema
	if err := s.UnmarshalJSON([]byte(`{`)); err == nil {
		t.Error("malformed JSON")
	}
}
