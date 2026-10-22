package veloxgql_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl"
)

// keeper and pet are hand-written entities, as velox-gen would write them,
// for batching's tests: a keeper has pets, which are tenant-scoped.
type keeper struct {
	ID   int64
	Name string
	Pets velox.Many[pet]
}

var keepers = struct {
	ID   velox.NumberCol[keeper, int64]
	Name velox.StringCol[keeper, string]
	Pets velox.Rel[keeper, pet]
}{velox.NumberCol[keeper, int64]{I: 0}, velox.StringCol[keeper, string]{I: 1}, velox.Rel[keeper, pet]{I: 0}}

var keeperMeta velox.MetaOnce[keeper]

func (*keeper) VeloxMeta() *velox.Meta[keeper] { return keeperMeta.Load(keeperDesc) }

func keeperDesc() velox.Desc[keeper] {
	return velox.Desc[keeper]{
		Table:      "keepers",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []velox.Relation{velox.EdgeTo[pet]("Pets", 0, 1)},
		Addr:       func(x *keeper, i int) any { return [...]any{&x.ID, &x.Name}[i] },
		RelAddr: func(x *keeper, i int) any {
			if i == 0 {
				return &x.Pets
			}
			return nil
		},
	}
}

type pet struct {
	ID       int64
	KeeperID int64
	OrgID    int64
	Name     string
}

var pets = struct {
	ID velox.NumberCol[pet, int64]
}{velox.NumberCol[pet, int64]{I: 0}}

var petMeta velox.MetaOnce[pet]

func (*pet) VeloxMeta() *velox.Meta[pet] { return petMeta.Load(petDesc) }

func petDesc() velox.Desc[pet] {
	return velox.Desc[pet]{
		Table:      "pets",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "KeeperID", Name: "keeper_id"}, {Field: "OrgID", Name: "org_id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Tenant:     2,
		Addr:       func(x *pet, i int) any { return [...]any{&x.ID, &x.KeeperID, &x.OrgID, &x.Name}[i] },
	}
}

// batchSDL is the schema of batching's tests: keepers are batched, loose
// keepers are not.
const batchSDL = `
type Query {
	keepers: [Keeper!]!
	loose: [Keeper!]!
}
type Mutation {
	addPet(id: Int!, keeper: Int!): [Keeper!]!
}
type Keeper {
	name: String!
	pets: PetConnection!
}
type PetConnection {
	totalCount: Int!
	nodes: [Pet!]!
}
type Pet {
	name: String!
}
`

// batchDB opens an in-memory SQLite database of keepers 1 "ann", 2 "bob"
// and 3 "cy", and their pets: of org 1, ann's "rex" and bob's "fifi"; of org
// 2, ann's "tom", bob's "kit" and cy's "max". The log records each
// statement.
func batchDB(t *testing.T) (*velox.DB, *txLog) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	for _, s := range []string{
		`CREATE TABLE keepers (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE pets (id INTEGER PRIMARY KEY, keeper_id INTEGER NOT NULL, org_id INTEGER NOT NULL, name TEXT NOT NULL)`,
		`INSERT INTO keepers (id, name) VALUES (1, 'ann'), (2, 'bob'), (3, 'cy')`,
		`INSERT INTO pets (id, keeper_id, org_id, name) VALUES (1, 1, 1, 'rex'), (2, 1, 2, 'tom'), (3, 2, 1, 'fifi'), (4, 2, 2, 'kit'), (5, 3, 2, 'max')`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	l := &txLog{}
	return velox.Open(velox.SQLite, raw, velox.WithAround(l.around), velox.WithTxAround(l.txAround)), l
}

// batchServer serves batchSDL on db. keepers passes its rows to
// veloxgql.Batch, and loose does not; a keeper's pets is
// gqlimpl.EdgeConnection, as the generated code's edges are.
func batchServer(db *velox.DB) http.Handler {
	parent := func(ctx context.Context) any { return graphql.GetFieldContext(ctx).Parent.Result }
	read := func(batch bool) resolver {
		return func(ctx context.Context, _ map[string]any) (any, error) {
			db, err := veloxgql.DBFrom(ctx)
			if err != nil {
				return nil, err
			}
			rows, err := db.Query[keeper]().OrderBy(keepers.ID.Asc()).Unbounded().All(ctx)
			if err != nil {
				return nil, err
			}
			ptrs := make([]*keeper, len(rows))
			out := make([]any, len(rows))
			for i := range rows {
				ptrs[i], out[i] = &rows[i], &rows[i]
			}
			if batch {
				veloxgql.Batch(ctx, ptrs)
			}
			return out, nil
		}
	}
	key := []gqlimpl.OrderTerm[pet]{gqlimpl.Term("id", veloxgql.OrderDirectionAsc, pets.ID.Asc(), pets.ID.Desc(),
		func(x *pet) int64 { return x.ID }, func(x *pet, v int64) { x.ID = v })}
	return serveSchema(testSchemaOf(batchSDL, map[string]resolver{
		"keepers": read(true),
		"loose":   read(false),
		"addPet": func(ctx context.Context, args map[string]any) (any, error) {
			db, err := veloxgql.DBFrom(ctx)
			if err != nil {
				return nil, err
			}
			p := &pet{ID: args["id"].(int64), KeeperID: args["keeper"].(int64), OrgID: 1, Name: "new"}
			if err := db.Insert(p).Exec(ctx); err != nil {
				return nil, err
			}
			return read(true)(ctx, nil)
		},
		"Keeper.name": func(ctx context.Context, _ map[string]any) (any, error) { return parent(ctx).(*keeper).Name, nil },
		"Keeper.pets": func(ctx context.Context, _ map[string]any) (any, error) {
			db, err := veloxgql.DBFrom(ctx)
			if err != nil {
				return nil, err
			}
			c, err := gqlimpl.NewConnection(ctx, db.Query[pet](), "Pet", gqlimpl.Args{}, nil, key)
			if err != nil {
				return nil, err
			}
			return gqlimpl.EdgeConnection(ctx, c, parent(ctx).(*keeper), keepers.Pets), nil
		},
		"PetConnection.totalCount": func(ctx context.Context, _ map[string]any) (any, error) {
			return parent(ctx).(*gqlimpl.Connection[pet]).TotalCount(ctx)
		},
		"PetConnection.nodes": func(ctx context.Context, _ map[string]any) (any, error) {
			p, err := parent(ctx).(*gqlimpl.Connection[pet]).Page(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]any, len(p.Rows))
			for i, r := range p.Rows {
				out[i] = r
			}
			return out, nil
		},
		"Pet.name": func(ctx context.Context, _ map[string]any) (any, error) { return parent(ctx).(*pet).Name, nil },
	}), db, veloxgql.WithLimits(veloxgql.Limits{MaxList: 10})) // lists cost MaxList each
}

