// Package z holds the zeros the zerodefault analyzer reports, and those it
// leaves.
package z

import (
	"context"
	"errors"

	"github.com/syssam/velox"
	tm "github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/veloxvet/testdata/gen"
)

// Literals inserted directly.
func literals(ctx context.Context, db *velox.DB) {
	db.Insert(&gen.User{Active: false}).Exec(ctx) // want `Active is false, its zero value, which the field's default replaces: the insert stores true; Users.Active.Set\(false\) in the insert stores false`
	db.Insert(&gen.User{Seats: 0}).Exec(ctx)      // want `Seats is 0, its zero value, which the field's default replaces: the insert stores 100`
	db.Insert(&gen.User{Plan: ""}).Exec(ctx)      // want `Plan is "", its zero value, which the field's default replaces: the insert stores "free"`
	db.Insert(&gen.User{Token: ""}).SQL(ctx)      // want `Token is "", its zero value, which the field's default replaces: the insert stores its DefaultFunc's value`
	db.Insert(&gen.User{
		Name:   "x",
		Active: false,  // want `Active is false`
		Seats:  int(0), // want `Seats is 0`
	}).OnConflict().DoNothing().Exec(ctx)
	db.Insert[gen.User](&gen.User{Active: false}).Exec(ctx) // want `Active is false`
	db.Insert(&gen.User{Rate: 0.0}).Exec(ctx)               // want `Rate is 0, its zero value, which the field's default replaces: the insert stores 1.5; Users.Rate.Set\(0\) in the insert stores 0`
	db.Insert(&gen.User{Big: 0}).Exec(ctx)                  // want `the insert stores 2e\+30;`

	// A real generated entity: Roster's role defaults to "member".
	db.Insert(&tm.Roster{Role: ""}).Exec(ctx) // want `Role is "", its zero value, which the field's default replaces: the insert stores "member"; Rosters.Role.Set\(""\) in the insert stores ""`
	db.Insert(&tm.Roster{Code: ""}).Exec(ctx) // want `Code is ""`

	// Not zero, a zero default, no default, a pointer, an unset field.
	db.Insert(&gen.User{Active: true, Seats: 3, Plan: "pro"}).Exec(ctx)
	db.Insert(&gen.User{Note: "", Name: ""}).Exec(ctx)
	db.Insert(&gen.User{Nick: nil}).Exec(ctx)
	db.Insert(&gen.User{}).Exec(ctx)
	db.Insert(&gen.Plain{ID: 0}).Exec(ctx)
	db.Insert(&tm.Invoice{Note: ""}).Exec(ctx)
}

// A setter can store the zero, so an insert with Set or SetInput is left.
func setters(ctx context.Context, db *velox.DB, in velox.Input[gen.User]) {
	db.Insert(&gen.User{Active: false}).Set(gen.Users.Active.Set(false)).Exec(ctx)
	db.Insert(&gen.User{Active: false}).SetInput(in).Exec(ctx)
	db.Insert(&gen.User{Active: false}).OnConflict().Set(gen.Users.Active.Set(false)).Exec(ctx)
}

// InsertAll and InsertBulk.
func many(ctx context.Context, db *velox.DB) {
	db.InsertAll([]gen.User{{Active: false}, {Active: true}}).Exec(ctx) // want `Active is false`
	db.InsertBulk(
		db.Insert(&gen.User{Seats: 0}), // want `Seats is 0`
		db.Insert(&gen.User{Seats: 0}).Set(gen.Users.Active.Set(false)),
	).Exec(ctx)
}

// Variables.
func variables(ctx context.Context, db *velox.DB, n int) {
	u := gen.User{Active: false} // want `Active is false`
	db.Insert(&u).Exec(ctx)

	p := &gen.User{Name: "p"}
	p.Seats = 0 // want `Seats is 0`
	db.Insert(p).Exec(ctx)

	var v gen.User
	v.Plan = "" // want `Plan is ""`
	db.Insert(&v).Exec(ctx)

	w := new(gen.User)
	w.Active = false // want `Active is false`
	db.Insert(w).Exec(ctx)

	// Set to something else, or after the insert: left.
	a := gen.User{Active: false, Seats: 0} // want `Active is false`
	a.Seats = n
	db.Insert(&a).Exec(ctx)
	a.Active = false

	b := gen.User{Seats: 0}
	b.Seats++
	db.Insert(&b).Exec(ctx)

	c := gen.User{Seats: 0}
	set(&c.Seats)
	db.Insert(&c).Exec(ctx)

	var d = gen.User{Active: false} // want `Active is false`
	_ = d.Label() + d.Name
	db.Insert(&d).Exec(ctx)

	var e = &gen.User{Seats: 0} // want `Seats is 0`
	db.Insert((e)).Exec(ctx)

	// Inserted twice, once with a setter: reported once.
	f := gen.User{Active: false} // want `Active is false`
	db.Insert(&f).Exec(ctx)
	db.Insert(&f).Set(gen.Users.Active.Set(false)).Exec(ctx)
	db.Insert(&f).SQL(ctx)
}

