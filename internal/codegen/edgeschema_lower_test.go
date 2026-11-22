package codegen

import (
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Riders take routes, each through a Ticket, whose fields are one of each
// kind Add may or may not fill.
type Rider struct{ schema.Schema }

func (Rider) Edges() []schema.Edge {
	return []schema.Edge{edge.To("routes", Route.Type).Through("tickets", Ticket.Type)}
}

type Route struct{ schema.Schema }

func (Route) Edges() []schema.Edge {
	return []schema.Edge{edge.From("riders", Rider.Type).Ref("routes")}
}

// stamped is a mixin with a field Add fills and one it cannot.
type stamped struct{ mixin.Schema }

func (stamped) Fields() []schema.Field {
	return []schema.Field{field.String("stamp").Default("x"), field.String("origin")}
}

type Ticket struct{ schema.Schema }

func (Ticket) Mixin() []schema.Mixin { return []schema.Mixin{stamped{}} }
func (Ticket) Fields() []schema.Field {
	return []schema.Field{
		field.Enum("class").Values("first", "second").Optional().Default("second"),
		field.String("note"),
		field.String("memo").Optional(),
		field.Int64("version").Version(),
		field.Time("touched_at").UpdateDefault(time.Now),
		field.JSON("extra", map[string]int{}).Optional(),
		field.Bool("picked"),
		field.String("nick").Optional().Nillable(),
		field.Time("made_at").Default(time.Now),
		field.String("seat").SchemaDefault(map[string]string{field.Postgres: "'a'", field.MySQL: "'a'", field.SQLite: "'a'"}),
		field.Int64("rider_id"),
		field.Int64("route_id"),
	}
}
func (Ticket) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("rider", Rider.Type).Unique().Required().Field("rider_id"),
		edge.To("route", Route.Type).Unique().Required().Field("route_id"),
	}
}

// Keys whose ID velox does not generate: Pass's has a DefaultFunc, Stub's
// none.
type Holder struct{ schema.Schema }

func (Holder) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("gates", Gate.Type).Through("passes", Pass.Type),
		edge.To("doors", Door.Type).Through("stubs", Stub.Type),
	}
}

type Gate struct{ schema.Schema }

func (Gate) Edges() []schema.Edge {
	return []schema.Edge{edge.From("holders", Holder.Type).Ref("gates")}
}

type Door struct{ schema.Schema }

func (Door) Edges() []schema.Edge {
	return []schema.Edge{edge.From("holders", Holder.Type).Ref("doors")}
}

type Pass struct{ schema.Schema }

func (Pass) Fields() []schema.Field {
	return []schema.Field{
		field.String("id").DefaultFunc(func() string { return "p" }),
		field.Int64("holder_id"), field.Int64("gate_id"),
	}
}
func (Pass) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("holder", Holder.Type).Unique().Required().Field("holder_id"),
		edge.To("gate", Gate.Type).Unique().Required().Field("gate_id"),
	}
}

type Stub struct{ schema.Schema }

func (Stub) Fields() []schema.Field {
	return []schema.Field{field.String("id"), field.Int64("holder_id"), field.Int64("door_id")}
}
func (Stub) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("holder", Holder.Type).Unique().Required().Field("holder_id"),
		edge.To("door", Door.Type).Unique().Required().Field("door_id"),
	}
}

// relDesc is the descriptor expression of e's relation name.
func relDesc(t *testing.T, o *Out, e, name string) string {
	t.Helper()
	for _, r := range o.entity(e).Rels {
		if r.Name == name {
			return r.Desc
		}
	}
	t.Fatalf("%s has no relation %s", e, name)
	return ""
}

// An edge schema's relation is EdgeThrough of the edge schema, from each
// side, with its columns in that side's order, and the columns Add cannot
// fill after them: a required field with no default of any kind. Optional
// fields (ADR 0090), Nillable ones, a Version, a database default and a
// generated key are filled.
func TestEdgeSchemaLowerUnfilled(t *testing.T) {
	o := mustLower(t, Config{Package: "gen", SchemaPath: lowerSchemaPath}, Rider{}, Route{}, Ticket{})
	if o.entity("RiderRoute") != nil {
		t.Error("a join entity is generated beside the edge schema")
	}
	cols := "ticketColOrigin, ticketColNote, ticketColTouchedAt, ticketColPicked"
	if got, want := relDesc(t, o, "Rider", "routes"),
		`velox.EdgeThrough[Ticket, Route]("routes", riderColID, ticketColRiderID, ticketColRouteID, routeColID, `+cols+`)`; got != want {
		t.Errorf("routes:\n got %s\nwant %s", got, want)
	}
	if got, want := relDesc(t, o, "Route", "riders"),
		`velox.EdgeThrough[Ticket, Rider]("riders", routeColID, ticketColRouteID, ticketColRiderID, riderColID, `+cols+`)`; got != want {
		t.Errorf("riders:\n got %s\nwant %s", got, want)
	}
	if got, want := relDesc(t, o, "Rider", "tickets"), `velox.EdgeTo[Ticket]("tickets", riderColID, ticketColRiderID)`; got != want {
		t.Errorf("tickets:\n got %s\nwant %s", got, want)
	}

	o = mustLower(t, Config{Package: "gen", SchemaPath: lowerSchemaPath}, Holder{}, Gate{}, Door{}, Pass{}, Stub{})
	if got := relDesc(t, o, "Holder", "gates"); strings.Contains(got, "passColID") {
		t.Errorf("a key with a DefaultFunc is unfilled: %s", got)
	}
	if got := relDesc(t, o, "Holder", "doors"); !strings.HasSuffix(got, ", doorColID, stubColID)") {
		t.Errorf("a string key with no default is filled: %s", got)
	}
}
