package gqlrelay

import (
	"encoding/base64"
	"fmt"
	"io"
	"strconv"

	"github.com/99designs/gqlgen/graphql"
)

// MarshalBytes and UnmarshalBytes implement the GraphQL Bytes scalar that
// field.Bytes fields generate (`scalar Bytes @goModel(model:
// ".../gqlrelay.Bytes")`): a []byte travels as a standard base64 string.
// Without them gqlgen bound the scalar to string and generated panicking
// resolver stubs, so a Bytes field could be neither read nor written.

// MarshalBytes writes b as a base64 string; nil is null.
func MarshalBytes(b []byte) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		if b == nil {
			_, _ = io.WriteString(w, "null")
			return
		}
		_, _ = io.WriteString(w, strconv.Quote(base64.StdEncoding.EncodeToString(b)))
	})
}

// UnmarshalBytes decodes a base64 string.
func UnmarshalBytes(v any) ([]byte, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case string:
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("gqlrelay: Bytes must be a base64 string: %w", err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("gqlrelay: Bytes must be a base64 string, got %T", v)
	}
}
