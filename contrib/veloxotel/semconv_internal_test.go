package veloxotel

import (
	"slices"
	"testing"

	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/dbconv"

	"github.com/syssam/velox2"
)

// TestNamesMatchSemconv: the names attrs.go writes out are those of the
// pinned semantic-conventions release, v1.43.0. The package imports no
// semconv package; this test does, so a bump is a compile-checked change.
func TestNamesMatchSemconv(t *testing.T) {
	if schemaURL != semconv.SchemaURL {
		t.Errorf("schemaURL %q, want %q", schemaURL, semconv.SchemaURL)
	}
	for _, c := range []struct {
		got  string
		want string
	}{
		{attrDBSystem, string(semconv.DBSystemNameKey)},
		{attrDBOperation, string(semconv.DBOperationNameKey)},
		{attrDBCollection, string(semconv.DBCollectionNameKey)},
		{attrDBQueryText, string(semconv.DBQueryTextKey)},
		{attrDBQuerySummary, string(semconv.DBQuerySummaryKey)},
		{attrReturnedRows, string(semconv.DBResponseReturnedRowsKey)},
		{attrCodeFile, string(semconv.CodeFilePathKey)},
		{attrCodeLine, string(semconv.CodeLineNumberKey)},
		{attrCodeFunction, string(semconv.CodeFunctionNameKey)},
		{attrErrorType, string(semconv.ErrorTypeKey)},
		{errorTypeOther, semconv.ErrorTypeOther.Value.AsString()},

		{dbSystem(velox.Postgres), semconv.DBSystemNamePostgreSQL.Value.AsString()},
		{dbSystem(velox.MySQL), semconv.DBSystemNameMySQL.Value.AsString()},
		{dbSystem(velox.SQLite), semconv.DBSystemNameSQLite.Value.AsString()},
		{dbSystem(velox.Dialect(255)), semconv.DBSystemNameOtherSQL.Value.AsString()},

		{metricDuration, dbconv.ClientOperationDuration{}.Name()},
		{unitDuration, dbconv.ClientOperationDuration{}.Unit()},
		{descDuration, dbconv.ClientOperationDuration{}.Description()},
		{metricReturnedRows, dbconv.ClientResponseReturnedRows{}.Name()},
		{unitReturnedRows, dbconv.ClientResponseReturnedRows{}.Unit()},
		{descReturnedRows, dbconv.ClientResponseReturnedRows{}.Description()},
	} {
		if c.got != c.want {
			t.Errorf("%q, want %q", c.got, c.want)
		}
	}
}

// TestRowBounds: the rows histogram's boundaries are the ones v1.43.0
// advises for db.client.response.returned_rows.
func TestRowBounds(t *testing.T) {
	want := []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000}
	if !slices.Equal(rowBounds, want) {
		t.Errorf("rowBounds %v, want %v", rowBounds, want)
	}
}
