package schema

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// NewRosterCode is a random code, Roster's default for its second unique
// key.
func NewRosterCode() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Player is on teams, each through a Roster.
type Player struct{ schema.Schema }

// Fields returns the name.
func (Player) Fields() []schema.Field { return []schema.Field{field.String("name")} }

// Edges returns Teams, many to many through the edge schema Roster, and
// its through edge Rosters.
func (Player) Edges() []schema.Edge {
	return []schema.Edge{edge.To("teams", Team.Type).Through("rosters", Roster.Type)}
}

// Team has players, the inverse of Player's Teams.
type Team struct{ schema.Schema }

// Fields returns the name.
func (Team) Fields() []schema.Field { return []schema.Field{field.String("name")} }

// Edges returns Players, the inverse of Player's Teams.
func (Team) Edges() []schema.Edge {
	return []schema.Edge{edge.From("players", Player.Type).Ref("teams")}
}

// Roster is the edge schema of Player's Teams: a link with a role, the time
// it was made, and a code, a second unique key. Each column has a default,
// so Rel.Add fills it.
type Roster struct{ schema.Schema }

// Fields returns the role, the time, the code and the two keys.
func (Roster) Fields() []schema.Field {
	return []schema.Field{
		field.String("role").Default("member"),
		field.Time("joined_at").Default(time.Now).Immutable(),
		field.String("code").MaxLen(16).DefaultFunc(NewRosterCode).Unique(),
		field.Int64("player_id"),
		field.Int64("team_id"),
	}
}

// Edges returns the two keys, which delete a link with either end.
func (Roster) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("player", Player.Type).Unique().Required().Field("player_id").OnDelete(edge.Cascade),
		edge.To("team", Team.Type).Unique().Required().Field("team_id").OnDelete(edge.Cascade),
	}
}

// Policy hides a captain's link from writes: Remove and Clear keep it.
func (Roster) Policy() velox.Policy {
	return velox.Policy{Write: func(context.Context) (velox.Predicate, error) {
		return velox.FieldNEQ("role", "captain"), nil
	}}
}
