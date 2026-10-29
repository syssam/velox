package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The model is one schema of n entities E0..E<n-1> that the three ORMs'
// schema DSLs all express with the same builder calls. Entity i has the fields
// of fieldLines and the edges of edgesOf:
//   - a chain: E<i> has the one-to-many edge next to E<i+1>, whose inverse prev
//     is unique, required and bound to the field prev_id;
//   - a many-to-many pair in each group of ten: E<10g> has links to E<10g+5>,
//     whose inverse is linked.

// edgeSpec is one edge of an entity, in the DSL's terms.
type edgeSpec struct {
	Name     string
	Target   int  // the entity at the other end
	Inverse  bool // edge.From, with Ref; edge.To otherwise
	Ref      string
	Unique   bool
	Required bool
	Field    string // the field that holds the foreign key; empty for none
}

// edgesOf returns the edges of entity i among n entities, in a fixed order.
func edgesOf(i, n int) []edgeSpec {
	var es []edgeSpec
	if i > 0 {
		es = append(es, edgeSpec{Name: "prev", Target: i - 1, Inverse: true, Ref: "next", Unique: true, Required: true, Field: "prev_id"})
	}
	if i < n-1 {
		es = append(es, edgeSpec{Name: "next", Target: i + 1})
	}
	if i%10 == 0 && i+5 < n {
		es = append(es, edgeSpec{Name: "links", Target: i + 5})
	}
	if i%10 == 5 {
		es = append(es, edgeSpec{Name: "linked", Target: i - 5, Inverse: true, Ref: "links"})
	}
	return es
}

// edgeLine is the DSL call for e, without the trailing comma.
func edgeLine(e edgeSpec) string {
	if !e.Inverse {
		return fmt.Sprintf("edge.To(%q, E%d.Type)", e.Name, e.Target)
	}
	s := fmt.Sprintf("edge.From(%q, E%d.Type).Ref(%q)", e.Name, e.Target, e.Ref)
	if e.Unique {
		s += ".Unique()"
	}
	if e.Required {
		s += ".Required()"
	}
	if e.Field != "" {
		s += fmt.Sprintf(".Field(%q)", e.Field)
	}
	return s
}

// fieldLines returns the DSL calls for the fields of entity i, without the
// trailing commas. fk is the DSL's type for the foreign key, "Int64" for
// velox2 and "Int" for the others.
func fieldLines(i int, fk string) []string {
	ls := []string{
		`field.String("name")`,
		`field.Int("count")`,
		`field.Bool("active")`,
		`field.Time("created_at")`,
		`field.String("note").Optional().Nillable()`,
		`field.Enum("status").Values("draft", "live", "gone")`,
	}
	if i > 0 {
		ls = append(ls, `field.`+fk+`("prev_id")`)
	}
	return ls
}

// editMarker is the line of every entity that the schema edit inserts after.
const editMarker = `field.Enum("status").Values("draft", "live", "gone"),`

// editLine is the field the schema edit adds. It is a variable so that a test
// can show that a different edit rebuilds.
var editLine = `field.Int64("extra"),`

// editSchema adds editLine to the last entity's schema file in appDir, after
// editMarker. It fails when the file lacks the marker.
func editSchema(appDir string, o orm, n int) error {
	p := filepath.Join(appDir, o.SchemaDir, fmt.Sprintf("e%d.go", n-1))
	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if !strings.Contains(string(src), editMarker) {
		return fmt.Errorf("cannot edit %s: no %s", p, editMarker)
	}
	edited := strings.Replace(string(src), editMarker, editMarker+"\n\t\t"+editLine, 1)
	return os.WriteFile(p, []byte(edited), 0o644)
}

// serviceEntities returns the five entities service s uses among n: one in each
// of five different groups of ten, offset by 7 groups per service, as bench/scale does.
func serviceEntities(s, n int) []int {
	es := make([]int, 5)
	for m := range es {
		es[m] = 10*((s*7+m)%(n/10)) + m
	}
	return es
}
