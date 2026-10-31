# Security policy

## Reporting a vulnerability

Report a vulnerability through GitHub's private vulnerability reporting on this
repository. Open the repository's Security tab, choose "Report a vulnerability",
and describe the problem there. Do not open a public issue, a pull request or a
discussion for it.

A report helps most when it has:

- the version or commit, and the database and its version;
- a schema or a small program that shows the problem;
- what you expected and what happened.

Please give us time to fix the problem before you make it public.

## Supported versions

Before 1.0, the supported version is `main`. After 1.0, it is the latest minor
release.

## Scope

A vulnerability is a flaw in velox that lets one party read or change what
another may not, or run what it may not. Examples are SQL injection through
a generated query or `migrate`'s rendered SQL, a tenant scope or a policy that
a query, a write or a load does not apply, and a secret in an error message or
a log line.

How an application builds its own raw SQL, hooks and policies is the
application's to secure.
