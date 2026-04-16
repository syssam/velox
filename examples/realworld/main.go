// Package main demonstrates a multi-tenant task manager using Velox ORM.
//
// Before running, generate the ORM code:
//
//	go run generate.go
//	go run main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"

	"github.com/syssam/velox/privacy"
)

// This file shows the intended usage pattern. It will compile after
// running `go run generate.go` which produces the velox/ package.
//
// The generated code provides:
//   - velox.Open(driver, dsn) -> *Client
//   - client.User, client.Task, client.Workspace -> entity clients
//   - client.Schema.Create(ctx) -> auto-migration
//   - Type-safe predicates: task.StatusField.EQ("todo")
//   - Eager loading: Query().WithWorkspace().All(ctx)

func main() {
	// In production, replace with actual generated client:
	//
	//   client, err := velox.Open("sqlite", "file:app.db?_pragma=foreign_keys(1)")
	//   if err != nil {
	//       log.Fatal(err)
	//   }
	//   defer client.Close()
	//
	//   // Auto-migrate the schema
	//   if err := client.Schema.Create(ctx); err != nil {
	//       log.Fatal(err)
	//   }

	ctx := context.Background()

	// Demonstrate viewer context setup
	admin := &privacy.SimpleViewer{UserID: "admin-1", UserRoles: []string{"admin"}}
	adminCtx := privacy.WithViewer(ctx, admin)
	slog.Info("admin context created", "viewer", admin.ID(), "roles", admin.Roles())

	member := &privacy.SimpleViewer{UserID: "user-1", UserRoles: []string{"member"}}
	memberCtx := privacy.WithViewer(ctx, member)
	slog.Info("member context created", "viewer", member.ID(), "roles", member.Roles())

	// These contexts would be used with the generated client:
	//
	//   // Admin creates a workspace
	//   ws, err := client.Workspace.Create().SetName("Engineering").Save(adminCtx)
	//
	//   // Member creates tasks
	//   t1, err := client.Task.Create().
	//       SetTitle("Implement login").
	//       SetStatus("todo").
	//       SetPriority("high").
	//       SetWorkspaceID(ws.ID).
	//       Save(memberCtx)
	//
	//   // Query with eager loading
	//   tasks, err := client.Task.Query().
	//       Where(task.StatusField.EQ("todo")).
	//       WithWorkspace().
	//       All(memberCtx)

	_ = adminCtx
	_ = memberCtx

	// HTTP handler example
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// In production with generated code:
	//
	//   mux.HandleFunc("GET /tasks", func(w http.ResponseWriter, r *http.Request) {
	//       // Extract viewer from JWT/session in production
	//       viewer := &privacy.SimpleViewer{UserID: "user-1", UserRoles: []string{"member"}}
	//       ctx := privacy.WithViewer(r.Context(), viewer)
	//
	//       tasks, err := client.Task.Query().
	//           Where(task.StatusField.NEQ("done")).
	//           All(ctx)
	//       if err != nil {
	//           http.Error(w, err.Error(), http.StatusInternalServerError)
	//           return
	//       }
	//       json.NewEncoder(w).Encode(tasks)
	//   })

	fmt.Println("Real-world example server on :8080 (run `go run generate.go` first for full functionality)")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
