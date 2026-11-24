package gqlplugin

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/plugin"
)

func respond(t *testing.T, in string) plugin.Response {
	t.Helper()
	var out bytes.Buffer
	if err := plugin.Run(Supports, strings.NewReader(in), &out, Generate); err != nil {
		t.Fatal(err)
	}
	var resp plugin.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("%v: %q", err, out.String())
	}
	if resp.Protocol != plugin.ProtocolVersion {
		t.Errorf("the response says protocol %d", resp.Protocol)
	}
	return resp
}

func request(t *testing.T, r plugin.Request) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSupportsThisVelox(t *testing.T) {
	if Supports.Protocol != plugin.ProtocolVersion || Supports.Model != plugin.ModelVersion {
		t.Errorf("velox-graphql is written for protocol %d and model %d; velox has %d and %d: update it", Supports.Protocol, Supports.Model, plugin.ProtocolVersion, plugin.ModelVersion)
	}
}

// oldVelox is the options of an application that moved from the old velox
// and keeps its GraphQL API, as the guide gives them.
const oldVelox = "names=schema,filters=lenient,node=on,sdlfiles=entity,orderfields=all,queryfields=all,optional=nullable,requirededges=nonnull,jsontypes=named,idops=equality,inputkeys=column,apptypes=JSON+UUID+Uint64"

