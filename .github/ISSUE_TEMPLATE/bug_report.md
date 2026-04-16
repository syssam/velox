---
name: Bug Report
about: Report a bug in Velox
title: ""
labels: bug
assignees: ""
---

## Describe the Bug

A clear and concise description of what the bug is.

## To Reproduce

Steps to reproduce the behavior:

1. Define schema with '...'
2. Run `velox generate ./schema`
3. See error

## Expected Behavior

A clear description of what you expected to happen.

## Actual Behavior

What actually happened instead.

## Schema Definition

```go
// Paste your relevant schema definition here
type Example struct{ velox.Schema }

func (Example) Fields() []velox.Field {
    return []velox.Field{
        // ...
    }
}
```

## Environment

- **Go version**: [e.g., 1.26]
- **Velox version**: [e.g., v0.1.0 or commit hash]
- **Database**: [e.g., PostgreSQL 16, MySQL 8, SQLite]
- **OS**: [e.g., macOS 15, Ubuntu 24.04]

## Generated Code (if applicable)

```go
// Paste relevant generated code snippet
```

## Error Output

```
Paste any error messages or stack traces here
```

## Additional Context

Add any other context about the problem here.