// petsOf is the response to field { name pets { totalCount nodes { name } } }
// for the keepers' pets, as name: pet, pet….
func petsOf(field string, keepers ...string) string {
	var ks []string
	for _, k := range keepers {
		name, ps, _ := strings.Cut(k, ":")
		var nodes []string
		for p := range strings.SplitSeq(ps, ",") {
			if p != "" {
				nodes = append(nodes, fmt.Sprintf(`{"name":%q}`, p))
			}
		}
		ks = append(ks, fmt.Sprintf(`{"name":%q,"pets":{"totalCount":%d,"nodes":[%s]}}`, name, len(nodes), strings.Join(nodes, ",")))
	}
	return fmt.Sprintf(`{"data":{%q:[%s]}}`, field, strings.Join(ks, ","))
}

// TestBatchPublic: the rows given to Batch batch their edges, a load and a
// count for every keeper; rows not given to it read their own.
func TestBatchPublic(t *testing.T) {
	db, log := batchDB(t)
	srv := batchServer(db)
	ctx := velox.WithTenant(context.Background(), int64(1))
	for _, c := range []struct {
		field string
		stmts int
	}{{"keepers", 3}, {"loose", 7}} {
		got := post(t, ctx, srv, `{ `+c.field+` { name pets { totalCount nodes { name } } } }`)
		if want := petsOf(c.field, "ann:rex", "bob:fifi", "cy:"); got != want {
			t.Errorf("%s:\n got %s\nwant %s", c.field, got, want)
		}
		if stmts, _ := log.take(); len(stmts) != c.stmts {
			t.Errorf("%s: %d statements, want %d: %q", c.field, len(stmts), c.stmts, stmts)
		}
	}
}

// TestBatchInMutation: in a mutation, whose resolvers the extension runs one
// at a time on its transaction, a batch still reads the edge of every row in
// one statement, the transaction's own writes among them, and waits on no
// other resolver.
func TestBatchInMutation(t *testing.T) {
	db, log := batchDB(t)
	srv := batchServer(db)
	ctx, cancel := context.WithTimeout(velox.WithTenant(context.Background(), int64(1)), 10*time.Second)
	defer cancel()
	got := post(t, ctx, srv, `mutation { addPet(id: 9, keeper: 1) { name pets { totalCount nodes { name } } } }`)
	if want := petsOf("addPet", "ann:rex,new", "bob:fifi", "cy:"); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	stmts, txs := log.take()
	if want := []string{"insert in tx", "select in tx", "select in tx", "select in tx"}; !slices.Equal(stmts, want) || !slices.Equal(txs, []string{"commit"}) {
		t.Errorf("statements %q and transactions %q, want %q and one commit", stmts, txs, want)
	}
}

// TestRequestsIsolated: concurrent requests of different tenants batch in
// tables of their own: each reads its tenant's pets only, in its own
// statements.
func TestRequestsIsolated(t *testing.T) {
	db, log := batchDB(t)
	srv := batchServer(db)
	const query = `{ keepers { name pets { totalCount nodes { name } } } }`
	want := map[int64]string{
		1: petsOf("keepers", "ann:rex", "bob:fifi", "cy:"),
		2: petsOf("keepers", "ann:tom", "bob:kit", "cy:max"),
	}
	const rounds = 10
	var wg sync.WaitGroup
	for org := range int64(2) {
		org++
		wg.Go(func() {
			ctx := velox.WithTenant(context.Background(), org)
			for range rounds {
				if got := post(t, ctx, srv, query); got != want[org] {
					t.Errorf("org %d:\n got %s\nwant %s", org, got, want[org])
				}
			}
		})
	}
	wg.Wait()
	if stmts, _ := log.take(); len(stmts) != 2*rounds*3 {
		t.Errorf("%d statements, want three per request", len(stmts))
	}
}