func TestRun(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", ElemType: "int64"}, {Name: "name", GoName: "Name", Type: "string", ElemType: "string"}},
	}}}
	resp := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, Generator: "velox-gen (devel)", OutPackage: "gen", OutPath: "example.com/app/gen", Model: m}))
	var paths []string
	for _, f := range resp.Files {
		paths = append(paths, f.Path)
		if strings.Contains(f.Content, "DO NOT EDIT") {
			t.Errorf("%s has the generated-file header, which velox-gen adds", f.Path)
		}
	}
	if resp.Error != "" || len(resp.Warnings) != 0 || strings.Join(paths, " ") != "velox.graphql user_gql.go" ||
		!strings.Contains(resp.Files[0].Content, "type User {\n  id: ID!\n") ||
		!strings.HasPrefix(resp.Files[1].Content, "package gen\n") || !strings.Contains(resp.Files[1].Content, "type UserConnection struct {") {
		t.Errorf("%+v", resp)
	}

	// only=enums: the enum marshalers alone, of a model whose User has a
	// role; no velox.graphql.
	em := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "role", GoName: "Role", Type: "enum", Enum: "UserRole", EnumValues: []string{"admin", "viewer"}}},
	}}, Enums: []*plugin.Enum{{Type: "UserRole", Values: []string{"admin", "viewer"}, Consts: []string{"UserRoleAdmin", "UserRoleViewer"}}}}
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"only": "enums"}, Model: em}))
	if resp.Error != "" || len(resp.Files) != 1 || resp.Files[0].Path != "user_gql.go" ||
		!strings.Contains(resp.Files[0].Content, "func (e UserRole) MarshalGQL(") || strings.Contains(resp.Files[0].Content, "UserConnection") {
		t.Errorf("only=enums: %+v", resp)
	}

	// The old velox's options, as one application gives them: names from
	// the schema, Node bound to the request's package with its Go in
	// noder_gql.go, lenient filters, and a file per entity.
	vm := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", ElemType: "int64"}, {Name: "image_url", GoName: "ImageURL", Type: "string", ElemType: "string"}},
	}}}
	old := map[string]string{}
	for kv := range strings.SplitSeq(oldVelox, ",") {
		k, v, _ := strings.Cut(kv, "=")
		old[k] = v
	}
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: old, Model: vm}))
	if resp.Error != "" || len(resp.Files) != 4 || resp.Files[1].Path != "velox_user.graphql" || resp.Files[2].Path != "noder_gql.go" ||
		!strings.Contains(resp.Files[1].Content, "  imageUrl: String!\n") ||
		!strings.Contains(resp.Files[0].Content, "interface Node @goModel(model: \"example.com/app/gen.Noder\")") ||
		!strings.Contains(strings.Join(strings.Fields(resp.Files[3].Content), " "), " LenientFilters: true,") {
		t.Errorf("%s: %+v", oldVelox, resp)
	}

	// names= and filters= stand alone.
	for _, c := range []struct {
		opts    map[string]string
		name    string
		filters bool
	}{
		{map[string]string{"names": "go", "filters": "strict"}, "imageURL", false},
		{map[string]string{"names": "schema"}, "imageUrl", false},
		{map[string]string{"names": "go", "filters": "lenient"}, "imageURL", true},
	} {
		resp := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: c.opts, Model: vm}))
		goSrc := strings.Join(strings.Fields(resp.Files[len(resp.Files)-1].Content), " ")
		if resp.Error != "" || !strings.Contains(resp.Files[0].Content, "  "+c.name+": String!\n") || strings.Contains(goSrc, " LenientFilters: true,") != c.filters {
			t.Errorf("%v: %+v", c.opts, resp)
		}
	}

	// node=on writes Node, bound to the request's package, and
	// noder_gql.go; node=off, the default, writes neither.
	for _, c := range []struct {
		opts map[string]string
		node bool
	}{
		{map[string]string{"node": "on"}, true},
		{map[string]string{"node": "off"}, false},
		{nil, false},
	} {
		resp := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: c.opts, Model: vm}))
		noder := slices.ContainsFunc(resp.Files, func(f plugin.File) bool { return f.Path == "noder_gql.go" })
		node := strings.Contains(resp.Files[0].Content, "interface Node @goModel(model: \"example.com/app/gen.Noder\")")
		if resp.Error != "" || noder != c.node || node != c.node {
			t.Errorf("%v: noder_gql.go %v, Node %v; want %v: %+v", c.opts, noder, node, c.node, resp)
		}
	}

	// sdlfiles=entity: velox_<label>.graphql. entities=User
	// writes User's SDL alone, and the same Go.
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"sdlfiles": "entity"}, Model: m}))
	if resp.Error != "" || len(resp.Files) != 3 || resp.Files[0].Path != "velox.graphql" || resp.Files[1].Path != "velox_user.graphql" ||
		!strings.HasPrefix(resp.Files[1].Content, "# User\n") || strings.Contains(resp.Files[0].Content, "type User ") {
		t.Errorf("sdlfiles=entity: %+v", resp)
	}
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"entities": "User"}, Model: m}))
	if resp.Error != "" || len(resp.Files) != 2 || !reflect.DeepEqual(resp.Files, respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Model: m})).Files) {
		t.Errorf("entities=User: %+v", resp)
	}

	// engine: graphql-go's SDL binds User with @goModel and its Go is the
	// same files; engine=gqlgen is the default's bytes; only=enums takes
	// either.
	gg := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"engine": "graphql-go"}, Model: m}))
	if gg.Error != "" || len(gg.Files) != 2 || !strings.Contains(gg.Files[0].Content, "type User @goModel(model: \"example.com/app/gen.User\") {\n") || gg.Files[1].Path != "user_gql.go" {
		t.Errorf("engine=graphql-go: %+v", gg)
	}
	def := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Model: m}))
	if eg := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"engine": "gqlgen"}, Model: m})); !reflect.DeepEqual(eg, def) {
		t.Errorf("engine=gqlgen: %+v, want the default's %+v", eg, def)
	}
	if en := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"only": "enums", "engine": "graphql-go"}, Model: em})); en.Error != "" || len(en.Files) != 1 {
		t.Errorf("only=enums,engine=graphql-go: %+v", en)
	}

	for name, c := range map[string]struct{ in, want string }{
		"whitelist and only": {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "where": "whitelist"}, Model: m}), "give one or the other"},
		"compat":             {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"compat": "ent"}, Model: m}), compatGone},
		"protocol":           {request(t, plugin.Request{Protocol: 1, Model: m}), "plugin protocol 1"},
		"version":            {request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 1}}), "model is version 1"},
		"no model":           {request(t, plugin.Request{Protocol: 2}), "no model"},
		"malformed":          {"{", "request"},
		"names":              {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"names": "snake"}, Model: m}), "velox-graphql: names=snake: names is go, a field's Go name (imageURL); or schema, its schema name (imageUrl)"},
		"names empty":        {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"names": ""}, Model: m}), "it is given names"},
		"filters":            {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"filters": "loose"}, Model: m}), "velox-graphql: filters=loose: filters is strict, an empty In matches no node; or lenient, it adds nothing"},
		"idops":              {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"idops": "id"}, Model: m}), "velox-graphql: idops=id: idops is kind, a field named like a key gets its kind's predicates; or equality, equality alone"},
		"orderfields":        {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"orderfields": "every"}, Model: m}), "velox-graphql: orderfields=every: orderfields is annotated"},
		"queryfields":        {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"queryfields": "on"}, Model: m}), "velox-graphql: queryfields=on: queryfields is annotated"},
		"optional":           {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"optional": "null"}, Model: m}), "velox-graphql: optional=null: optional is nonnull"},
		"requirededges":      {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"requirededges": "required"}, Model: m}), "velox-graphql: requirededges=required: requirededges is nullable"},
		"inputkeys":          {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"inputkeys": "field"}, Model: m}), "velox-graphql: inputkeys=field: inputkeys is edge"},
		"jsontypes":          {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"jsontypes": "go"}, Model: m}), "velox-graphql: jsontypes=go: jsontypes is json"},
		"idops and only":     {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "idops": "equality"}, Model: m}), "give one or the other"},
		"names and only":     {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "names": "schema"}, Model: m}), "give one or the other"},
		"filters and only":   {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "filters": "strict"}, Model: m}), "give one or the other"},
		"node":               {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"node": "true"}, Model: m}), "velox-graphql: node=true: node is off, no Node; or on, Relay's Node and node(id:)"},
		"node empty":         {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"node": ""}, Model: m}), "it is given node"},
		"node and only":      {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "node": "on"}, Model: m}), "give one or the other"},
		"options":            {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"x": "y", "a": ""}, Model: m}), "velox-graphql takes the options only=enums, names=go or names=schema, filters=strict or filters=lenient, node=off or node=on, idops=kind or idops=equality, orderfields=annotated or orderfields=all, queryfields=annotated or queryfields=all, optional=nonnull or optional=nullable, requirededges=nullable or requirededges=nonnull, inputkeys=edge or inputkeys=column, jsontypes=json or jsontypes=named, where=whitelist, engine=gqlgen or engine=graphql-go, gqlgenx=blank, sdlfiles=one or sdlfiles=entity, entities=A+B and apptypes=A+B; it is given a, x=y"},
		"sdlfiles":           {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"sdlfiles": "type"}, Model: m}), "it is given sdlfiles=type"},
		"sdlfiles and only":  {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "sdlfiles": "entity"}, Model: m}), "give one or the other"},
		"entities and only":  {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "entities": "User"}, Model: m}), "give one or the other"},
		"entities empty":     {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"entities": ""}, Model: m}), "it is given entities"},
		"entities twice":     {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"entities": "User+User"}, Model: m}), "entities=User+User: name each entity once, separated by +"},
		"entities gap":       {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"entities": "User++Post"}, Model: m}), "name each entity once"},
		"entities unknown":   {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"entities": "User+Post"}, Model: m}), "entities= names Post, which the schema package does not declare"},
		"apptypes and only":  {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "apptypes": "Money"}, Model: m}), "give one or the other"},
		"apptypes twice":     {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"entities": "User", "apptypes": "Money+Money"}, Model: m}), "apptypes=Money+Money: name each type once, separated by +"},
		"apptypes declared":  {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Options: map[string]string{"apptypes": "Cursor"}, Model: m}), "apptypes= names Cursor, which velox-graphql declares itself"},
		"engine":             {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"engine": "graphql-go-v2"}, Model: m}), "it is given engine=graphql-go-v2"},
		"engine no package":  {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"engine": "graphql-go"}, Model: m}), "engine=graphql-go binds every type to the generated package, and the request names none"},
		"engine refuses": {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"engine": "graphql-go"}, Model: &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", ElemType: "int64"}, {Name: "name", GoName: "Name", Type: "string", ElemType: "string", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"field_name":"displayName"}`)}}},
		}}}}), "schema User, field name: engine=graphql-go: graphql.FieldName(\"displayName\") binds it to its Go field Name through @goField(name:), and gqlc does not yet honour @goField(name:)"},
		"only":     {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "types"}, Model: m}), "it is given only=types"},
		"only and": {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "x": ""}, Model: m}), "it is given only=enums, x"},
		"enums":    {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums"}, Model: m}), "the request names no generated package"},
		"render": {request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "User", PrimaryKey: []int{0},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "x", GoName: "X", Type: "other"}},
		}}}}), "User"},
		"go": {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Model: &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "Thing", PrimaryKey: []int{0},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "x", GoName: "X", Type: "string"}},
		}}}}), "schema Thing: the model gives it no label or no handles"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := respond(t, c.in)
			if len(resp.Files) != 0 || !strings.Contains(resp.Error, c.want) {
				t.Errorf("%+v, want an error with %q", resp, c.want)
			}
		})
	}
}

