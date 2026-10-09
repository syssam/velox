# Keeping application packages fast to compile

On a large schema, most of a cold build is not spent compiling velox's
generated packages. It is spent compiling the application packages that *use*
them, and almost all of that cost comes from one type: the generated root
`Client` (and `Tx`).

This page explains the cost, how to measure it in your own project, and the
generated API that avoids it.

## The cost

The root `Client` has one typed field per entity:

```go
type Client struct {
    User  *userclient.UserClient
    Post  *postclient.PostClient
    // ... one per entity
}
```

A package that names `*Client` makes the compiler load every entity client and
every builder those clients reach. It does not matter whether the name appears
in an exported signature or only inside a function body, and it does not
matter how much of the client the package uses.

Measured on a 453-entity schema (Go 1.26, `GOMAXPROCS=4`), one package each:

| the package contains | CPU | peak memory |
|---|---|---|
| nothing from velox | 0.00 s | 19 MB |
| `*entity.User` in a signature | 0.35 s | 146 MB |
| `*userclient.UserClient` in a signature | 0.36 s | 144 MB |
| `*velox.Client` in a signature | 2.4 s | 1.0 GB |
| an opaque handle in the signature, `client.User.Query()` in the body | 1.8 s | 709 MB |
| `*velox.Tx` in a signature | — | 815 MB |
| `userclient.From(p).Query()` | 0.23 s | 98 MB |
| six entity clients built with `From` | 0.33 s | 125 MB |
| `velox.IsNotFound(err)`, `velox.TypeUser` | 0.10 s | 50 MB |

A real 371-line service from the same project went from 2.9 CPU-seconds and
870 MB to 0.8 s and 254 MB when it stopped naming the root client. Its call
sites did not change.

Two things follow from the table:

- **Functions and constants of the root package are cheap.** `IsNotFound`,
  `IsConstraintError`, the `Type*` names and the aggregate helpers do not load
  the client. Only the `Client` and `Tx` *types* do.
- **An opaque handle is not enough.** `ClientHandle`/`TxHandle` keep the client
  out of a package's *export data*, which is what makes the build cache large.
  They do not make the package compile faster if its body still reaches the
  client through `ClientOf(h).User`.

The peak-memory column matters as much as the CPU column: a build that needs
1 GB per compiler process cannot run many of them at once.

## The narrow path

Take a `runtime.TxRunner` (or a `runtime.ConfigProvider` if the package never
starts a transaction) and build the entity clients you use with `From`:

```go
package narrowsvc

import (
    "context"

    "github.com/syssam/velox/runtime"

    postclient "example.com/app/velox/client/post"
    userclient "example.com/app/velox/client/user"
    "example.com/app/velox/entity"
    "example.com/app/velox/user"
)

type Service struct {
    db    runtime.TxRunner
    users *userclient.UserClient
}

// New takes the generated root client: *velox.Client is a runtime.TxRunner.
func New(db runtime.TxRunner) *Service {
    return &Service{db: db, users: userclient.From(db)}
}

func (s *Service) UserByEmail(ctx context.Context, email string) (*entity.User, error) {
    return s.users.Query().Where(user.EmailField.EQ(email)).Only(ctx)
}
```

The caller passes the root client as before — `narrowsvc.New(client)` — because
`*Client` implements `runtime.TxRunner`.

### Transactions

`RunTx` commits when the function returns nil and rolls back when it returns
an error or panics. The transaction arrives as a `runtime.ConfigProvider`;
entity clients built from it run inside the transaction:

```go
func (s *Service) RenameWithPost(ctx context.Context, id int, name, title string) error {
    return s.db.RunTx(ctx, func(tx runtime.ConfigProvider) error {
        if err := userclient.From(tx).UpdateOneID(id).SetName(name).Exec(ctx); err != nil {
            return err
        }
        return postclient.From(tx).Create().SetTitle(title).SetAuthorID(id).Exec(ctx)
    })
}
```

`StartTx` is for code that ends the transaction itself. The caller must
`Commit` or `Rollback` the `runtime.Transaction` it returns:

```go
func (s *Service) Rename(ctx context.Context, id int, name string) error {
    tx, err := s.db.StartTx(ctx)
    if err != nil {
        return err
    }
    committed := false
    defer func() {
        if !committed {
            _ = tx.Rollback()
        }
    }()
    if err := userclient.From(tx).UpdateOneID(id).SetName(name).Exec(ctx); err != nil {
        return err
    }
    if err := tx.Commit(); err != nil {
        return err
    }
    committed = true
    return nil
}
```

Do not defer an unconditional `Rollback`: after a `Commit` it fails at the
driver, but rollback hooks registered with `OnRollback` run first.

To pass a transaction to another package, pass the `runtime.ConfigProvider` (or
`runtime.Transaction`), not a `*Tx`.

### What stays the same

- Hooks and interceptors registered on the root client (`client.Use`,
  `client.User.Use`, `client.Intercept`) apply to clients built with `From`:
  both read the same stores.
- The entity's privacy policy applies.
- `client.User.Query()` keeps working. Nothing has to migrate at once; a
  package is fast as soon as it no longer names `Client` or `Tx`.

### What to avoid

- Do not store or pass `*velox.Client` or `*velox.Tx` in a package you want
  fast, including in unexported fields and local variables.
- Do not wrap the root client in your own struct and pass that around: a
  struct that embeds `*velox.Client` costs the same as the client.
- Do not build an entity client in a package-level variable (`var users =
  userclient.From(db)`). The schema's defaults, hooks and policy are attached
  when the generated root package initialises, and Go may initialise your
  package first; the constructor panics rather than hand out a client with no
  policy. Build clients in a constructor or a function.
- `From` builds a client on each call. It is cheap, but in a hot path keep the
  client in a field, as `Service.users` above.

## Measuring your own project

Per-package compile time and peak memory, without changing any source:

```bash
cat > /tmp/tx.sh <<'EOF'
#!/bin/bash
tool="$1"; shift
if [[ "$(basename "$tool")" == compile && -n "$TOOLEXEC_IMPORTPATH" ]]; then
  t="$TXLOG.$$.$RANDOM"
  /usr/bin/time -l -o "$t" "$tool" "$@"; rc=$?
  awk -v p="$TOOLEXEC_IMPORTPATH" '/ real /{r=$1;u=$3;s=$5} /maximum resident/{m=$1} END{print p, r, u, s, m}' "$t" >> "$TXLOG"
  rm -f "$t"; exit $rc
fi
exec "$tool" "$@"
EOF
chmod +x /tmp/tx.sh
TXLOG=/tmp/build.log GOCACHE=/tmp/cold-cache go build -toolexec /tmp/tx.sh ./cmd/server
sort -k3 -n -r /tmp/build.log | head -30     # most CPU first: path real user sys maxrss
```

(`/usr/bin/time -l` is the macOS spelling; on Linux use `-v` and read
`Maximum resident set size`.) Use a separate `GOCACHE` so the measurement is
cold and your normal cache is untouched.

Application packages that take two or more CPU-seconds and close to a
gigabyte for a few hundred lines are the ones naming the root client.

`tests/integration/narrowsvc` in the velox repository is the pattern above as
compiled, tested code, and `TestNarrowService_DoesNotImportRoot` shows how to
pin that a package stays independent of the root package.
