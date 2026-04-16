//go:build ignore

package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"

	"example.com/fullgql/gqlgen"
	"example.com/fullgql/velox"

	"github.com/syssam/velox/dialect"
	_ "modernc.org/sqlite"
)

func main() {
	// Open SQLite database.
	client, err := velox.Open(dialect.SQLite, "file:fullgql.db?_pragma=foreign_keys(1)")
	if err != nil {
		slog.Error("opening database", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	// Run schema migration.
	if err := client.Schema.Create(context.Background()); err != nil {
		slog.Error("creating schema", "error", err)
		os.Exit(1)
	}

	// Create GraphQL handler.
	srv := handler.NewDefaultServer(gqlgen.NewExecutableSchema(gqlgen.Config{
		Resolvers: &gqlgen.Resolver{Client: client},
	}))

	// Routes.
	http.Handle("/", playground.Handler("Velox GraphQL", "/query"))
	http.Handle("/query", srv)

	port := "8080"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}
	slog.Info("server started", "url", "http://localhost:"+port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
