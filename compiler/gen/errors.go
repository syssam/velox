// Package gen provides code generation for Velox schemas.
package gen

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for common failure cases.
var (
	// ErrInvalidSchema indicates a schema definition error.
	ErrInvalidSchema = errors.New("velox: invalid schema")
	// ErrMissingConfig indicates a configuration error.
	ErrMissingConfig = errors.New("velox: missing configuration")
	// ErrInvalidEdge indicates an edge definition error.
	ErrInvalidEdge = errors.New("velox: invalid edge definition")
	// ErrGenerationFailed indicates a code generation failure.
	ErrGenerationFailed = errors.New("velox: code generation failed")
	// ErrValidationFailed indicates a validation failure.
	ErrValidationFailed = errors.New("velox: validation failed")
)

// SchemaError represents a schema definition error.
type SchemaError struct {
	Type       string // Entity type name
	Field      string // Field name (if applicable)
	Message    string
	Cause      error
	Suggestion string // Optional actionable fix hint
}

// Error implements the error interface.
func (e *SchemaError) Error() string {
	var b strings.Builder
	b.WriteString("velox: schema error")
	if e.Type != "" {
		b.WriteString(" on type ")
		b.WriteString(e.Type)
	}
	if e.Field != "" {
		b.WriteString(" field ")
		b.WriteString(e.Field)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.Cause != nil {
		b.WriteString(": ")
		b.WriteString(e.Cause.Error())
	}
	if e.Suggestion != "" {
		b.WriteString(" (hint: ")
		b.WriteString(e.Suggestion)
		b.WriteString(")")
	}
	return b.String()
}

// Unwrap returns the underlying error.
func (e *SchemaError) Unwrap() error {
	return e.Cause
}

// Is reports whether the target matches the sentinel error for SchemaError.
func (e *SchemaError) Is(target error) bool {
	return target == ErrInvalidSchema
}

// NewSchemaError creates a new SchemaError.
func NewSchemaError(typeName, fieldName, message string, cause error) *SchemaError {
	return &SchemaError{
		Type:    typeName,
		Field:   fieldName,
		Message: message,
		Cause:   cause,
	}
}

// ConfigError represents a configuration error.
type ConfigError struct {
	Option     string
	Value      any
	Message    string
	Cause      error
	Suggestion string // Optional actionable fix hint
}

// Error implements the error interface.
func (e *ConfigError) Error() string {
	var b strings.Builder
	if e.Value != nil {
		fmt.Fprintf(&b, "velox: config error for %q (value: %v): %s", e.Option, e.Value, e.Message)
	} else {
		fmt.Fprintf(&b, "velox: config error for %q: %s", e.Option, e.Message)
	}
	if e.Cause != nil {
		b.WriteString(": ")
		b.WriteString(e.Cause.Error())
	}
	if e.Suggestion != "" {
		b.WriteString(" (hint: ")
		b.WriteString(e.Suggestion)
		b.WriteString(")")
	}
	return b.String()
}

// Is reports whether the target matches the sentinel error for ConfigError.
func (e *ConfigError) Is(target error) bool {
	return target == ErrMissingConfig
}

// Unwrap returns the underlying error.
func (e *ConfigError) Unwrap() error {
	return e.Cause
}

// NewConfigError creates a new ConfigError.
func NewConfigError(option string, value any, message string, cause error) *ConfigError {
	return &ConfigError{
		Option:  option,
		Value:   value,
		Message: message,
		Cause:   cause,
	}
}

// EdgeError represents an edge/relationship error.
type EdgeError struct {
	From       string
	To         string
	Edge       string
	Message    string
	Cause      error
	Suggestion string // Optional actionable fix hint
}

// Error implements the error interface.
func (e *EdgeError) Error() string {
	var b strings.Builder
	b.WriteString("velox: edge error")
	if e.Edge != "" {
		b.WriteString(" on edge ")
		b.WriteString(e.Edge)
	}
	if e.From != "" && e.To != "" {
		fmt.Fprintf(&b, " (%s -> %s)", e.From, e.To)
	} else if e.From != "" {
		b.WriteString(" from ")
		b.WriteString(e.From)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.Cause != nil {
		b.WriteString(": ")
		b.WriteString(e.Cause.Error())
	}
	if e.Suggestion != "" {
		b.WriteString(" (hint: ")
		b.WriteString(e.Suggestion)
		b.WriteString(")")
	}
	return b.String()
}

// Unwrap returns the underlying error.
func (e *EdgeError) Unwrap() error {
	return e.Cause
}

// Is reports whether the target matches the sentinel error for EdgeError.
func (e *EdgeError) Is(target error) bool {
	return target == ErrInvalidEdge
}

// NewEdgeError creates a new EdgeError.
func NewEdgeError(from, to, edgeName, message string, cause error) *EdgeError {
	return &EdgeError{
		From:    from,
		To:      to,
		Edge:    edgeName,
		Message: message,
		Cause:   cause,
	}
}

// GenerationError represents a code generation error.
type GenerationError struct {
	Phase   string // "entity", "client", "predicate", etc.
	Entity  string // Entity type name (if applicable)
	Feature string // Feature name (if applicable)
	File    string
	Message string
	Cause   error
}

// Error implements the error interface.
func (e *GenerationError) Error() string {
	var b strings.Builder
	b.WriteString("velox: generation error")
	if e.Phase != "" {
		b.WriteString(" in phase ")
		b.WriteString(e.Phase)
	}
	if e.Entity != "" {
		b.WriteString(" for entity ")
		b.WriteString(e.Entity)
	}
	if e.Feature != "" {
		b.WriteString(" [feature: ")
		b.WriteString(e.Feature)
		b.WriteString("]")
	}
	if e.File != "" {
		b.WriteString(" (file: ")
		b.WriteString(e.File)
		b.WriteString(")")
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.Cause != nil {
		b.WriteString(": ")
		b.WriteString(e.Cause.Error())
	}
	return b.String()
}

// Unwrap returns the underlying error.
func (e *GenerationError) Unwrap() error {
	return e.Cause
}

// Is reports whether the target matches the sentinel error for GenerationError.
func (e *GenerationError) Is(target error) bool {
	return target == ErrGenerationFailed
}

// NewGenerationError creates a new GenerationError.
func NewGenerationError(phase, file, message string, cause error) *GenerationError {
	return &GenerationError{
		Phase:   phase,
		File:    file,
		Message: message,
		Cause:   cause,
	}
}

// NewEntityGenerationError creates a GenerationError with entity context.
func NewEntityGenerationError(phase, entity, file, message string, cause error) *GenerationError {
	return &GenerationError{
		Phase:   phase,
		Entity:  entity,
		File:    file,
		Message: message,
		Cause:   cause,
	}
}

// NewFeatureGenerationError creates a GenerationError with feature context.
func NewFeatureGenerationError(phase, feature, file, message string, cause error) *GenerationError {
	return &GenerationError{
		Phase:   phase,
		Feature: feature,
		File:    file,
		Message: message,
		Cause:   cause,
	}
}

// SchemaValidationError represents a schema validation error during code generation.
// This is distinct from velox.ValidationError which represents runtime field validation.
type SchemaValidationError struct {
	Type    string
	Field   string
	Value   any
	Message string
	Cause   error
}

// Error implements the error interface.
func (e *SchemaValidationError) Error() string {
	var b strings.Builder
	b.WriteString("velox: schema validation error")
	if e.Type != "" {
		b.WriteString(" on type ")
		b.WriteString(e.Type)
	}
	if e.Field != "" {
		b.WriteString(" field ")
		b.WriteString(e.Field)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.Cause != nil {
		b.WriteString(": ")
		b.WriteString(e.Cause.Error())
	}
	return b.String()
}

// Unwrap returns the underlying error.
func (e *SchemaValidationError) Unwrap() error {
	return e.Cause
}

// Is reports whether the target matches the sentinel error for SchemaValidationError.
func (e *SchemaValidationError) Is(target error) bool {
	return target == ErrValidationFailed
}

// NewSchemaValidationError creates a new SchemaValidationError.
func NewSchemaValidationError(typeName, field string, value any, message string, cause error) *SchemaValidationError {
	return &SchemaValidationError{
		Type:    typeName,
		Field:   field,
		Value:   value,
		Message: message,
		Cause:   cause,
	}
}

// IsSchemaError reports whether the error is a SchemaError.
func IsSchemaError(err error) bool {
	var target *SchemaError
	return errors.As(err, &target)
}

// IsConfigError reports whether the error is a ConfigError.
func IsConfigError(err error) bool {
	var target *ConfigError
	return errors.As(err, &target)
}

// IsEdgeError reports whether the error is an EdgeError.
func IsEdgeError(err error) bool {
	var target *EdgeError
	return errors.As(err, &target)
}

// IsGenerationError reports whether the error is a GenerationError.
func IsGenerationError(err error) bool {
	var target *GenerationError
	return errors.As(err, &target)
}

// IsSchemaValidationError reports whether the error is a SchemaValidationError.
func IsSchemaValidationError(err error) bool {
	var target *SchemaValidationError
	return errors.As(err, &target)
}
