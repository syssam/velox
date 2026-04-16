# Real-World Example: Multi-Tenant Task Manager

A reference application demonstrating Velox ORM features:

- **Schema definitions** with fields, edges, enums, and mixins
- **Privacy policies** for role-based access control
- **HTTP handlers** with generated query builders
- **SQLite** for zero-setup development

## Run

```bash
cd examples/realworld
go run generate.go    # Generate ORM code
go run main.go        # Start server on :8080
```

## API

```bash
curl localhost:8080/tasks
curl localhost:8080/workspaces
```

## Key Patterns Demonstrated

- `privacy.DenyIfNoViewer()` -- Require authentication
- `privacy.HasRole("admin")` -- Role-based mutation guard
- `privacy.WithViewer(ctx, viewer)` -- Set auth context
- `client.Task.Query().Where(...).WithWorkspace().All(ctx)` -- Eager loading
- `task.StatusField.EQ("todo")` -- Type-safe predicates