// Chains: parentheses, an explicit instantiation, and what is not followed.
func chains(ctx context.Context, db *velox.DB, xs []gen.User) {
	(db.Insert(&gen.User{Active: false})).Exec(ctx)                       // want `Active is false`
	db.Insert(&gen.User{Active: false}).DoNothing[*gen.User]().Exec(ctx)  // want `Active is false`
	db.Insert(&gen.User{Active: false}).Update(gen.Users.Active).SQL(ctx) // want `Active is false`
	db.InsertBulk((db.Insert(&gen.User{Active: false}))).Exec(ctx)        // want `Active is false`
	run(db.Insert(&gen.User{Active: false}))
	keep := db.Insert(&gen.User{Active: false}).DoNothing[*gen.User]
	keep().Exec(ctx)
	db.InsertAll(xs).Exec(ctx)
	db.Insert(newUser()).Exec(ctx)
	p := newUser()
	p.Active = false
	db.Insert(p).Exec(ctx)
	var q, r = gen.User{Active: false}, 1 // want `Active is false`
	db.Insert(&q).Exec(ctx)
	_ = r
	u := gen.User{Active: false}
	u.VeloxMeta()
	db.Insert(&u).Exec(ctx)
	var v, _ = two()
	db.Insert(&v).Exec(ctx)
	w := gen.User{Active: false} // want `Active is false`
	w.Seats, w.Plan = 0, ""      // want `Seats is 0` `Plan is ""`
	db.Insert(&w).Exec(ctx)
	x := gen.User{Seats: 0}
	x.Seats, _ = two2()
	db.Insert(&x).Exec(ctx)
}

// A zero set on a path that never reaches the insert is not reported.
func paths(ctx context.Context, db *velox.DB, bad bool) error {
	u := gen.User{Active: true}
	if bad {
		u.Active = false
		return errors.New(u.Name)
	}
	db.Insert(&u).Exec(ctx)

	v := gen.User{Active: true}
	if bad {
		v.Active = false // want `Active is false`
	}
	db.Insert(&v).Exec(ctx)

	y := gen.User{Active: true}
	if bad {
		y.Plan = "" // want `Plan is ""`
		if len(y.Name) > 1 {
			y.Name = "long"
		} else {
			y.Name = "short"
		}
	}
	db.Insert(&y).Exec(ctx)

	w := gen.User{Active: true}
	switch {
	case bad:
		w.Active = false
		panic(w.Name)
	default:
		w.Seats = 0 // want `Seats is 0`
	}
	db.Insert(&w).Exec(ctx)

	// In a function literal, the paths are not followed: reported.
	x := gen.User{Active: true}
	func() { x.Active = false }() // want `Active is false`
	db.Insert(&x).Exec(ctx)
	return nil
}

func run(velox.InsertQuery[gen.User]) {}

func newUser() *gen.User { return &gen.User{} }

func two2() (int, error) { return 0, nil }

func two() (gen.User, error) { return gen.User{Active: false}, nil }

func set(p *int) { *p = 1 }

// What the check cannot follow is not reported.
func unknown(ctx context.Context, db *velox.DB, param *gen.User, f func(*gen.User)) {
	param.Active = false
	db.Insert(param).Exec(ctx)

	u := gen.User{Active: false}
	f(&u)
	db.Insert(&u).Exec(ctx)

	q := db.Insert(&gen.User{Active: false})
	q.Exec(ctx)

	d := gen.User{Active: false}
	e := d
	db.Insert(&e).Exec(ctx)

	g := gen.User{Active: false}
	g = gen.User{Active: true}
	db.Insert(&g).Exec(ctx)

	_ = db.Insert(&gen.User{Active: false})
}

// The directive waives the statement that holds the zero.
func waived(ctx context.Context, db *velox.DB) {
	db.Insert(&gen.User{Active: false}).Exec(ctx) //velox:zero-ok the test wants the default
	//velox:zero-ok
	db.Insert(&gen.User{
		Active: false,
	}).Exec(ctx)
	u := gen.User{
		Active: false, //velox:zero-ok
	}
	db.Insert(&u).Exec(ctx)
}

// An entity with a default and no handles variable has no Set to suggest.
func bare(ctx context.Context, db *velox.DB) {
	db.Insert(&gen.Bare{On: false}).Exec(ctx) // want `On is false, its zero value, which the field's default replaces: the insert stores true; a Set of the column in the insert stores false`
}

// More that the check does not follow: a field's address, a variable of an
// entity without defaults, a variable from a call that returns two, and a
// generic entity.
func notFollowed(ctx context.Context, db *velox.DB) {
	h := struct{ u gen.User }{u: gen.User{Active: false}}
	db.Insert(&h.u).Exec(ctx)

	p := gen.Plain{ID: 0}
	db.Insert(&p).Exec(ctx)

	v, _ := two()
	db.Insert(&v).Exec(ctx)
}

func generic[E any, PE velox.Entity[E]](ctx context.Context, db *velox.DB) {
	var e E
	db.Insert[E, PE](&e).Exec(ctx)
}

type bareList []gen.Bare

// A named slice type is InsertAll's slice, and an element without a key
// names its field by position.
func shapes(ctx context.Context, db *velox.DB) {
	db.InsertAll(bareList{{On: false}}).Exec(ctx) // want `On is false, its zero value, which the field's default replaces: the insert stores true; a Set of the column in the insert stores false`
	db.Insert(&gen.Bare{1, false}).Exec(ctx)      // want `On is false, its zero value, which the field's default replaces: the insert stores true; a Set of the column in the insert stores false`
	db.Insert(&gen.Bare{1, true}).Exec(ctx)
}
