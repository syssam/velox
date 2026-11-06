# 42. An entity's generated file is <entity>.go

Status: accepted, 2026-11-06.

## Context

velox-gen wrote each entity to `<label>_velox.go`, and velox-graphql writes
`<label>_gql.go`. The suffix kept an entity's name from making its file a
test or tying it to one system: go build reads `ab_test.go` as a test,
`server_linux.go` as Linux's and `device_arm64.go` as arm64's. ent names
the file `user.go`, and gen read as velox-gen's suffix on every file.

go build decides by the end of a name, cut at its first dot. A one-word
name, `linux.go` or `test.go`, builds everywhere. Only a name of two or
more words whose last is `test`, a GOOS or a GOARCH is left out. A prefix,
`velox_ab_test.go`, or a dot, `ab_test.velox.go`, is still left out.

## Decision

- An entity's file, a join entity's too, is `<label>.go`: `post.go`,
  `post_tag.go`.
- When go build would leave `<label>.go` out of a build, the file is
  `<label>_velox.go`: `ab_test_velox.go`, `server_linux_velox.go`. So is an
  entity labelled `velox`, whose `velox.go` is the package's own file.
  `codegen.GoFileSystems`, which decides the plugins' and the user's files,
  decides this name too.
- velox-graphql's files keep `_gql.go`, so gen tells ORM files from
  GraphQL ones by name.
- velox-gen removes the `_velox.go` files an older velox-gen wrote, as it
  removes any generated file it no longer writes: it knows them by their
  header.
- `GeneratedCodeIsVersion18` stays. Each file's contents are unchanged, and
  code generated before still compiles against this runtime.

## Consequences

- gen lists `post.go`, `post_gql.go`, `user.go`, `velox.go`, as ent's does.
- A hand-written `gen/user.go` now collides with the generated one. velox-gen
  refuses to overwrite a file without its header and names it, so the user
  renames theirs, to `user_methods.go` say.
- A plugin's `post.go` collides with velox-gen's, and velox-gen refuses it,
  as it refused `post_velox.go` before.
- Measured: every file in internal/testmodel, internal/testbind and the
  examples moved with its contents unchanged (54 renames, 100% similar).
