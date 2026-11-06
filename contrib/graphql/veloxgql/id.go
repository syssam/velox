package veloxgql

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Noder is the Go type of the GraphQL Node interface: gqlgen binds Node to
// it, and every entity that is a Node implements it in velox-gen's output.
type Noder interface {
	// IsNode marks the type as a Node.
	IsNode()
	// GlobalID is the node's ID, EncodeID of its GraphQL type and its key.
	GlobalID() string
}

// EncodeID returns the global ID of the row of the GraphQL type typ whose
// primary key is key: base64url without padding of "<typ>:<key>", the key in
// its canonical text.
//
// The key is a Go integer (decimal), a string (as it is), a UUID (a [16]byte,
// the UUID scalar, or a type with MarshalText such as uuid.UUID: the
// lower-case hyphenated form), or another type with MarshalText (its text). A
// UUID and its string form give the same ID. Any other key type is a mistake
// in the generated code, and EncodeID panics.
func EncodeID(typ string, key any) string {
	k, ok := keyText(key)
	if !ok {
		panic(fmt.Sprintf("veloxgql: EncodeID: %T is not a supported key type", key))
	}
	return base64.RawURLEncoding.EncodeToString([]byte(typ + ":" + k))
}

// DecodeID returns the key of id, which must be an ID of the GraphQL type
// typ whose key is of type K. It fails with ErrWrongIDType when id is a
// well-formed ID of another type, and with ErrBadID when id is not exactly
// what EncodeID writes for some key: canonical base64url without padding (no
// line breaks, no stray trailing bits) of "<Type>:<key>", the key the
// canonical text of a K. So each row has one ID. Neither error's text holds
// the key.
//
// K is a Go integer, a string, [16]byte, UUID, or a type with MarshalText
// whose pointer has UnmarshalText, such as uuid.UUID. A named type without
// those methods, such as type UserID int64 or a [16]byte UUID type without
// text methods, is not supported: generated code decodes the underlying type
// and converts it.
func DecodeID[K any](id, typ string) (K, error) {
	var zero K
	b, ok := decodeBase64(id)
	if !ok {
		return zero, badID(typ, "not canonical base64url without padding")
	}
	t, key, ok := strings.Cut(string(b), ":")
	switch {
	case !ok:
		return zero, badID(typ, "no type")
	case t == "":
		return zero, badID(typ, "an empty type")
	case t != typ:
		return zero, gqlcore.InputErrorf("%s ID: %w", typ, ErrWrongIDType)
	}
	var k K
	switch err := gqlcore.SetValue(basePtr(&k), gqlcore.Text(key)); {
	case errors.Is(err, gqlcore.ErrUnsupported):
		return zero, fmt.Errorf("veloxgql: DecodeID: %s is not a supported key type", gqlcore.TypeName[K]())
	case err != nil:
		return zero, badID(typ, "the key does not parse")
	}
	canon, ok := keyText(k)
	switch {
	case !ok:
		return zero, fmt.Errorf("veloxgql: DecodeID: %s is not a supported key type", gqlcore.TypeName[K]())
	case canon != key:
		return zero, badID(typ, "the key is not in its canonical form")
	}
	return k, nil
}

// IDType returns the GraphQL type of id, so that generated code can tell
// which entity to look it up in before DecodeID decodes its key. It fails
// with ErrBadID when id is not canonical base64url without padding of
// "<Type>:<key>" with a non-empty Type. It does not check the key, which is
// DecodeID's to check, and its error holds neither the type nor the key.
func IDType(id string) (string, error) {
	b, ok := decodeBase64(id)
	if !ok {
		return "", gqlcore.InputErrorf("ID: %w: not canonical base64url without padding", ErrBadID)
	}
	t, _, ok := strings.Cut(string(b), ":")
	if !ok || t == "" {
		return "", gqlcore.InputErrorf("ID: %w: no type", ErrBadID)
	}
	return t, nil
}

func badID(typ, why string) error {
	return gqlcore.InputErrorf("%s ID: %w: %s", typ, ErrBadID, why)
}

// keyText is gqlcore.KeyText of key, a UUID unwrapped to its [16]byte: the one
// scalar of this package that is a key type.
func keyText(key any) (string, bool) {
	if u, ok := key.(UUID); ok {
		key = [16]byte(u)
	}
	return gqlcore.KeyText(key)
}
