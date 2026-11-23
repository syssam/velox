# 146. A kept filter decodes with veloxgql.DecodeWhere

- Status: accepted, 2026-11-23.

## Context

nextapp keeps access-policy and data-filter rules as JSON documents of a
where input: `{"ownerId": "123"}`, `{"not": {"idIn": ["999"]}}`,
`{"addressCity": "Elsewhere"}`. veloxe generated an `UnmarshalGQL` on
every where input, and nextapp read a rule with it, then lowered it.
A rule that cannot be read must deny everything, never filter nothing.

velox-graphql writes no such method. The generated GraphQL code was cut
down on purpose: a method per input costs code in each of nextapp's 496
entities. Under gqlgen an input carries no struct tags, and its Go names
are not its GraphQL names (`AuthorID` is `authorID`, `City` may be
`addressCity`). Only the input's `gqlgraph.Type` knows the names, in
`WhereFields`, and that value is unexported in the generated package.

## Decision

- `veloxgql.DecodeWhere(v any, dst any) error` reads `v` into `dst`, a
  pointer to a generated where input. `v` is a decoded JSON object
  (`map[string]any`), or JSON text (`[]byte`, `json.RawMessage`, numbers
  kept whole). No method is generated.
- Names come from the input's own `Predicate`. Called on a nil input,
  with a context that asks for the names (`gqlcore.AskWhereNames`),
  `gqlgraph.Predicate` puts its `Type`'s names in the context's slot: the
  input's name, `PostWhereInput`, and each struct field's GraphQL name,
  `WhereField.Name` plus the predicate's suffix (`titleNEQ`), in field
  order. These are the SDL's names, `graphql.Name` renames included.
  A nil input costs one `ctx.Value` more; no other call changes. The
  names are cached per `reflect.Type` in a `sync.Map`.
- Values follow the field's Go type, as veloxe's `gqlinput.DecodeWhere`
  did:
  - an integer from a number or a string of digits, so an ID arrives as a
    string; a float beyond 2^53 is refused;
  - a type with `UnmarshalGQL` by it (enums, by GraphQL name);
    `time.Time` by `veloxgql.Time`; `[]byte` by `veloxgql.Bytes`; a type
    with `UnmarshalText` (a UUID, a decimal) from a string or a number;
  - a single value where a list goes is a list of one, as GraphQL
    coerces a list;
  - a nested input (`not`, `and`, `or`, `has…With`) is read the same way,
    with its own type's names.
- `null` is a field not given. An unknown key, a `null` inside a list,
  and a value of the wrong type are refused with BAD_USER_INPUT, naming
  the path: `veloxgql: not.idIn[0]: expected a whole number, got a
  boolean`. On an error `dst` is unchanged.
- Limits are not applied here. `Pred(ctx)` applies them, as it does for a
  where argument.

## Rejected

- A generated `UnmarshalGQL` per input, even one line. Neither engine
  calls it. gqlgen calls a model's `UnmarshalGQL` only when `MarshalGQL`
  is beside it (gqlgen v0.17.86, `codegen/config/binder.go`), and then
  instead of its own decoding of the input's fields. graphql-go binds an
  input by its fields. Only the application would call it, and
  `DecodeWhere` serves that with no code per input.
- Names from struct tags. Only `engine=graphql-go` writes tags, and only
  where graphql-go's own derivation differs.
- Names from Go names compared without case, as gqlgen binds. That would
  accept spellings the SDL does not have. A stored rule must name what
  the SDL names.
- veloxe's reading of an enum by its stored value (`"draft"`). The
  generated `UnmarshalGQL` takes the GraphQL name only.

## Consequences

- nextapp replaces `w.UnmarshalGQL(doc)` with `veloxgql.DecodeWhere(doc,
  &w)`, then `w.Pred(ctx)`.
- No generated-code change: `GeneratedCodeIsVersion27` stays.
- A hand-written where input that does not call `gqlgraph.Predicate`
  is refused: "is not a where input velox-graphql generated".
