package graph

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"unicode/utf8"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// The parameters of a password's hash: PBKDF2-HMAC-SHA256 with 600,000
// iterations and a 16-byte random salt, as OWASP's password storage cheat
// sheet advises for PBKDF2. An application may prefer argon2id or bcrypt,
// from golang.org/x/crypto; the standard library has PBKDF2.
const (
	hashIterations = 600_000
	saltBytes      = 16
	keyBytes       = 32
	minPassword    = 8 // characters
)

// hashPassword is the stored form of password:
// pbkdf2-sha256$<iterations>$<salt>$<key>, salt and key base64 without
// padding. A password shorter than minPassword is BAD_USER_INPUT, naming the
// argument.
func hashPassword(ctx context.Context, password string) (string, error) {
	if utf8.RuneCountInString(password) < minPassword {
		return "", codedError(ctx, fmt.Sprintf("password: at least %d characters", minPassword), veloxgql.CodeBadUserInput)
	}
	salt := make([]byte, saltBytes)
	_, _ = rand.Read(salt) // crypto/rand.Read never fails
	key, err := pbkdf2.Key(sha256.New, password, salt, hashIterations, keyBytes)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", hashIterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}
