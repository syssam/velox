# tracker

An issue tracker's JSON API: the example to read for how the pieces of velox fit
together in a real service. The others show one topic each.

| Piece | Where |
|---|---|
| Entities, relations, a many-to-many, a cascading foreign key | `schema/schema.go` |
| Tenants: each organization sees only its own rows | `field.Int64("org_id").Tenant()`; `authed` in `auth.go` names the tenant once per request |
| Who may change what, enforced in the statements themselves for projects, issues and comments | `gen/policy.go`, `gen/principal.go`; `User` has no policy, as signing up inserts one before anyone is logged in, so `invite` checks the role itself |
| Logins: salted password hashes, tokens stored only as hashes | `auth.go` |
| Transactions | `signup` in `auth.go`, `createIssue` in `server.go` |
| A label upsert that is safe under concurrency, its names sorted so that transactions cannot deadlock | `setLabels` in `server.go` |
| Eager loading, nested, with no N+1 | `getIssue` and `listIssues` in `server.go` |
| A count per row in one statement | `CountEach` in `listProjects` |
| Keyset paging | `page` and `cut` in `server.go` |
| Mapping velox's errors to HTTP statuses | `fail` in `server.go` |
| Deadlines on statements and transactions, and a shutdown that lets requests finish | `dbLimits` and `serveUntil` in `main.go` |
| Migrations per dialect, applied at startup | `migrations/sqlite/`, `migrations/postgres/`, `open` in `main.go` |
| SQLite through database/sql, PostgreSQL through pgx's native protocol | `open` and `openPostgres` in `main.go` |

## Run it

```sh
cd examples
go run ./tracker                                   # a temporary SQLite file
TRACKER_DB=tracker.db go run ./tracker             # a SQLite file that stays
TRACKER_DB='postgres://…' go run ./tracker         # PostgreSQL
```

```sh
api=http://localhost:8080
token=$(curl -s $api/signup -d '{"org":"acme","org_name":"Acme","email":"ann@acme.test","name":"Ann","password":"correct horse"}' | jq -r .token)
auth="Authorization: Bearer $token"

curl -s $api/projects -H "$auth" -d '{"name":"Rocket"}'
curl -s $api/projects/1/issues -H "$auth" -d '{"title":"Fuel leak","labels":["urgent","bug"]}'
curl -s $api/issues/1/comments -H "$auth" -d '{"body":"On it"}'
curl -s "$api/projects/1/issues?status=open&label=bug&limit=20" -H "$auth"
curl -s $api/issues/1 -H "$auth"
curl -s -X PATCH $api/issues/1 -H "$auth" -d '{"status":"closed"}'
curl -s $api/projects -H "$auth"
```

## The API

| Route | Who | |
|---|---|---|
| `POST /signup` | anyone | a new organization, its first user an admin; returns a token |
| `POST /login` | anyone | returns a token |
| `POST /logout` | a user | ends this token's session |
| `GET /me` | a user | |
| `POST /users` | an admin | adds a member |
| `GET /projects` | a user | a page, with each project's count of open issues |
| `POST /projects` | an admin | |
| `GET /projects/{id}/issues` | a user | a page; `?status=`, `?label=`, `?after=`, `?limit=` |
| `POST /projects/{id}/issues` | a user | with labels, created as needed |
| `GET /issues/{id}` | a user | with author, assignee, labels and comments |
| `PATCH /issues/{id}` | an admin, the author, the assignee | title, body, status, assignee_id |
| `DELETE /issues/{id}` | an admin, the author, the assignee | its comments and label links go with it |
| `POST /issues/{id}/comments` | a user | |

A page is `{"items": [...], "next": 42}`; pass `next` as `?after=` for the next one,
until it is null.

## What to notice

- **Isolation is not the handlers' job.** No handler filters by organization.
  `authed` puts the tenant in the context, and every statement on a tenant-scoped
  entity is scoped by it, or fails without one. Another organization's issue is a
  404, not a 403: it does not exist for the caller.
- **A foreign key does not know about tenants.** `updateIssue` and `addComment`
  read the row they are about to refer to, in the caller's tenant, first; so does
  `createIssue` for its project. Without that, a request could point at another
  organization's row by guessing its id.
- **The policy is part of the statement.** `PATCH` by someone who may not change the
  issue changes no row; `missingOrDenied` then tells 404 from 403.
- **`gen.Issue` is the response.** Its JSON tags come from the schema, and
  `password_hash` is `Sensitive`, so it cannot be written out. JSON goes one way: an
  entity with loaded relations does not decode, so a client needs its own structs,
  as `main_test.go` has.

## What it leaves out

It has no rate limiting, which a service needs on `/login` and `/signup` above all:
each hashes a password, which is slow on purpose. It has no email verification, no
password reset and no session cleanup, and comments are not paged, so an issue with
very many of them is costly to read. None of these is about velox. Expired sessions
stop working, but their rows stay until something deletes them.
