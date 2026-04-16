package schema

import (
	"fmt"
	"strings"
)

// ValidationError represents a schema validation error.
type ValidationError struct {
	Table   string
	Column  string
	Message string
	// Breaking indicates if this is a breaking change.
	Breaking bool
}

func (e *ValidationError) Error() string {
	if e.Column != "" {
		return fmt.Sprintf("%s.%s: %s", e.Table, e.Column, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Table, e.Message)
}

// ValidationResult holds the results of schema validation.
type ValidationResult struct {
	Errors   []*ValidationError
	Warnings []*ValidationError
}

// HasErrors returns true if there are any validation errors.
func (r *ValidationResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// HasWarnings returns true if there are any validation warnings.
func (r *ValidationResult) HasWarnings() bool {
	return len(r.Warnings) > 0
}

// HasBreakingChanges returns true if there are any breaking changes.
func (r *ValidationResult) HasBreakingChanges() bool {
	for _, e := range r.Errors {
		if e.Breaking {
			return true
		}
	}
	for _, w := range r.Warnings {
		if w.Breaking {
			return true
		}
	}
	return false
}

// String returns a human-readable summary of the validation result.
func (r *ValidationResult) String() string {
	var sb strings.Builder
	if len(r.Errors) > 0 {
		sb.WriteString("Errors:\n")
		for _, e := range r.Errors {
			sb.WriteString("  - ")
			sb.WriteString(e.Error())
			if e.Breaking {
				sb.WriteString(" [BREAKING]")
			}
			sb.WriteString("\n")
		}
	}
	if len(r.Warnings) > 0 {
		sb.WriteString("Warnings:\n")
		for _, w := range r.Warnings {
			sb.WriteString("  - ")
			sb.WriteString(w.Error())
			if w.Breaking {
				sb.WriteString(" [BREAKING]")
			}
			sb.WriteString("\n")
		}
	}
	if !r.HasErrors() && !r.HasWarnings() {
		sb.WriteString("No issues found")
	}
	return sb.String()
}

// ValidateOption configures schema validation.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	allowDropColumn    bool
	allowDropTable     bool
	allowDropIndex     bool
	allowNullToNotNull bool
}

// AllowDropColumn allows dropping columns without error.
func AllowDropColumn() ValidateOption {
	return func(c *validateConfig) {
		c.allowDropColumn = true
	}
}

// AllowDropTable allows dropping tables without error.
func AllowDropTable() ValidateOption {
	return func(c *validateConfig) {
		c.allowDropTable = true
	}
}

// AllowDropIndex allows dropping indexes without error.
func AllowDropIndex() ValidateOption {
	return func(c *validateConfig) {
		c.allowDropIndex = true
	}
}

// AllowNullToNotNull allows changing nullable columns to not null.
func AllowNullToNotNull() ValidateOption {
	return func(c *validateConfig) {
		c.allowNullToNotNull = true
	}
}

// ValidateDiff validates the difference between current and desired schema.
// It returns validation errors for breaking changes and warnings for potentially
// dangerous operations.
//
// Example:
//
//	result := schema.ValidateDiff(current, desired)
//	if result.HasBreakingChanges() {
//	    log.Fatal("Breaking changes detected:", result)
//	}
//	if result.HasWarnings() {
//	    log.Println("Warnings:", result)
//	}
func ValidateDiff(current, desired []*Table, opts ...ValidateOption) *ValidationResult {
	cfg := &validateConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	result := &ValidationResult{}
	currentMap := make(map[string]*Table, len(current))
	for _, t := range current {
		currentMap[t.Name] = t
	}
	desiredMap := make(map[string]*Table, len(desired))
	for _, t := range desired {
		desiredMap[t.Name] = t
	}

	// Check for dropped tables
	for name := range currentMap {
		if _, ok := desiredMap[name]; !ok {
			err := &ValidationError{
				Table:    name,
				Message:  "table will be dropped",
				Breaking: true,
			}
			if cfg.allowDropTable {
				result.Warnings = append(result.Warnings, err)
			} else {
				result.Errors = append(result.Errors, err)
			}
		}
	}

	// Check for changes in existing tables
	for name, desired := range desiredMap {
		current, exists := currentMap[name]
		if !exists {
			// New table, no validation needed
			continue
		}
		validateTableDiff(current, desired, cfg, result)
	}

	return result
}

func validateTableDiff(current, desired *Table, cfg *validateConfig, result *ValidationResult) {
	currentCols := make(map[string]*Column, len(current.Columns))
	for _, c := range current.Columns {
		currentCols[c.Name] = c
	}

	// Check for dropped columns
	for name := range currentCols {
		found := false
		for _, c := range desired.Columns {
			if c.Name == name {
				found = true
				break
			}
		}
		if !found {
			err := &ValidationError{
				Table:    current.Name,
				Column:   name,
				Message:  "column will be dropped",
				Breaking: true,
			}
			if cfg.allowDropColumn {
				result.Warnings = append(result.Warnings, err)
			} else {
				result.Errors = append(result.Errors, err)
			}
		}
	}

	// Check for column changes
	for _, desiredCol := range desired.Columns {
		currentCol, exists := currentCols[desiredCol.Name]
		if !exists {
			// New column
			if !desiredCol.Nullable && desiredCol.Default == nil {
				result.Warnings = append(result.Warnings, &ValidationError{
					Table:   current.Name,
					Column:  desiredCol.Name,
					Message: "new NOT NULL column without default value may fail if table has data",
				})
			}
			continue
		}

		// Type change
		if currentCol.Type != desiredCol.Type {
			result.Warnings = append(result.Warnings, &ValidationError{
				Table:   current.Name,
				Column:  desiredCol.Name,
				Message: fmt.Sprintf("column type changing from %v to %v", currentCol.Type, desiredCol.Type),
			})
		}

		// Nullable to NOT NULL
		if currentCol.Nullable && !desiredCol.Nullable {
			err := &ValidationError{
				Table:    current.Name,
				Column:   desiredCol.Name,
				Message:  "column changing from NULL to NOT NULL may fail if column has NULL values",
				Breaking: true,
			}
			if cfg.allowNullToNotNull {
				result.Warnings = append(result.Warnings, err)
			} else {
				result.Errors = append(result.Errors, err)
			}
		}

		// Size reduction
		if currentCol.Size > 0 && desiredCol.Size > 0 && desiredCol.Size < currentCol.Size {
			result.Warnings = append(result.Warnings, &ValidationError{
				Table:   current.Name,
				Column:  desiredCol.Name,
				Message: fmt.Sprintf("column size reducing from %d to %d may truncate data", currentCol.Size, desiredCol.Size),
			})
		}

		// Unique constraint added
		if !currentCol.Unique && desiredCol.Unique {
			result.Warnings = append(result.Warnings, &ValidationError{
				Table:   current.Name,
				Column:  desiredCol.Name,
				Message: "adding UNIQUE constraint may fail if duplicate values exist",
			})
		}
	}

	// Check for dropped indexes
	currentIdxs := make(map[string]*Index, len(current.Indexes))
	for _, idx := range current.Indexes {
		currentIdxs[idx.Name] = idx
	}
	for name := range currentIdxs {
		found := false
		for _, idx := range desired.Indexes {
			if idx.Name == name {
				found = true
				break
			}
		}
		if !found {
			err := &ValidationError{
				Table:   current.Name,
				Message: fmt.Sprintf("index %q will be dropped", name),
			}
			if cfg.allowDropIndex {
				result.Warnings = append(result.Warnings, err)
			} else {
				result.Errors = append(result.Errors, err)
			}
		}
	}
}

// ValidateTable validates a single table definition.
func ValidateTable(t *Table) *ValidationResult {
	result := &ValidationResult{}

	// Check for primary key
	if len(t.PrimaryKey) == 0 {
		result.Warnings = append(result.Warnings, &ValidationError{
			Table:   t.Name,
			Message: "table has no primary key",
		})
	}

	// Check for duplicate column names
	colNames := make(map[string]bool)
	for _, c := range t.Columns {
		if colNames[c.Name] {
			result.Errors = append(result.Errors, &ValidationError{
				Table:   t.Name,
				Column:  c.Name,
				Message: "duplicate column name",
			})
		}
		colNames[c.Name] = true
	}

	// Check for duplicate index names
	idxNames := make(map[string]bool)
	for _, idx := range t.Indexes {
		if idxNames[idx.Name] {
			result.Errors = append(result.Errors, &ValidationError{
				Table:   t.Name,
				Message: fmt.Sprintf("duplicate index name: %s", idx.Name),
			})
		}
		idxNames[idx.Name] = true

		// Check that index columns exist
		for _, col := range idx.Columns {
			if col != nil && !colNames[col.Name] {
				result.Errors = append(result.Errors, &ValidationError{
					Table:   t.Name,
					Message: fmt.Sprintf("index %q references non-existent column %q", idx.Name, col.Name),
				})
			}
		}
	}

	// Check foreign keys
	for _, fk := range t.ForeignKeys {
		// Check that FK columns exist
		for _, col := range fk.Columns {
			if !colNames[col.Name] {
				result.Errors = append(result.Errors, &ValidationError{
					Table:   t.Name,
					Message: fmt.Sprintf("foreign key references non-existent column %q", col.Name),
				})
			}
		}
	}

	return result
}

// ValidateSchema validates all tables in a schema.
func ValidateSchema(tables []*Table) *ValidationResult {
	result := &ValidationResult{}

	tableNames := make(map[string]bool)
	for _, t := range tables {
		// Check for duplicate table names
		if tableNames[t.Name] {
			result.Errors = append(result.Errors, &ValidationError{
				Table:   t.Name,
				Message: "duplicate table name",
			})
		}
		tableNames[t.Name] = true

		// Validate individual table
		tableResult := ValidateTable(t)
		result.Errors = append(result.Errors, tableResult.Errors...)
		result.Warnings = append(result.Warnings, tableResult.Warnings...)
	}

	// Validate foreign key references
	for _, t := range tables {
		for _, fk := range t.ForeignKeys {
			if !tableNames[fk.RefTable.Name] {
				result.Errors = append(result.Errors, &ValidationError{
					Table:   t.Name,
					Message: fmt.Sprintf("foreign key references non-existent table %q", fk.RefTable.Name),
				})
			}
		}
	}

	return result
}
