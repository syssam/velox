package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/example/tracker/gen"
)

const (
	sessionLife = 7 * 24 * time.Hour
	saltLen     = 16
	keyLen      = 32
)

// hashIterations is PBKDF2-HMAC-SHA256's work factor, as OWASP recommends. The
// tests lower it.
var hashIterations = 600_000

// errBadLogin answers a wrong organization, email and password alike, so that a
// caller cannot tell which of them exists.
var errBadLogin = errors.New("wrong organization, email or password")

// hashPassword returns the base64 of a new salt and the password's key.
func hashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, hashIterations, keyLen)
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(append(salt, key...)), nil
}

// passwordMatches reports whether password is the one hashed into stored.
func passwordMatches(stored, password string) bool {
	b, err := base64.RawStdEncoding.DecodeString(stored)
	if err != nil || len(b) != saltLen+keyLen {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, b[:saltLen], hashIterations, keyLen)
	return err == nil && subtle.ConstantTimeCompare(key, b[saltLen:]) == 1
}

// newSession inserts a session for u and returns its token. Only the token's hash
// is stored.
func newSession(ctx context.Context, db *velox.DB, u *gen.User) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s := gen.Session{TokenHash: hashToken(token), OrgID: u.OrgID, UserID: u.ID, ExpiresAt: time.Now().Add(sessionLife)}
	if err := db.Insert(&s).Exec(ctx); err != nil {
		return "", err
	}
	return token, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// signup creates an organization and its first user, an admin, together: a failure
// leaves neither.
func (s *server) signup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Org, Email, Name, Password string
		OrgName                    string `json:"org_name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Password) < 8 {
		writeJSON(w, http.StatusUnprocessableEntity, errorBody{"the password must have at least 8 characters"})
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var token string
	err = s.db.Tx(r.Context(), func(ctx context.Context, tx *velox.DB) error {
		org := gen.Org{Slug: in.Org, Name: in.OrgName}
		if err := tx.Insert(&org).Exec(ctx); err != nil {
			return err
		}
		ctx = velox.WithTenant(ctx, org.ID)
		u := gen.User{Email: in.Email, Name: in.Name, PasswordHash: hash, Role: gen.UserRoleAdmin}
		if err := tx.Insert(&u).Exec(ctx); err != nil {
			return err
		}
		token, err = newSession(ctx, tx, &u)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"token": token})
}

// invite adds a member to the caller's organization; only an admin may.
func (s *server) invite(w http.ResponseWriter, r *http.Request) {
	if p, _ := gen.PrincipalFromContext(r.Context()); !p.Admin {
		writeJSON(w, http.StatusForbidden, errorBody{"only an admin can add a user"})
		return
	}
	var in struct{ Email, Name, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Password) < 8 {
		writeJSON(w, http.StatusUnprocessableEntity, errorBody{"the password must have at least 8 characters"})
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u := gen.User{Email: in.Email, Name: in.Name, PasswordHash: hash, Role: gen.UserRoleMember}
	if err := s.db.Insert(&u).Exec(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, &u)
}

// login exchanges an organization, an email and a password for a token.
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Org, Email, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	u, err := s.findLogin(r.Context(), in.Org, in.Email)
	switch {
	case velox.IsNotFound(err):
		// Hash anyway, so that an unknown email takes as long as a wrong password.
		_, _ = hashPassword(in.Password)
		writeJSON(w, http.StatusUnauthorized, errorBody{errBadLogin.Error()})
		return
	case err != nil:
		s.fail(w, r, err)
		return
	case !passwordMatches(u.PasswordHash, in.Password):
		writeJSON(w, http.StatusUnauthorized, errorBody{errBadLogin.Error()})
		return
	}
	token, err := newSession(velox.WithTenant(r.Context(), u.OrgID), s.db, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// findLogin finds a user by organization and email. The Org is found first, which
// gives the tenant the User statement is scoped to.
func (s *server) findLogin(ctx context.Context, slug, email string) (*gen.User, error) {
	org, err := s.db.Query[gen.Org]().Where(gen.Orgs.Slug.EQ(slug)).One(ctx)
	if err != nil {
		return nil, err
	}
	return s.db.Query[gen.User]().Where(gen.Users.Email.EQ(email)).One(velox.WithTenant(ctx, org.ID))
}

// logout ends the session of the request's token.
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	_, err := s.db.Delete[gen.Session]().Where(gen.Sessions.TokenHash.EQ(hashToken(bearer(r)))).Exec(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// me returns the caller.
func (s *server) me(w http.ResponseWriter, r *http.Request) {
	p, _ := gen.PrincipalFromContext(r.Context())
	u, err := s.db.Query[gen.User]().Where(gen.Users.ID.EQ(p.UserID)).One(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func bearer(r *http.Request) string {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token
}

// authed serves next with the request's context naming the caller's tenant and
// the caller, which every tenant-scoped statement and policy then reads. A request
// without a live session gets a 401.
func (s *server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sess, err := s.db.Query[gen.Session]().
			Where(gen.Sessions.TokenHash.EQ(hashToken(bearer(r))), gen.Sessions.ExpiresAt.GT(time.Now())).
			One(ctx)
		if velox.IsNotFound(err) {
			writeJSON(w, http.StatusUnauthorized, errorBody{"log in first"})
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		ctx = velox.WithTenant(ctx, sess.OrgID)
		u, err := s.db.Query[gen.User]().Where(gen.Users.ID.EQ(sess.UserID)).One(ctx)
		if velox.IsNotFound(err) {
			writeJSON(w, http.StatusUnauthorized, errorBody{"log in first"})
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		ctx = gen.WithPrincipal(ctx, gen.Principal{UserID: u.ID, Admin: u.Role == gen.UserRoleAdmin})
		next(w, r.WithContext(ctx))
	}
}
