# Security Policy

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| latest  | Yes                |

## Reporting a Vulnerability

If you discover a security vulnerability in Velox, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, report privately via [**GitHub Security Advisories**](https://github.com/syssam/velox/security/advisories/new). This is GitHub's built-in private disclosure channel — maintainers receive the report without it being visible to the public, and the fix can be coordinated in a draft advisory before publishing.

When filing, please include:

1. A description of the vulnerability
2. Steps to reproduce the issue
3. The potential impact of the vulnerability
4. Any suggested fixes (optional)

### What to Expect

- **Acknowledgment**: We will acknowledge receipt of your report within 48 hours.
- **Assessment**: We will investigate and assess the severity within 5 business days.
- **Resolution**: We aim to release a fix within 30 days of confirmed vulnerabilities, depending on complexity.
- **Disclosure**: We will coordinate with you on public disclosure timing after the fix is released.

### Scope

The following are in scope for security reports:

- SQL injection vulnerabilities in generated code or the SQL builder (`dialect/sql/`)
- Authentication/authorization bypasses in the privacy layer (`privacy/`)
- Code generation flaws that produce insecure output
- Dependency vulnerabilities in direct dependencies

### Out of Scope

- Vulnerabilities in user-written schema definitions or application code
- Issues in development-only tools (e.g., `velox init` scaffolding)
- Denial-of-service attacks against the code generator itself

## Security Best Practices

When using Velox in production:

1. **Enable the privacy layer** (`gen.FeaturePrivacy`) for authorization
2. **Use parameterized queries** -- Velox's SQL builder uses parameterized queries by default; never bypass this with raw SQL
3. **Keep dependencies updated** -- run `go mod tidy` and review dependency updates regularly
4. **Review generated code** -- audit generated code after schema changes, especially for sensitive entities
5. **Avoid raw SQL functions with untrusted input** -- see the section below

### Raw SQL Functions (`Expr`, `ExprP`, `P`)

The SQL builder provides escape hatches for raw SQL expressions:

```go
sql.Expr("age > ?", 18)             // SAFE: parameterized
sql.ExprP("name = ?", userInput)     // SAFE: parameterized
sql.Expr("age > " + userInput)       // UNSAFE: SQL injection
sql.P(func(b *sql.Builder) { ... })  // SAFE if using b.Arg() for values
```

These functions are necessary for advanced queries (e.g., database-specific functions, complex expressions) but bypass the builder's type-safe API. Rules:

- **Always use `?` placeholders** and pass values via the `args` parameter
- **Never concatenate** user input into the expression string
- **Never pass user input** as table names, column names, or other identifiers
- The `gosec` linter is enabled in CI to catch common SQL injection patterns

Generated code from Velox schemas always uses parameterized queries and does not use these raw functions.
