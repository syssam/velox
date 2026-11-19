package engine_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/syssam/velox"
)

// The entities of ADR 0091, on real databases. A conversation, its
// participants and its messages are read through "the conversation is one
// the caller takes part in", which the participants' own policy holds too,
// so applying it inside the subquery reaches it again. pcA and pcB read each
// other: a mutual cycle. pcC has no policy and reads both. pcDoc is visible
// when an approval names its id as text.

// pcUser is the context key of the caller's user id.
type pcUser struct{}

func pcUserOf(ctx context.Context) int64 { v, _ := ctx.Value(pcUser{}).(int64); return v }

func pcAs(user int64) context.Context {
	return context.WithValue(context.Background(), pcUser{}, user)
}

var pcEntityTable velox.EntityTable

func pcEntities() *velox.EntityTable {
	return pcEntityTable.Load(func() []velox.EntityLink {
		return []velox.EntityLink{
			velox.LinkEntity[pcConv]("Conversation", "pc_conversations"),
			velox.LinkEntity[pcPart]("ConversationParticipant", "pc_participants"),
			velox.LinkEntity[pcMsg]("Message", "pc_messages"),
			velox.LinkEntity[pcA]("A", "pc_as"),
			velox.LinkEntity[pcB]("B", "pc_bs"),
			velox.LinkEntity[pcApproval]("Approval", "pc_approvals"),
		}
	})
}

// pcMine is nextapp's rule: column IN (SELECT conversation_id FROM
// participants WHERE user_id = me).
func pcMine(column string) func(context.Context) (velox.Predicate, error) {
	return func(ctx context.Context) (velox.Predicate, error) {
		return velox.FieldInSelect(column, "ConversationParticipant", "conversation_id", velox.FieldEQ("user_id", pcUserOf(ctx))), nil
	}
}

type pcConv struct {
	ID    int64
	Title string
}

type pcPart struct {
	ID             int64
	ConversationID int64
	UserID         int64
}

type pcMsg struct {
	ID             int64
	ConversationID int64
	Body           string
}

func (*pcConv) Policy() velox.Policy { return velox.Policy{Read: pcMine("id")} }
func (*pcPart) Policy() velox.Policy { return velox.Policy{Read: pcMine("conversation_id")} }
func (*pcMsg) Policy() velox.Policy  { return velox.Policy{Read: pcMine("conversation_id")} }

// pcA is visible when its B is open (and visible); pcB when a shown A (that
// is visible) names it.
type pcA struct {
	ID    int64
	BID   int64
	Shown bool
}

type pcB struct {
	ID   int64
	Open bool
}

type pcC struct {
	ID  int64
	AID int64
	BID int64
}

func (*pcA) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) {
		return velox.FieldInSelect("b_id", "B", "id", velox.FieldEQ("open", true)), nil
	}}
}

func (*pcB) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) {
		return velox.FieldInSelect("id", "A", "b_id", velox.FieldEQ("shown", true)), nil
	}}
}

// pcDoc is visible when an approved approval's entity_id, a text column,
// is its id.
type pcDoc struct {
	ID    int64
	Title string
}

type pcApproval struct {
	ID       int64
	EntityID string
	Status   string
}

func (*pcDoc) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) {
		return velox.FieldInSelect("id", "Approval", "entity_id", velox.FieldEQ("status", "approved")), nil
	}}
}

var (
	pcConvMeta     velox.MetaOnce[pcConv]
	pcPartMeta     velox.MetaOnce[pcPart]
	pcMsgMeta      velox.MetaOnce[pcMsg]
	pcAMeta        velox.MetaOnce[pcA]
	pcBMeta        velox.MetaOnce[pcB]
	pcCMeta        velox.MetaOnce[pcC]
	pcDocMeta      velox.MetaOnce[pcDoc]
	pcApprovalMeta velox.MetaOnce[pcApproval]
)

// pcCols is the column descriptors of the given fields and columns, in pairs.
func pcCols(pairs ...string) []velox.ColumnDescriptor {
	cs := make([]velox.ColumnDescriptor, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		cs = append(cs, velox.ColumnDescriptor{Field: pairs[i], Name: pairs[i+1]})
	}
	return cs
}

