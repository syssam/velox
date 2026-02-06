// This file is the server entry point.
// Run after generating code:
//
//	go run generate.go
//	go run github.com/99designs/gqlgen generate
//	DATABASE_URL="postgres://..." go run .
//
// Or for SQLite:
//
//	go run .
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	"example.com/shop/graph"
	"example.com/shop/velox"
)

const defaultPort = "8080"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	// Database connection - supports PostgreSQL or SQLite
	ctx := context.Background()

	var client *velox.Client
	var err error

	dsn := os.Getenv("DATABASE_URL")
	if dsn != "" {
		// PostgreSQL
		client, err = velox.Open("postgres", dsn)
	} else {
		// Default to SQLite for easy local development
		client, err = velox.Open("sqlite", "file:shop.db?cache=shared&_pragma=foreign_keys(1)")
	}
	// Enable debug logging to see SQL queries
	client = client.Debug()
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()
	log.Println("Database connected successfully")

	// Run automatic migration to create/update schema
	if err := client.Schema.Create(ctx); err != nil {
		log.Fatalf("failed to create schema: %v", err)
	}
	log.Println("Database schema created/updated successfully")

	// Create GraphQL resolver
	resolver := graph.NewResolver(client)

	// Create GraphQL server
	srv := handler.NewDefaultServer(
		graph.NewExecutableSchema(graph.Config{
			Resolvers: resolver,
		}),
	)

	// Setup routes
	http.Handle("/", playground.Handler("Shop GraphQL", "/query"))
	http.Handle("/query", srv)

	log.Printf("Server running at http://localhost:%s/", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
