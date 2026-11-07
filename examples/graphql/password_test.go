package main

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

// checkPassword reports whether the stored hash, as graph's hashPassword
// writes it, is password's.
func checkPassword(t *testing.T, stored, password string) bool {
	t.Helper()
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		t.Fatalf("stored hash %q", stored)
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter != 600_000 {
		t.Fatalf("iterations %q: %v", parts[1], err)
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	key, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(salt) != 16 || len(key) != 32 {
		t.Fatalf("salt %q, key %q: %v %v", parts[2], parts[3], err1, err2)
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(key))
	if err != nil {
		t.Fatal(err)
	}
	return subtle.ConstantTimeCompare(got, key) == 1
}

// passwordHashOf is the stored hash of the user named name.
func (s *testServer) passwordHashOf(t *testing.T, name string) string {
	t.Helper()
	var h string
	if err := s.sql.QueryRowContext(t.Context(), `SELECT password_hash FROM users WHERE name = ?`, name).Scan(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

// TestCreateUserHashesPassword: createUser takes a password, and the row
// holds the server's salted PBKDF2 hash of it, never the password, and a
// different hash for the same password.
func TestCreateUserHashesPassword(t *testing.T) {
	s := newServer(t)
	for _, name := range []string{"Dana", "Eve"} {
		s.do(t, request{
			role:  "admin",
			query: `mutation { createUser(input: {name: "` + name + `", email: "x@example.com", joinedAt: "2026-07-01T09:00:00Z"}, password: "correct horse") { name } }`,
			want:  `{"data": {"createUser": {"name": "` + name + `"}}}`,
			stmts: []string{`INSERT INTO "users" ("name", "email", "role", "joined_at", "password_hash") VALUES (?, ?, ?, ?, ?) RETURNING "id" [` + name + ` x@example.com member 2026-07-01 09:00:00 +0000 UTC <hash>]`},
		})
	}
	dana, eve := s.passwordHashOf(t, "Dana"), s.passwordHashOf(t, "Eve")
	if !checkPassword(t, dana, "correct horse") || checkPassword(t, dana, "correct horsf") {
		t.Errorf("Dana's hash %q does not check the password alone", dana)
	}
	if dana == eve {
		t.Errorf("one password, one hash %q: no salt", dana)
	}
}

// TestPasswordRules: a password under 8 characters is BAD_USER_INPUT, with
// no statement; the hash is no input field a client could set.
func TestPasswordRules(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		role:  "admin",
		query: `mutation { createUser(input: {name: "Dana", email: "x@example.com", joinedAt: "2026-07-01T09:00:00Z"}, password: "short") { name } }`,
		want:  `{"errors": [{"message": "password: at least 8 characters", "path": ["createUser"], "extensions": {"code": "BAD_USER_INPUT"}}], "data": null}`,
	})
	s.do(t, request{
		role:  "admin",
		query: `mutation { createUser(input: {name: "Dana", email: "x@example.com", joinedAt: "2026-07-01T09:00:00Z", passwordHash: "x"}, password: "correct horse") { name } }`,
		want:  `{"errors": [{"message": "Field \"passwordHash\" is not defined by type \"CreateUserInput\".", "extensions": {"code": "GRAPHQL_VALIDATION_FAILED"}}], "data": null}`,
	})
}

// TestUpdateUserPassword: updateUser with a password writes its hash in the
// UPDATE of the input's fields; without one, the hash is left as it is.
func TestUpdateUserPassword(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		role:  "admin",
		query: `mutation { updateUser(id: "` + user2 + `", input: {name: "Bea"}, password: "new secret") { name } }`,
		want:  `{"data": {"updateUser": {"name": "Bea"}}}`,
		stmts: []string{
			`SELECT "id" FROM "users" WHERE "id" = ? LIMIT ? [2 2]`,
			`UPDATE "users" SET "name" = ?, "password_hash" = ? WHERE "id" = ? [Bea <hash> 2]`,
			selUsers + ` WHERE "id" = ? LIMIT ? [2 2]`,
		},
	})
	if h := s.passwordHashOf(t, "Bea"); !checkPassword(t, h, "new secret") {
		t.Errorf("Bea's hash %q is not the new password's", h)
	}
	before := s.passwordHashOf(t, "Bea")
	s.do(t, request{
		role:  "admin",
		query: `mutation { updateUser(id: "` + user2 + `", input: {name: "Bria"}) { name } }`,
		want:  `{"data": {"updateUser": {"name": "Bria"}}}`,
		stmts: []string{
			`SELECT "id" FROM "users" WHERE "id" = ? LIMIT ? [2 2]`,
			`UPDATE "users" SET "name" = ? WHERE "id" = ? [Bria 2]`,
			selUsers + ` WHERE "id" = ? LIMIT ? [2 2]`,
		},
	})
	if h := s.passwordHashOf(t, "Bria"); h != before {
		t.Errorf("an update with no password changed the hash: %q, was %q", h, before)
	}
}
