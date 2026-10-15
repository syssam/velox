module github.com/syssam/velox2/cmd/velox-gen

go 1.27.0

require (
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
)

require (
	github.com/syssam/velox2 v0.0.0-00010101000000-000000000000
	golang.org/x/tools v0.52.0
)

replace github.com/syssam/velox2 => ../..