func (*pcConv) VeloxMeta() *velox.Meta[pcConv] {
	return pcConvMeta.Load(func() velox.Descriptor[pcConv] {
		return velox.Descriptor[pcConv]{
			Table: "pc_conversations", Columns: pcCols("ID", "id", "Title", "title"), PrimaryKey: []int{0},
			Schema: (*pcConv)(nil), Entities: pcEntities,
			FieldPtr: func(x *pcConv, i int) any { return [...]any{&x.ID, &x.Title}[i] },
		}
	})
}

func (*pcPart) VeloxMeta() *velox.Meta[pcPart] {
	return pcPartMeta.Load(func() velox.Descriptor[pcPart] {
		return velox.Descriptor[pcPart]{
			Table: "pc_participants", Columns: pcCols("ID", "id", "ConversationID", "conversation_id", "UserID", "user_id"), PrimaryKey: []int{0},
			Schema: (*pcPart)(nil), Entities: pcEntities,
			FieldPtr: func(x *pcPart, i int) any { return [...]any{&x.ID, &x.ConversationID, &x.UserID}[i] },
		}
	})
}

func (*pcMsg) VeloxMeta() *velox.Meta[pcMsg] {
	return pcMsgMeta.Load(func() velox.Descriptor[pcMsg] {
		return velox.Descriptor[pcMsg]{
			Table: "pc_messages", Columns: pcCols("ID", "id", "ConversationID", "conversation_id", "Body", "body"), PrimaryKey: []int{0},
			Schema: (*pcMsg)(nil), Entities: pcEntities,
			FieldPtr: func(x *pcMsg, i int) any { return [...]any{&x.ID, &x.ConversationID, &x.Body}[i] },
		}
	})
}

func (*pcA) VeloxMeta() *velox.Meta[pcA] {
	return pcAMeta.Load(func() velox.Descriptor[pcA] {
		return velox.Descriptor[pcA]{
			Table: "pc_as", Columns: pcCols("ID", "id", "BID", "b_id", "Shown", "shown"), PrimaryKey: []int{0},
			Schema: (*pcA)(nil), Entities: pcEntities,
			FieldPtr: func(x *pcA, i int) any { return [...]any{&x.ID, &x.BID, &x.Shown}[i] },
		}
	})
}

func (*pcB) VeloxMeta() *velox.Meta[pcB] {
	return pcBMeta.Load(func() velox.Descriptor[pcB] {
		return velox.Descriptor[pcB]{
			Table: "pc_bs", Columns: pcCols("ID", "id", "Open", "open"), PrimaryKey: []int{0},
			Schema: (*pcB)(nil), Entities: pcEntities,
			FieldPtr: func(x *pcB, i int) any { return [...]any{&x.ID, &x.Open}[i] },
		}
	})
}

func (*pcC) VeloxMeta() *velox.Meta[pcC] {
	return pcCMeta.Load(func() velox.Descriptor[pcC] {
		return velox.Descriptor[pcC]{
			Table: "pc_cs", Columns: pcCols("ID", "id", "AID", "a_id", "BID", "b_id"), PrimaryKey: []int{0},
			Entities: pcEntities,
			FieldPtr: func(x *pcC, i int) any { return [...]any{&x.ID, &x.AID, &x.BID}[i] },
		}
	})
}

func (*pcDoc) VeloxMeta() *velox.Meta[pcDoc] {
	return pcDocMeta.Load(func() velox.Descriptor[pcDoc] {
		return velox.Descriptor[pcDoc]{
			Table: "pc_docs", Columns: pcCols("ID", "id", "Title", "title"), PrimaryKey: []int{0},
			Schema: (*pcDoc)(nil), Entities: pcEntities,
			FieldPtr: func(x *pcDoc, i int) any { return [...]any{&x.ID, &x.Title}[i] },
		}
	})
}

func (*pcApproval) VeloxMeta() *velox.Meta[pcApproval] {
	return pcApprovalMeta.Load(func() velox.Descriptor[pcApproval] {
		return velox.Descriptor[pcApproval]{
			Table: "pc_approvals", Columns: pcCols("ID", "id", "EntityID", "entity_id", "Status", "status"), PrimaryKey: []int{0},
			Entities: pcEntities,
			FieldPtr: func(x *pcApproval, i int) any { return [...]any{&x.ID, &x.EntityID, &x.Status}[i] },
		}
	})
}