// TestUnfilledWarning: an M2M edge whose edge schema has a column Add cannot
// fill is written without add inputs, and velox-graphql says so, once per
// edge, for either engine, naming fields by their Go names. The edge whose
// edge schema fills every column draws no warning, and neither does an edge
// of a type with no create or update input; a create input alone is enough.
func TestUnfilledWarning(t *testing.T) {
	generate := func(m *plugin.Model, opts map[string]string) []string {
		t.Helper()
		files, warnings, err := Generate(&plugin.Request{Protocol: 2, OutPackage: "gen", OutPath: "example.com/app/gen", Options: opts, Model: m})
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatal("no files")
		}
		return warnings
	}
	want := []string{
		"User.following: no add inputs; Follow.Note has no default",
		"User.followers: no add inputs; Follow.Note has no default",
	}
	for _, opts := range []map[string]string{nil, {"engine": "graphql-go"}, {"names": "schema", "node": "on", "orderfields": "all"}} {
		if got := generate(modeltest.EdgeSchema(t), opts); !reflect.DeepEqual(got, want) {
			t.Errorf("%v: warnings %q, want %q", opts, got, want)
		}
	}
	m := modeltest.EdgeSchema(t)
	for _, e := range m.Entities {
		for _, ed := range e.Edges {
			if ed.Name == "following" {
				ed.Unfilled = append(ed.Unfilled, "id")
			}
		}
	}
	if got, want := generate(m, nil), "User.following: no add inputs; Follow.Note, Follow.ID have no default"; len(got) == 0 || got[0] != want {
		t.Errorf("warnings %q, want %q first", got, want)
	}
	for ann, want := range map[string][]string{`{"query_field": "users"}`: nil, `{"query_field": "users", "mutations": ["create"]}`: want} {
		m = modeltest.EdgeSchema(t)
		for _, e := range m.Entities {
			if e.Name == "User" {
				e.Annotations["graphql"] = json.RawMessage(ann)
			}
		}
		if got := generate(m, nil); !reflect.DeepEqual(got, want) {
			t.Errorf("User %s: warnings %q, want %q", ann, got, want)
		}
	}
}
