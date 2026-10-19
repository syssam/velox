module github.com/syssam/velox2/contrib/graphql

go 1.27.0

require (
	github.com/syssam/velox2 v0.0.0-00010101000000-000000000000
	github.com/vektah/gqlparser/v2 v2.5.59
)

require github.com/agnivade/levenshtein v1.2.1 // indirect

replace github.com/syssam/velox2 => ../..