// pcTables are the tables' columns after id, with I for an integer, T for
// text and B for a boolean, and their rows, written as SQL literals.
var pcTables = []struct {
	name, cols string
	rows       []string
}{
	// Conversation 10 is ann's (1) and bob's (2), 20 bob's and cat's (3),
	// 30 ann's alone.
	{"pc_conversations", "title T", []string{"10, 'ab'", "20, 'bc'", "30, 'a'"}},
	{"pc_participants", "conversation_id I, user_id I", []string{"1, 10, 1", "2, 10, 2", "3, 20, 2", "4, 20, 3", "5, 30, 1"}},
	{"pc_messages", "conversation_id I, body T", []string{"100, 10, 'x'", "101, 10, 'y'", "200, 20, 'z'", "300, 30, 'w'"}},
	// B 1 and 2 are open, 3 is not; A 10 and 13 are shown.
	{"pc_bs", "open B", []string{"1, TRUE", "2, TRUE", "3, FALSE"}},
	{"pc_as", "b_id I, shown B", []string{"10, 1, TRUE", "11, 1, FALSE", "12, 2, FALSE", "13, 3, TRUE"}},
	{"pc_cs", "a_id I, b_id I", []string{"1, 10, 1", "2, 10, 3"}},
	// Doc 1 is approved; doc 2 is pending, and approved only as "02", which
	// is not its id as text.
	{"pc_docs", "title T", []string{"1, 'one'", "2, 'two'", "3, 'three'"}},
	{"pc_approvals", "entity_id T, status T", []string{"1, '1', 'approved'", "2, '2', 'pending'", "3, '02', 'approved'", "4, 'x', 'approved'"}},
}

// pcDBs returns SQLite in memory, and PostgreSQL and MySQL when
// VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL name them, each holding
// pcTables.
func pcDBs(t *testing.T) map[string]*velox.DB {
	t.Helper()
	out := map[string]*velox.DB{}
	open := func(name, driver, dsn string, d velox.Dialect) {
		db, err := velox.Connect(driver, dsn)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Cleanup(func() { _ = db.Close() })
		pcSetup(t, db, d)
		out[name] = db
	}
	open("sqlite", "sqlite", ":memory:", velox.SQLite)
	if dsn := os.Getenv("VELOX_TEST_POSTGRES"); dsn != "" {
		open("postgres", "pgx", dsn, velox.Postgres)
	}
	if dsn := os.Getenv("VELOX_TEST_MYSQL"); dsn != "" {
		open("mysql", "mysql", dsn, velox.MySQL)
	}
	return out
}

func pcSetup(t *testing.T, db *velox.DB, d velox.Dialect) {
	t.Helper()
	bg := context.Background()
	types := map[string]string{"I": "BIGINT", "T": "TEXT", "B": "BOOLEAN"}
	switch d {
	case velox.MySQL:
		types["T"] = "VARCHAR(64)"
	case velox.SQLite:
		types["I"] = "INTEGER"
	}
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(bg, "test schema", q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, tb := range pcTables {
		exec("DROP TABLE IF EXISTS " + tb.name)
		var cols []string
		for c := range strings.SplitSeq(tb.cols, ", ") {
			name, typ, _ := strings.Cut(c, " ")
			cols = append(cols, name+" "+types[typ]+" NOT NULL")
		}
		exec("CREATE TABLE " + tb.name + " (id " + types["I"] + " PRIMARY KEY, " + strings.Join(cols, ", ") + ")")
		for _, r := range tb.rows {
			exec("INSERT INTO " + tb.name + " VALUES (" + r + ")")
		}
		t.Cleanup(func() { _, _ = db.Exec(bg, "test schema", "DROP TABLE IF EXISTS "+tb.name) })
	}
}

// pcIDs reads the ids of E's visible rows under ctx, in order.
func pcIDs[E any, PE velox.Entity[E]](ctx context.Context, t *testing.T, db *velox.DB, id func(*E) int64, ps ...velox.Pred[E]) []int64 {
	t.Helper()
	rows, err := db.Query[E, PE]().Where(ps...).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = id(&rows[i])
	}
	slices.Sort(ids)
	return ids
}

func pcWant(t *testing.T, what string, got []int64, want ...int64) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

