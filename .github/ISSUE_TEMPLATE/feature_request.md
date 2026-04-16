---
name: Feature Request
about: Suggest a new feature or enhancement for Velox
title: ""
labels: enhancement
assignees: ""
---

## Is your feature request related to a problem?

A clear and concise description of what the problem is. Example: "I'm always frustrated when [...]"

## Describe the Solution You'd Like

A clear description of what you want to happen.

## Proposed API

```go
// Show how the feature would be used in schema definitions or application code

// Schema-level example:
func (User) Fields() []velox.Field {
    return []velox.Field{
        // ...
    }
}

// Application-level example:
client.User.Query().NewFeature().All(ctx)
```

## Describe Alternatives You've Considered

A description of any alternative solutions or features you've considered.

## Affected Packages

Which packages would this feature affect? (Check all that apply)

- [ ] `velox.go` (core interfaces)
- [ ] `schema/` (field, edge, mixin, index builders)
- [ ] `compiler/gen/` (code generation)
- [ ] `compiler/gen/sql/` (SQL dialect generator)
- [ ] `contrib/graphql/` (GraphQL extension)
- [ ] `dialect/sql/` (SQL runtime)
- [ ] `privacy/` (authorization layer)
- [ ] `runtime/` (runtime helpers)
- [ ] `cmd/velox/` (CLI tool)

## Additional Context

Add any other context, screenshots, or references here.

### Related Issues

- #issue_number
