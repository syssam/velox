module github.com/syssam/velox/contrib/graphqlgo

go 1.27

// Isolated module: graphql-go and its Go 1.27 floor stay out of the root
// velox module, so gqlgen users neither download it nor need that Go.
replace github.com/syssam/velox => ../..

require (
	github.com/99designs/gqlgen v0.17.86
	github.com/syssam/graphql-go v0.0.0-20260926154251-135bb6be891a
	github.com/syssam/velox v0.0.0-00010101000000-000000000000
	github.com/vektah/gqlparser/v2 v2.5.58
)

require (
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
