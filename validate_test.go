package velox_test

import (
	"errors"
	"testing"
	"time"

	"github.com/syssam/velox2"
)

func TestInsertValidates(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	tctx := velox.WithTenant(ctx, int64(7))
	err := db.Insert(&dAuthor{Name: ""}).Exec(tctx)
	var ve *velox.ValidationError
	if !errors.As(err, &ve) || ve.Field != "Name" || !errors.Is(err, errEmptyName) || err.Error() != "velox: velox_test.dAuthor: Name: must not be empty" {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Fatal("a refused row sends nothing")
	}
	if err := db.InsertAll([]dAuthor{{Name: "ok"}, {Name: "this name is far too long"}}).Exec(tctx); !errors.As(err, &ve) {
		t.Fatalf("InsertAll validates every row: %v", err)
	}
	if e.Calls != 0 {
		t.Fatal("a refused InsertAll sends nothing")
	}
}

func TestUpdateValidatesAndRefusesImmutable(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
	tctx := velox.WithTenant(ctx, int64(7))
	_, err := db.Update[dAuthor]().Set(dAuthors.Name.Set("")).Where(dAuthors.ID.EQ(1)).Exec(tctx)
	var ve *velox.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("got %v", err)
	}
	_, err = db.Update[dAuthor]().Set(dAuthors.CreatedAt.Set(time.Now())).Where(dAuthors.ID.EQ(1)).Exec(tctx)
	if !errors.Is(err, velox.ErrImmutable) || err.Error() != "velox: velox_test.dAuthor: CreatedAt: column is immutable" {
		t.Fatalf("got %v", err)
	}
	a := dAuthor{ID: 1, OrgID: 7, Name: "ada", CreatedAt: time.Now()}
	if err := db.Save(&a, dAuthors.CreatedAt).Exec(tctx); !errors.Is(err, velox.ErrImmutable) {
		t.Fatalf("Save: %v", err)
	}
	a.Name = ""
	if err := db.Save(&a, dAuthors.Name).Exec(tctx); !errors.As(err, &ve) {
		t.Fatalf("Save validates: %v", err)
	}
	if err := db.Insert(&dAuthor{Name: "ada"}).OnConflict(dAuthors.ID).Update(dAuthors.CreatedAt).Exec(tctx); !errors.Is(err, velox.ErrImmutable) || err.Error() != "velox: velox_test.dAuthor: CreatedAt: column is immutable" {
		t.Fatalf("upsert: %v", err)
	}
}

func TestInsertValidatesAPointerColumnByItsElement(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	bad := "ada"
	err := db.Insert(&dAuthor{Name: "ada", Email: &bad}).Exec(velox.WithTenant(ctx, int64(7)))
	var ve *velox.ValidationError
	if !errors.As(err, &ve) || ve.Field != "Email" || err.Error() != "velox: velox_test.dAuthor: Email: not an email" || e.Calls != 0 {
		t.Fatalf("got %v", err)
	}
	ok := "ada@example.com"
	if err := db.Insert(&dAuthor{Name: "ada", Email: &ok}).Exec(velox.WithTenant(ctx, int64(7))); err != nil {
		t.Fatal(err)
	}
}

func TestNilPointerIsNotValidated(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	if err := db.Insert(&dAuthor{Name: "ada"}).Exec(velox.WithTenant(ctx, int64(7))); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateSetNullIsNotValidated(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
	tctx := velox.WithTenant(ctx, int64(7))
	// SetNull of a pointer column assigns nil: not checked.
	if _, err := db.Update[dAuthor]().Set(dAuthors.Email.SetNull()).Where(dAuthors.ID.EQ(1)).Exec(tctx); err != nil {
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
	err := velox.Validate(&dAuthor{Name: "", Email: &bad}, dAuthors.Email)
	if !errors.As(err, &ve) || ve.Field != "Email" || err.Error() != "velox: velox_test.dAuthor: Email: not an email" {
		t.Errorf("Validate(Email): %v", err)
	}
	if err := velox.Validate(&dAuthor{Name: ""}, dAuthors.Name); !errors.As(err, &ve) || !errors.Is(err, errEmptyName) {
		t.Errorf("Validate(Name): %v", err)
	}
	// An empty name not named, and a nil Email, are not checked.
	if err := velox.Validate(&dAuthor{Name: ""}, dAuthors.Email); err != nil {
		t.Errorf("Validate of a nil pointer: %v", err)
	}
	if err := velox.Validate(&dAuthor{Name: "ada"}, dAuthors.Name, dAuthors.Email); err != nil {
		t.Errorf("Validate of a valid row: %v", err)
	}

	if err := velox.ValidateSet(dAuthors.Name.Set("")); !errors.As(err, &ve) || ve.Field != "Name" {
		t.Errorf("ValidateSet(Name): %v", err)
	}
	if err := velox.ValidateSet(dAuthors.Name.Set("ada"), dAuthors.Email.SetNull()); err != nil {
		t.Errorf("ValidateSet of valid values: %v", err)
	}
	if err := velox.ValidateSet(dAuthors.CreatedAt.Set(time.Now())); !errors.Is(err, velox.ErrImmutable) {
		t.Errorf("ValidateSet(CreatedAt): %v", err)
	}
	if err := velox.ValidateSet[dAuthor](); err != nil {
		t.Errorf("ValidateSet(): %v", err)
	}
}
