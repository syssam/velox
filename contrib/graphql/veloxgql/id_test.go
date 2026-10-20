package veloxgql_test

import (
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// raw is the ID of the text s, written by hand, to build malformed IDs.
func raw(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// roundTrip encodes key as an ID of User, checks the ID is the base64url of
// want, and decodes it back as K.
func roundTrip[K comparable](t *testing.T, key K, want string) {
	t.Helper()
	id := veloxgql.EncodeID("User", key)
	if id != raw(want) {
		t.Errorf("EncodeID(%v) = %q (%q), want %q", key, id, decoded(id), want)
	}
	if strings.ContainsAny(id, "=+/") {
		t.Errorf("EncodeID(%v) = %q, want base64url without padding", key, id)
	}
	got, err := veloxgql.DecodeID[K](id, "User")
	if err != nil || got != key {
		t.Errorf("DecodeID(%q) = %v, %v, want %v", want, got, err, key)
	}
}

func decoded(id string) string {
	b, _ := base64.RawURLEncoding.DecodeString(id)
	return string(b)
}

func TestIDRoundTrip(t *testing.T) {
	roundTrip(t, int64(1), "User:1")
	roundTrip(t, int64(-42), "User:-42")
	roundTrip(t, int64(math.MaxInt64), "User:9223372036854775807")
	roundTrip(t, int64(math.MinInt64), "User:-9223372036854775808")
	roundTrip(t, 7, "User:7")
	roundTrip(t, int8(-8), "User:-8")
	roundTrip(t, int16(300), "User:300")
	roundTrip(t, int32(70000), "User:70000")
	roundTrip(t, uint(9), "User:9")
	roundTrip(t, uint8(255), "User:255")
	roundTrip(t, uint16(65535), "User:65535")
	roundTrip(t, uint32(math.MaxUint32), "User:4294967295")
	roundTrip(t, uint64(math.MaxUint64), "User:18446744073709551615")
	roundTrip(t, "ann", "User:ann")
	roundTrip(t, "a:b/c ü", "User:a:b/c ü") // the type ends at the first colon
	roundTrip(t, "", "User:")
	roundTrip(t, "1:2", "User:1:2") // a string key may hold a colon; the type cannot

	const text = "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	u := uuid.MustParse(text)
	roundTrip(t, u, "User:"+text)                // a UUID type with MarshalText, as velox fields use
	roundTrip(t, [16]byte(u), "User:"+text)      // a bare 16-byte array
	roundTrip(t, veloxgql.UUID(u), "User:"+text) // the scalar
	if a, b := veloxgql.EncodeID("User", u), veloxgql.EncodeID("User", text); a != b {
		t.Errorf("a UUID and its string form encode differently: %q, %q", a, b)
	}
	if got, err := veloxgql.DecodeID[string](veloxgql.EncodeID("User", u), "User"); err != nil || got != text {
		t.Errorf("a UUID ID decoded as a string = %q, %v, want %q", got, err, text)
	}
}

func TestIDWrongType(t *testing.T) {
	for _, key := range []any{int64(1), "secret-key"} {
		id := veloxgql.EncodeID("Post", key)
		_, err := veloxgql.DecodeID[int64](id, "User")
		if !errors.Is(err, veloxgql.ErrWrongIDType) {
			t.Fatalf("a Post ID decoded as User: %v, want ErrWrongIDType", err)
		}
		if errors.Is(err, veloxgql.ErrBadID) {
			t.Errorf("a wrong-type ID is also ErrBadID: %v", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, "User") {
			t.Errorf("%q does not name the expected type", msg)
		}
		if strings.Contains(msg, "1") || strings.Contains(msg, "secret") || strings.Contains(msg, "Post") {
			t.Errorf("%q echoes the decoded ID", msg)
		}
	}
	// The type is checked before the key: a Post key that is no User key is
	// still the wrong type.
	if _, err := veloxgql.DecodeID[int64](raw("Post:abc"), "User"); !errors.Is(err, veloxgql.ErrWrongIDType) {
		t.Errorf("Post:abc as User: %v, want ErrWrongIDType", err)
	}
	// The type is matched exactly.
	for _, s := range []string{"user:1", "Users:1", "Use:1", " User:1"} {
		if _, err := veloxgql.DecodeID[int64](raw(s), "User"); !errors.Is(err, veloxgql.ErrWrongIDType) {
			t.Errorf("%q as User: %v, want ErrWrongIDType", s, err)
		}
	}
}

func TestIDMalformed(t *testing.T) {
	// The variants below decode to "User:12" unless the decoder is strict.
	for _, id := range []string{trailingBits(raw("User:12")), raw("User:12")[:3] + "\n" + raw("User:12")[3:]} {
		if b, err := base64.RawURLEncoding.DecodeString(id); err != nil || string(b) != "User:12" {
			t.Fatalf("%q does not test what it should: %q, %v", id, b, err)
		}
	}
	std := base64.StdEncoding.EncodeToString([]byte("User:12")) // padded
	cases := []struct {
		name, id string
		decode   func(string) error
	}{
		{"empty", "", decodeAs[int64]},
		{"bad base64", "!!!", decodeAs[int64]},
		{"padded", std, decodeAs[int64]},
		{"standard alphabet", base64.RawStdEncoding.EncodeToString([]byte("User:?>")), decodeAs[string]},
		{"no colon", raw("User1"), decodeAs[int64]},
		{"empty type", raw(":1"), decodeAs[int64]},
		{"trailing bits", trailingBits(raw("User:12")), decodeAs[int64]},
		{"newline", raw("User:12")[:3] + "\n" + raw("User:12")[3:], decodeAs[int64]},
		{"carriage return", raw("User:12")[:3] + "\r" + raw("User:12")[3:], decodeAs[int64]},
		{"trailing newline", raw("User:12") + "\n", decodeAs[int64]},
		{"key not an integer", raw("User:abc"), decodeAs[int64]},
		{"two colons as an integer", raw("User:1:2"), decodeAs[int64]},
		{"empty integer key", raw("User:"), decodeAs[int64]},
		{"integer with a plus", raw("User:+1"), decodeAs[int64]},
		{"integer with a leading zero", raw("User:01"), decodeAs[int64]},
		{"integer with spaces", raw("User: 1"), decodeAs[int64]},
		{"integer overflow", raw("User:300"), decodeAs[int8]},
		{"negative unsigned", raw("User:-1"), decodeAs[uint64]},
		{"fractional", raw("User:1.0"), decodeAs[int64]},
		{"not a UUID", raw("User:not-a-uuid"), decodeAs[uuid.UUID]},
		{"UUID upper case", raw("User:0193A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B"), decodeAs[uuid.UUID]},
		{"UUID without hyphens", raw("User:0193a1b2c3d47e5f8a9b0c1d2e3f4a5b"), decodeAs[uuid.UUID]},
		{"UUID in braces", raw("User:{0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b}"), decodeAs[uuid.UUID]},
		{"bare UUID array", raw("User:0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5"), decodeAs[[16]byte]},
		{"bare UUID upper case", raw("User:0193A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B"), decodeAs[[16]byte]},
		{"scalar UUID misplaced hyphen", raw("User:0193a1b2c-3d4-7e5f-8a9b-0c1d2e3f4a5b"), decodeAs[veloxgql.UUID]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.decode(c.id)
			if !errors.Is(err, veloxgql.ErrBadID) {
				t.Fatalf("got %v, want ErrBadID", err)
			}
			if errors.Is(err, veloxgql.ErrWrongIDType) {
				t.Errorf("a malformed ID is also ErrWrongIDType: %v", err)
			}
			if !strings.Contains(err.Error(), "User") {
				t.Errorf("%q does not name the expected type", err)
			}
		})
	}
}

// trailingBits is id with other unused bits in its last character, which
// a lenient decoder decodes to the same bytes.
func trailingBits(id string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, id[len(id)-1])
	return id[:len(id)-1] + string(alphabet[i^1])
}

func decodeAs[K any](id string) error {
	_, err := veloxgql.DecodeID[K](id, "User")
	return err
}

// A key type the IDs do not support is the caller's mistake, not the
// client's: neither sentinel.
func TestIDUnsupportedKey(t *testing.T) {
	_, err := veloxgql.DecodeID[struct{}](raw("User:1"), "User")
	if err == nil || errors.Is(err, veloxgql.ErrBadID) || errors.Is(err, veloxgql.ErrWrongIDType) {
		t.Errorf("DecodeID[struct{}] = %v, want an unsupported-type error", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("EncodeID of a struct key did not panic")
		}
	}()
	veloxgql.EncodeID("User", struct{}{})
}

// TestIDType pins IDType: the type of a well-formed ID, whatever its key,
// and ErrBadID with no type for anything else. Node reads it to pick the
// entity, then DecodeID checks the key.
func TestIDType(t *testing.T) {
	for id, want := range map[string]string{
		veloxgql.EncodeID("User", int64(7)): "User",
		veloxgql.EncodeID("Post", "a:b"):    "Post",
		raw("Tag:"):                         "Tag",
		raw("Tag:not a number"):             "Tag", // the key is DecodeID's to check
	} {
		if got, err := veloxgql.IDType(id); err != nil || got != want {
			t.Errorf("IDType(%q) = %q, %v, want %q", decoded(id), got, err, want)
		}
	}
	for _, id := range []string{"", "!!", raw("User"), raw(":7"), raw("User:7") + "=", raw("User:7") + "\n"} {
		got, err := veloxgql.IDType(id)
		if !errors.Is(err, veloxgql.ErrBadID) || got != "" {
			t.Errorf("IDType(%q) = %q, %v, want ErrBadID", id, got, err)
		}
		if err != nil && strings.Contains(err.Error(), "User") {
			t.Errorf("IDType(%q): %v holds the ID's text", id, err)
		}
	}
}