// TestPolicyCycleCut: nextapp's conversation rule, whose subquery reads the
// participants through their own policy, which holds the same subquery. The
// inner occurrence is cut (ADR 0091): each user reads exactly the
// conversations, participants and messages of the conversations they take
// part in, and nothing of another's.
func TestPolicyCycleCut(t *testing.T) {
	for name, db := range pcDBs(t) {
		t.Run(name, func(t *testing.T) {
			convID := func(x *pcConv) int64 { return x.ID }
			partID := func(x *pcPart) int64 { return x.ID }
			msgID := func(x *pcMsg) int64 { return x.ID }
			ann, bob, cat, nobody := pcAs(1), pcAs(2), pcAs(3), pcAs(4)
			pcWant(t, "ann's conversations", pcIDs(ann, t, db, convID), 10, 30)
			pcWant(t, "ann's participants", pcIDs(ann, t, db, partID), 1, 2, 5)
			pcWant(t, "ann's messages", pcIDs(ann, t, db, msgID), 100, 101, 300)
			pcWant(t, "bob's conversations", pcIDs(bob, t, db, convID), 10, 20)
			pcWant(t, "bob's participants", pcIDs(bob, t, db, partID), 1, 2, 3, 4)
			pcWant(t, "bob's messages", pcIDs(bob, t, db, msgID), 100, 101, 200)
			pcWant(t, "cat's messages", pcIDs(cat, t, db, msgID), 200)
			pcWant(t, "nobody's conversations", pcIDs(nobody, t, db, convID))
			pcWant(t, "nobody's participants", pcIDs(nobody, t, db, partID))
			pcWant(t, "nobody's messages", pcIDs(nobody, t, db, msgID))
			// Asking for another's rows by id finds none.
			pcWant(t, "ann asks for conversation 20", pcIDs(ann, t, db, convID, velox.Pred[pcConv](velox.FieldEQ("id", 20))))
			pcWant(t, "ann asks for message 200", pcIDs(ann, t, db, msgID, velox.Pred[pcMsg](velox.FieldEQ("conversation_id", 20))))
			pcWant(t, "ann asks for cat's participant row", pcIDs(ann, t, db, partID, velox.Pred[pcPart](velox.FieldEQ("user_id", 3))))
		})
	}
}

// TestPolicyMutualCycleCut: A's policy reads B and B's reads A. Each is
// filtered by the other's policy, cut where it reaches itself: an A is
// visible when its B is open and a shown A names that B; a B when it is open
// and a shown A names it.
func TestPolicyMutualCycleCut(t *testing.T) {
	for name, db := range pcDBs(t) {
		t.Run(name, func(t *testing.T) {
			bg := context.Background()
			pcWant(t, "A", pcIDs(bg, t, db, func(x *pcA) int64 { return x.ID }), 10, 11)
			pcWant(t, "B", pcIDs(bg, t, db, func(x *pcB) int64 { return x.ID }), 1)
			// A statement reaching A, then B on its own: B's condition made
			// inside A's, cut at A, is not reused as B's whole condition,
			// which would let B 3 (named by the shown A 13) through.
			cID := func(x *pcC) int64 { return x.ID }
			both := []velox.Pred[pcC]{
				velox.Pred[pcC](velox.FieldInSelect("a_id", "A", "id")),
				velox.Pred[pcC](velox.FieldInSelect("b_id", "B", "id")),
			}
			pcWant(t, "C through A and B", pcIDs(bg, t, db, cID, both...), 1)
		})
	}
}

// TestFieldInSelectTextKeyOnDatabases: an integer id in a text key column is
// compared as text on every dialect: "1" matches 1, "02" does not match 2.
func TestFieldInSelectTextKeyOnDatabases(t *testing.T) {
	for name, db := range pcDBs(t) {
		t.Run(name, func(t *testing.T) {
			docID := func(x *pcDoc) int64 { return x.ID }
			bg := context.Background()
			pcWant(t, "approved docs", pcIDs(bg, t, db, docID), 1)
			lifted := db.WithoutPolicy("test")
			pcWant(t, "docs with no approval", pcIDs(bg, t, lifted, docID, velox.Pred[pcDoc](velox.Not(velox.FieldInSelect("id", "Approval", "entity_id")))), 3)
		})
	}
}
