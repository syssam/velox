// Package otelvelox is a verified, runnable reference for tracing velox with
// OpenTelemetry.
//
// velox ships no bespoke tracing driver — by design it matches Ent and lets you
// instrument at the database/sql layer, one level below the ORM. This package
// contains no library code. It exists to:
//
//  1. prove the wiring documented in docs/observability.md compiles against a
//     real github.com/XSAM/otelsql release, and
//  2. demonstrate end to end (see otelvelox_test.go) that a span is emitted for
//     every statement velox runs through sql.OpenDB.
//
// It is its own Go module so the OpenTelemetry/otelsql dependencies stay out of
// the root velox module.
package otelvelox
