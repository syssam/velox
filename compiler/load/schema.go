package load

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// Schema represents a velox.Schema that was loaded from a compiled user package.
type Schema struct {
	Name         string         `json:"name,omitempty"`
	Pos          string         `json:"-"`
	View         bool           `json:"view,omitempty"`
	Config       velox.Config   `json:"config"`
	Edges        []*Edge        `json:"edges,omitempty"`
	Fields       []*Field       `json:"fields,omitempty"`
	Indexes      []*Index       `json:"indexes,omitempty"`
	Hooks        []*Position    `json:"hooks,omitempty"`
	Interceptors []*Position    `json:"interceptors,omitempty"`
	Policy       []*Position    `json:"policy,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

// Position describes a position in the schema.
type Position struct {
	Index      int  // Index in the field/hook list.
	MixedIn    bool // Indicates if the schema object was mixed-in.
	MixinIndex int  // Mixin index in the mixin list.
}

// Field represents a velox.Field that was loaded from a compiled user package.
type Field struct {
	Name             string                  `json:"name,omitempty"`
	Info             *field.TypeInfo         `json:"type,omitempty"`
	ValueScanner     bool                    `json:"value_scanner,omitempty"`
	Tag              string                  `json:"tag,omitempty"`
	Size             *int64                  `json:"size,omitempty"`
	Enums            []struct{ N, V string } `json:"enums,omitempty"`
	Unique           bool                    `json:"unique,omitempty"`
	Nillable         bool                    `json:"nillable,omitempty"`
	Optional         bool                    `json:"optional,omitempty"`
	Default          bool                    `json:"default,omitempty"`
	DefaultValue     any                     `json:"default_value,omitempty"`
	DefaultKind      reflect.Kind            `json:"default_kind,omitempty"`
	UpdateDefault    bool                    `json:"update_default,omitempty"`
	Immutable        bool                    `json:"immutable,omitempty"`
	Validators       int                     `json:"validators,omitempty"`
	StorageKey       string                  `json:"storage_key,omitempty"`
	Position         *Position               `json:"position,omitempty"`
	Sensitive        bool                    `json:"sensitive,omitempty"`
	SchemaType       map[string]string       `json:"schema_type,omitempty"`
	Annotations      map[string]any          `json:"annotations,omitempty"`
	Comment          string                  `json:"comment,omitempty"`
	Deprecated       bool                    `json:"deprecated,omitempty"`
	DeprecatedReason string                  `json:"deprecated_reason,omitempty"`
}

// Edge represents a velox.Edge that was loaded from a compiled user package.
type Edge struct {
	Name        string                 `json:"name,omitempty"`
	Type        string                 `json:"type,omitempty"`
	Tag         string                 `json:"tag,omitempty"`
	Field       string                 `json:"field,omitempty"`
	RefName     string                 `json:"ref_name,omitempty"`
	Ref         *Edge                  `json:"ref,omitempty"`
	Through     *struct{ N, T string } `json:"through,omitempty"`
	Unique      bool                   `json:"unique,omitempty"`
	Inverse     bool                   `json:"inverse,omitempty"`
	Required    bool                   `json:"required,omitempty"`
	Immutable   bool                   `json:"immutable,omitempty"`
	StorageKey  *edge.StorageKey       `json:"storage_key,omitempty"`
	Annotations map[string]any         `json:"annotations,omitempty"`
	Comment     string                 `json:"comment,omitempty"`
}

// Index represents a velox.Index that was loaded from a compiled user package.
type Index struct {
	Unique      bool           `json:"unique,omitempty"`
	Edges       []string       `json:"edges,omitempty"`
	Fields      []string       `json:"fields,omitempty"`
	StorageKey  string         `json:"storage_key,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

// NewEdge creates a loaded edge from edge descriptor.
// It returns an error if the descriptor contains an error.
func NewEdge(ed *edge.Descriptor) (*Edge, error) {
	if ed.Err != nil {
		return nil, ed.Err
	}
	ne := &Edge{
		Tag:         ed.Tag,
		Type:        ed.Type,
		Name:        ed.Name,
		Field:       ed.Field,
		Unique:      ed.Unique,
		Inverse:     ed.Inverse,
		Required:    ed.Required,
		Immutable:   ed.Immutable,
		RefName:     ed.RefName,
		Through:     ed.Through,
		StorageKey:  ed.StorageKey,
		Comment:     ed.Comment,
		Annotations: make(map[string]any),
	}
	for _, at := range ed.Annotations {
		ne.addAnnotation(at)
	}
	if ref := ed.Ref; ref != nil {
		refEdge, err := NewEdge(ref)
		if err != nil {
			return nil, err
		}
		ne.Ref = refEdge
		ne.StorageKey = ne.Ref.StorageKey
	}
	return ne, nil
}

// NewField creates a loaded field from field descriptor.
func NewField(fd *field.Descriptor) (*Field, error) {
	if fd.Err != nil {
		return nil, fmt.Errorf("field %q: %v", fd.Name, fd.Err)
	}
	sf := &Field{
		Name:             fd.Name,
		Info:             fd.Info,
		ValueScanner:     fd.ValueScanner != nil,
		Tag:              fd.Tag,
		Enums:            fd.Enums,
		Unique:           fd.Unique,
		Nillable:         fd.Nillable,
		Optional:         fd.Optional,
		Default:          fd.Default != nil,
		UpdateDefault:    fd.UpdateDefault != nil,
		Immutable:        fd.Immutable,
		StorageKey:       fd.StorageKey,
		Validators:       len(fd.Validators),
		Sensitive:        fd.Sensitive,
		SchemaType:       fd.SchemaType,
		Annotations:      make(map[string]any),
		Comment:          fd.Comment,
		Deprecated:       fd.Deprecated,
		DeprecatedReason: fd.DeprecatedReason,
	}
	for _, at := range fd.Annotations {
		sf.addAnnotation(at)
	}
	if sf.Info == nil {
		return nil, fmt.Errorf("missing type info for field %q", sf.Name)
	}
	if size := int64(fd.Size); size != 0 {
		sf.Size = &size
	}
	if sf.Default {
		sf.DefaultKind = reflect.TypeOf(fd.Default).Kind()
	}
	// If the default value can be encoded to the generator.
	// For example, not a function like time.Now.
	if _, err := json.Marshal(fd.Default); err == nil {
		sf.DefaultValue = fd.Default
	}
	return sf, nil
}

// NewIndex creates a loaded index from index descriptor.
func NewIndex(idx *index.Descriptor) *Index {
	ni := &Index{
		Edges:       idx.Edges,
		Fields:      idx.Fields,
		Unique:      idx.Unique,
		StorageKey:  idx.StorageKey,
		Annotations: make(map[string]any),
	}
	for _, at := range idx.Annotations {
		ni.addAnnotation(at)
	}
	return ni
}

// MarshalSchema encodes the velox.Schema interface into a JSON
// that can be decoded into the Schema objects declared above.
func MarshalSchema(iface velox.Interface) (b []byte, err error) {
	s := &Schema{
		Config:      iface.Config(),
		Name:        indirect(reflect.TypeOf(iface)).Name(),
		Annotations: make(map[string]any),
	}
	_, s.View = iface.(velox.Viewer)
	if err = s.loadMixin(iface); err != nil {
		return nil, fmt.Errorf("schema %q: %w", s.Name, err)
	}
	// Schema annotations override mixed-in annotations.
	for _, at := range iface.Annotations() {
		if e, ok := at.(interface{ Err() error }); ok && e.Err() != nil {
			return nil, fmt.Errorf("schema %q: %w", s.Name, e.Err())
		}
		s.addAnnotation(at)
	}
	if err = s.loadFields(iface); err != nil {
		return nil, fmt.Errorf("schema %q: %w", s.Name, err)
	}
	edges, err := safeEdges(iface)
	if err != nil {
		return nil, fmt.Errorf("schema %q: %w", s.Name, err)
	}
	for _, e := range edges {
		var ne *Edge
		ne, err = NewEdge(e.Descriptor())
		if err != nil {
			return nil, fmt.Errorf("schema %q: %w", s.Name, err)
		}
		s.Edges = append(s.Edges, ne)
	}
	indexes, err := safeIndexes(iface)
	if err != nil {
		return nil, fmt.Errorf("schema %q: %w", s.Name, err)
	}
	for _, idx := range indexes {
		s.Indexes = append(s.Indexes, NewIndex(idx.Descriptor()))
	}
	if err := s.loadHooks(iface); err != nil {
		return nil, fmt.Errorf("schema %q: %w", s.Name, err)
	}
	if err := s.loadInterceptors(iface); err != nil {
		return nil, fmt.Errorf("schema %q: %w", s.Name, err)
	}
	if err := s.loadPolicy(iface); err != nil {
		return nil, fmt.Errorf("schema %q: %w", s.Name, err)
	}
	return json.Marshal(s)
}

// UnmarshalSchema decodes the given buffer to a loaded schema.
func UnmarshalSchema(buf []byte) (*Schema, error) {
	s := &Schema{}
	if err := json.Unmarshal(buf, s); err != nil {
		return nil, err
	}
	for _, f := range s.Fields {
		if err := f.defaults(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// loadMixin loads mixin to schema from velox.Interface.
func (s *Schema) loadMixin(iface velox.Interface) error {
	mixin, err := safeMixin(iface)
	if err != nil {
		return err
	}
	for i, mx := range mixin {
		name := indirect(reflect.TypeOf(mx)).Name()
		fields, ferr := safeFields(mx)
		if ferr != nil {
			return fmt.Errorf("mixin %q: %w", name, ferr)
		}
		for j, f := range fields {
			sf, ferr := NewField(f.Descriptor())
			if ferr != nil {
				return fmt.Errorf("mixin %q: %w", name, ferr)
			}
			sf.Position = &Position{
				Index:      j,
				MixedIn:    true,
				MixinIndex: i,
			}
			s.Fields = append(s.Fields, sf)
		}
		edges, eerr := safeEdges(mx)
		if eerr != nil {
			return fmt.Errorf("mixin %q: %w", name, eerr)
		}
		for _, e := range edges {
			ne, eerr := NewEdge(e.Descriptor())
			if eerr != nil {
				return fmt.Errorf("mixin %q: %w", name, eerr)
			}
			s.Edges = append(s.Edges, ne)
		}
		indexes, ierr := safeIndexes(mx)
		if ierr != nil {
			return fmt.Errorf("mixin %q: %w", name, ierr)
		}
		for _, idx := range indexes {
			s.Indexes = append(s.Indexes, NewIndex(idx.Descriptor()))
		}
		hooks, herr := safeHooks(mx)
		if herr != nil {
			return fmt.Errorf("mixin %q: %w", name, herr)
		}
		for j := range hooks {
			s.Hooks = append(s.Hooks, &Position{
				Index:      j,
				MixedIn:    true,
				MixinIndex: i,
			})
		}
		inters, terr := safeInterceptors(mx)
		if terr != nil {
			return fmt.Errorf("mixin %q: %w", name, terr)
		}
		for j := range inters {
			s.Interceptors = append(s.Interceptors, &Position{
				Index:      j,
				MixedIn:    true,
				MixinIndex: i,
			})
		}
		policy, perr := safePolicy(mx)
		if perr != nil {
			return fmt.Errorf("mixin %q: %w", name, perr)
		}
		if policy != nil {
			s.Policy = append(s.Policy, &Position{
				MixedIn:    true,
				MixinIndex: i,
			})
		}
		for _, at := range mx.Annotations() {
			s.addAnnotation(at)
		}
	}
	return nil
}

// loadFields loads field to schema from velox.Interface.
func (s *Schema) loadFields(iface velox.Interface) error {
	fields, err := safeFields(iface)
	if err != nil {
		return err
	}
	for i, f := range fields {
		sf, err := NewField(f.Descriptor())
		if err != nil {
			return err
		}
		sf.Position = &Position{Index: i}
		s.Fields = append(s.Fields, sf)
	}
	return nil
}

func (s *Schema) loadHooks(iface velox.Interface) error {
	hooks, err := safeHooks(iface)
	if err != nil {
		return err
	}
	for i := range hooks {
		s.Hooks = append(s.Hooks, &Position{
			Index:   i,
			MixedIn: false,
		})
	}
	return nil
}

func (s *Schema) loadInterceptors(iface velox.Interface) error {
	inters, err := safeInterceptors(iface)
	if err != nil {
		return err
	}
	for i := range inters {
		s.Interceptors = append(s.Interceptors, &Position{
			Index:   i,
			MixedIn: false,
		})
	}
	return nil
}

func (s *Schema) loadPolicy(iface velox.Interface) error {
	policy, err := safePolicy(iface)
	if err != nil {
		return err
	}
	if policy != nil {
		s.Policy = append(s.Policy, &Position{})
	}
	return nil
}

func (s *Schema) addAnnotation(an schema.Annotation) {
	addAnnotation(s.Annotations, an)
}

func (e *Edge) addAnnotation(an schema.Annotation) {
	addAnnotation(e.Annotations, an)
}

func (i *Index) addAnnotation(an schema.Annotation) {
	addAnnotation(i.Annotations, an)
}

func (f *Field) addAnnotation(an schema.Annotation) {
	addAnnotation(f.Annotations, an)
}

func addAnnotation(annotations map[string]any, an schema.Annotation) {
	curr, ok := annotations[an.Name()]
	if !ok {
		annotations[an.Name()] = an
		return
	}
	if m, ok := curr.(schema.Merger); ok {
		annotations[an.Name()] = m.Merge(an)
	}
}

func (f *Field) defaults() error {
	if !f.Default || !f.Info.Numeric() || f.DefaultKind == reflect.Func {
		return nil
	}
	n, ok := f.DefaultValue.(float64)
	if !ok {
		return fmt.Errorf("unexpected default value type for field: %q", f.Name)
	}
	switch t := f.Info.Type; {
	case t >= field.TypeInt8 && t <= field.TypeInt64:
		f.DefaultValue = int64(n)
	case t >= field.TypeUint8 && t <= field.TypeUint64:
		f.DefaultValue = uint64(n)
	case t == field.TypeFloat32:
		f.DefaultValue = float32(n)
	}
	return nil
}

// safeFields wraps the schema.Fields and mixin.Fields method with recover to ensure no panics in marshaling.
func safeFields(fd interface{ Fields() []velox.Field }) (fields []velox.Field, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%T.Fields panics: %v", fd, v)
			fields = nil
		}
	}()
	return fd.Fields(), nil
}

// safeEdges wraps the schema.Edges method with recover to ensure no panics in marshaling.
func safeEdges(iface interface{ Edges() []velox.Edge }) (edges []velox.Edge, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%T.Edges panics: %v", iface, v)
			edges = nil
		}
	}()
	return iface.Edges(), nil
}

