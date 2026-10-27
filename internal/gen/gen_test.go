package gen

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adejoux/tfmodelgen/internal/spec"
)

var update = flag.Bool("update", false, "rewrite the generated files of internal/e2e")

// The files of internal/e2e are the golden output: they are compiled and
// tested there against the Plugin Framework.
func TestGolden(t *testing.T) {
	doc, err := spec.Load("../../testdata/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, name, file string
		attrs            []string
		schema           SchemaKind
	}{
		{"/projects/{id}", "Project", "project_model_gen.go", nil, SchemaBoth},
		{"/projects", "Projects", "projects_model_gen.go", nil, SchemaBoth},
		{"/projects/{id}", "ProjectStatus", "project_status_model_gen.go", []string{"status", "updatedAt"}, SchemaResource},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ep, err := Build(doc, Options{Path: c.path, Name: c.name, Attrs: c.attrs})
			if err != nil {
				t.Fatal(err)
			}
			got, err := Emit(ep, EmitOptions{Package: "e2e", SpecName: "openapi.json", Schema: c.schema})
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("..", "e2e", c.file)
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("%s is out of date: run go test ./internal/gen -update", golden)
			}
		})
	}
}

func TestWarnings(t *testing.T) {
	doc, err := spec.Load("../../testdata/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	ep, err := Build(doc, Options{Path: "/projects/{id}"})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Root.Name != "Project" {
		t.Fatalf("default name = %q", ep.Root.Name)
	}
	all := strings.Join(ep.Warnings, "\n")
	for _, want := range []string{"meta-argument", "free-form object", "recursive reference", "several alternatives", "header"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing warning %q in:\n%s", want, all)
		}
	}
}

func build(t *testing.T, doc string, path string) *Endpoint {
	t.Helper()
	d, err := spec.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	ep, err := Build(d, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Emit(ep, EmitOptions{Schema: SchemaBoth}); err != nil {
		t.Fatal(err)
	}
	return ep
}

func field(t *testing.T, obj *ObjectType, tf string) *Field {
	t.Helper()
	for _, f := range obj.Fields {
		if f.TFName == tf {
			return f
		}
	}
	t.Fatalf("no attribute %q in %s", tf, obj.Name)
	return nil
}

func TestOpenAPI30Nullable(t *testing.T) {
	ep := build(t, `{"openapi":"3.0.3","paths":{"/things/{thingId}":{"get":{
		"parameters":[{"name":"thingId","in":"path","required":true,"schema":{"type":"integer"}}],
		"responses":{"2XX":{"description":"ok","content":{"application/json":{"schema":{
			"type":"object","properties":{
				"note":{"type":"string","nullable":true},
				"FromAPI":{"type":"string"},
				"size":{"enum":[1,2,3]}
			}}}}}}}}}}`, "/things/{thingId}")
	if ep.Status != "2XX" || ep.Root.Name != "Things" {
		t.Fatalf("status %q, name %q", ep.Status, ep.Root.Name)
	}
	if f := field(t, ep.Root, "note"); !f.Node.Nullable {
		t.Error("nullable not honoured")
	}
	if f := field(t, ep.Root, "from_api"); f.GoName != "FromAPIField" {
		t.Errorf("method clash not avoided: %s", f.GoName)
	}
	if f := field(t, ep.Root, "size"); f.Node.Kind != KInt || !strings.Contains(f.Desc, "`2`") {
		t.Errorf("enum: kind %v, desc %q", f.Node.Kind, f.Desc)
	}
	if len(ep.Params) != 1 || ep.Params[0].ArgName != "thingID" || ep.Params[0].Node.Kind != KInt {
		t.Errorf("path parameter: %+v", ep.Params)
	}
}

func TestErrors(t *testing.T) {
	for name, doc := range map[string]string{
		"version":  `{"openapi":"2.0","paths":{}}`,
		"not json": `openapi: 3.1.0`,
	} {
		if _, err := spec.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	d, err := spec.Parse([]byte(`{"openapi":"3.1.0","paths":{"/a":{"get":{"responses":{"204":{"description":"none"}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(d, Options{Path: "/a"}); err == nil {
		t.Error("operation without JSON response accepted")
	}
	if _, err := Build(d, Options{Path: "/missing"}); err == nil {
		t.Error("unknown path accepted")
	}
}

func TestNaming(t *testing.T) {
	for in, want := range map[string]string{
		"created_at": "CreatedAt", "userId": "UserID", "HTTPServer": "HTTPServer",
		"api-key": "APIKey", "2fa": "X2fa", "": "Field", "self-link": "SelfLink",
	} {
		if got := GoName(in); got != want {
			t.Errorf("GoName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"createdAt": "created_at", "userID": "user_id", "count": "count_value",
		"API-Key": "api_key", "9lives": "x_9lives", "déjà vu": "dj_vu",
	} {
		if got := TFName(in); got != want {
			t.Errorf("TFName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"ProjectSettings": "projectSettings", "APIKey": "apiKey", "ID": "id", "URLMap": "urlMap",
	} {
		if got := lowerFirst(in); got != want {
			t.Errorf("lowerFirst(%q) = %q, want %q", in, got, want)
		}
	}
	for path, want := range map[string]string{"/v1/projects/{id}": "Projects", "/{id}": "Response"} {
		if got := DefaultName(path, ""); got != want {
			t.Errorf("DefaultName(%q) = %q, want %q", path, got, want)
		}
	}
	if got := DefaultName("", "listProjectMembers"); got != "ProjectMembers" {
		t.Errorf("DefaultName from operationId = %q", got)
	}
}

func TestAttrs(t *testing.T) {
	doc, err := spec.Load("../../testdata/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	ep, err := Build(doc, Options{Path: "/projects/{id}", Attrs: []string{"status", "updated_at"}})
	if err != nil {
		t.Fatal(err)
	}
	var exposed []string
	for _, f := range ep.Root.Fields {
		if f.Exposed {
			exposed = append(exposed, f.TFName)
		}
	}
	if strings.Join(exposed, ",") != "updated_at,status" {
		t.Fatalf("exposed = %v", exposed)
	}
	src, err := Emit(ep, EmitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"ProjectUserModel", "rsschema", `tfsdk:"name"`} {
		if strings.Contains(string(src), unwanted) {
			t.Errorf("unexpected %q in output", unwanted)
		}
	}
	if !strings.Contains(string(src), "ProjectUserAPI struct") {
		t.Error("API structs must stay complete")
	}
	// The path parameter id is not an exposed attribute: it gets its own
	// data source argument.
	var id *Param
	for _, p := range ep.Params {
		if p.Name == "id" {
			id = p
		}
	}
	if id == nil || id.Field != nil || id.TFName != "id" || id.ModelGoName != "ID" ||
		!strings.Contains(string(src), "type ProjectDataSourceModel struct") {
		t.Errorf("data source argument id: %+v", id)
	}

	if _, err := Build(doc, Options{Path: "/projects/{id}", Attrs: []string{"state"}}); err == nil ||
		!strings.Contains(err.Error(), "response properties:") {
		t.Errorf("unknown attribute: %v", err)
	}
	if _, err := Build(doc, Options{Path: "/projects", Attrs: []string{"id"}}); err == nil {
		t.Error("-attrs accepted on an array response")
	}
}
