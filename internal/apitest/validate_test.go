package apitest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func TestInsertValidates(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	tctx := testmodel.Org.With(context.Background(), int64(7))
	err := db.Insert(&fixture.DAuthor{Name: ""}).Exec(tctx)
	var ve *velox.ValidationError
	if !errors.As(err, &ve) || ve.Field != "Name" || !errors.Is(err, fixture.ErrEmptyName) || err.Error() != "velox: fixture.DAuthor: Name: must not be empty" {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Fatal("a refused row sends nothing")
	}
	if err := db.InsertAll([]fixture.DAuthor{{Name: "ok"}, {Name: "this name is far too long"}}).Exec(tctx); !errors.As(err, &ve) {
		t.Fatalf("InsertAll validates every row: %v", err)
	}
	if e.Calls != 0 {
		t.Fatal("a refused InsertAll sends nothing")
	}
}

func TestUpdateValidatesAndRefusesImmutable(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 1})
	tctx := testmodel.Org.With(context.Background(), int64(7))
	_, err := db.Update[fixture.DAuthor]().Set(fixture.DAuthors.Name.Set("")).Where(fixture.DAuthors.ID.EQ(1)).Exec(tctx)
	var ve *velox.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("got %v", err)
	}
	_, err = db.Update[fixture.DAuthor]().Set(fixture.DAuthors.CreatedAt.Set(time.Now())).Where(fixture.DAuthors.ID.EQ(1)).Exec(tctx)
	if !errors.Is(err, velox.ErrImmutable) || err.Error() != "velox: fixture.DAuthor: CreatedAt: column is immutable" {
		t.Fatalf("got %v", err)
	}
	a := fixture.DAuthor{ID: 1, OrgID: 7, Name: "ada", CreatedAt: time.Now()}
	if err := db.Save(&a, fixture.DAuthors.CreatedAt).Exec(tctx); !errors.Is(err, velox.ErrImmutable) {
		t.Fatalf("Save: %v", err)
	}
	a.Name = ""
	if err := db.Save(&a, fixture.DAuthors.Name).Exec(tctx); !errors.As(err, &ve) {
		t.Fatalf("Save validates: %v", err)
	}
	if err := db.Insert(&fixture.DAuthor{Name: "ada"}).OnConflict(fixture.DAuthors.ID).Update(fixture.DAuthors.CreatedAt).Exec(tctx); !errors.Is(err, velox.ErrImmutable) || err.Error() != "velox: fixture.DAuthor: CreatedAt: column is immutable" {
		t.Fatalf("upsert: %v", err)
	}
}

func TestInsertValidatesAPointerColumnByItsElement(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	bad := "ada"
	err := db.Insert(&fixture.DAuthor{Name: "ada", Email: &bad}).Exec(testmodel.Org.With(context.Background(), int64(7)))
	var ve *velox.ValidationError
	if !errors.As(err, &ve) || ve.Field != "Email" || err.Error() != "velox: fixture.DAuthor: Email: not an email" || e.Calls != 0 {
		t.Fatalf("got %v", err)
	}
	ok := "ada@example.com"
	if err := db.Insert(&fixture.DAuthor{Name: "ada", Email: &ok}).Exec(testmodel.Org.With(context.Background(), int64(7))); err != nil {
		t.Fatal(err)
	}
}

func TestNilPointerIsNotValidated(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	if err := db.Insert(&fixture.DAuthor{Name: "ada"}).Exec(testmodel.Org.With(context.Background(), int64(7))); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateSetNullIsNotValidated(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 1})
	tctx := testmodel.Org.With(context.Background(), int64(7))
	// SetNull of a pointer column assigns nil: not checked.
	if _, err := db.Update[fixture.DAuthor]().Set(fixture.DAuthors.Email.SetNull()).Where(fixture.DAuthors.ID.EQ(1)).Exec(tctx); err != nil {
		t.Fatal(err)
	}
}

// TestValidateBeforeAStatement: Validate checks the given columns of a row,
// and ValidateSet the Set values of an update's assignments, as Insert and
// Update would, without a DB or a statement: a nil pointer and SetNull are
// not checked, an unnamed column is not, and an Immutable column is refused.
func TestValidateBeforeAStatement(t *testing.T) {
	var ve *velox.ValidationError
	bad := "ada"
	err := velox.Validate(&fixture.DAuthor{Name: "", Email: &bad}, fixture.DAuthors.Email)
	if !errors.As(err, &ve) || ve.Field != "Email" || err.Error() != "velox: fixture.DAuthor: Email: not an email" {
		t.Errorf("Validate(Email): %v", err)
	}
	if err := velox.Validate(&fixture.DAuthor{Name: ""}, fixture.DAuthors.Name); !errors.As(err, &ve) || !errors.Is(err, fixture.ErrEmptyName) {
		t.Errorf("Validate(Name): %v", err)
	}
	// An empty name not named, and a nil Email, are not checked.
	if err := velox.Validate(&fixture.DAuthor{Name: ""}, fixture.DAuthors.Email); err != nil {
		t.Errorf("Validate of a nil pointer: %v", err)
	}
	if err := velox.Validate(&fixture.DAuthor{Name: "ada"}, fixture.DAuthors.Name, fixture.DAuthors.Email); err != nil {
		t.Errorf("Validate of a valid row: %v", err)
	}

	if err := velox.ValidateSet(fixture.DAuthors.Name.Set("")); !errors.As(err, &ve) || ve.Field != "Name" {
		t.Errorf("ValidateSet(Name): %v", err)
	}
	if err := velox.ValidateSet(fixture.DAuthors.Name.Set("ada"), fixture.DAuthors.Email.SetNull()); err != nil {
		t.Errorf("ValidateSet of valid values: %v", err)
	}
	if err := velox.ValidateSet(fixture.DAuthors.CreatedAt.Set(time.Now())); !errors.Is(err, velox.ErrImmutable) {
		t.Errorf("ValidateSet(CreatedAt): %v", err)
	}
	if err := velox.ValidateSet[fixture.DAuthor](); err != nil {
		t.Errorf("ValidateSet(): %v", err)
	}
}