// safeIndexes wraps the schema.Indexes method with recover to ensure no panics in marshaling.
func safeIndexes(iface interface{ Indexes() []velox.Index }) (indexes []velox.Index, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%T.Indexes panics: %v", iface, v)
			indexes = nil
		}
	}()
	return iface.Indexes(), nil
}

// safeMixin wraps the schema.Mixin method with recover to ensure no panics in marshaling.
func safeMixin(iface velox.Interface) (mixin []velox.Mixin, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%T.Mixin panics: %v", iface, v)
			mixin = nil
		}
	}()
	return iface.Mixin(), nil
}

// safeHooks wraps the schema.Hooks method with recover to ensure no panics in marshaling.
func safeHooks(iface interface{ Hooks() []velox.Hook }) (hooks []velox.Hook, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%T.Hooks panics: %v", iface, v)
			hooks = nil
		}
	}()
	return iface.Hooks(), nil
}

// safeInterceptors wraps the schema.Interceptors method with recover to ensure no panics in marshaling.
func safeInterceptors(iface interface{ Interceptors() []velox.Interceptor }) (inters []velox.Interceptor, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%T.Interceptors panics: %v", iface, v)
			inters = nil
		}
	}()
	return iface.Interceptors(), nil
}

// safePolicy wraps the schema.Policy method with recover to ensure no panics in marshaling.
func safePolicy(iface interface{ Policy() velox.Policy }) (policy velox.Policy, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%T.Policy panics: %v", iface, v)
			policy = nil
		}
	}()
	return iface.Policy(), nil
}

// indirect returns the type at the end of indirection.
// This function is duplicated from compiler/internal/reflectutil because schema.go
// is embedded (//go:embed schema.go) into the runner binary via load.go, and the
// runner binary cannot access internal packages. compiler/compiler.go uses the
// shared reflectutil.Indirect instead.
func indirect(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
