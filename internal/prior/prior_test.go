package prior

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the generated files of internal/e2e")

func load(t *testing.T) []Resource {
	t.Helper()
	b, err := os.ReadFile("../../testdata/provider_schema_v0.json")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Load(b)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// internal/e2e/widget_schema_v0_gen.go is the golden output: it is compiled
// and tested there through UpgradeResourceState.
func TestGolden(t *testing.T) {
	r, err := Find(load(t), "demo_widget", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Generate(r, Options{
		Package: "e2e", Source: "provider_schema_v0.json",
		Number: NumberInt64, Types: map[string]string{"ratio": NumberFloat64},
	})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("..", "e2e", FileName(DefaultName(r), r.Schema.Version))
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
		t.Fatalf("%s is out of date: run go test ./internal/prior -update", golden)
	}
}

func TestLoadAndFind(t *testing.T) {
	rs := load(t)
	if len(rs) != 2 || rs[0].Type != "demo_other" || rs[0].Schema.Version != 2 {
		t.Fatalf("resources: %+v", rs)
	}
	if DefaultName(rs[1]) != "Widget" {
		t.Fatalf("default name = %q", DefaultName(rs[1]))
	}
	if _, err := Find(rs, "demo_widget", "demo"); err != nil {
		t.Fatalf("short provider name: %v", err)
	}
	if _, err := Find(rs, "demo_widget", "other"); err == nil {
		t.Fatal("wrong provider accepted")
	}
	if _, err := Find(rs, "demo_nope", ""); err == nil {
		t.Fatal("missing resource accepted")
	}

	single, err := Load([]byte(`{"version": 3, "block": {"attributes": {"n": {"type": "number", "optional": true}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Find(single, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(r, Options{}); err == nil {
		t.Fatal("a single schema needs a name")
	}
	src, err := Generate(r, Options{Name: "Thing"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"func ThingSchemaV3()", "Version: 3", "N types.Number", "func ThingStateUpgraderV3[T any]"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
}

func TestErrors(t *testing.T) {
	r, _ := Find(load(t), "demo_widget", "")
	if _, err := Generate(r, Options{Types: map[string]string{"settings.nope": NumberInt64}}); err == nil || !strings.Contains(err.Error(), "settings.nope") {
		t.Fatalf("unused override: %v", err)
	}
	if _, err := Generate(r, Options{Number: "int32"}); err == nil {
		t.Fatal("bad number type accepted")
	}
	for _, doc := range []string{
		`{"version": 0, "block": {"attributes": {"t": {"type": ["tuple", ["string"]], "optional": true}}}}`,
		`{"version": 0, "block": {"attributes": {"t": {"type": "blob", "optional": true}}}}`,
		`{"version": 0, "block": {"block_types": {"b": {"nesting_mode": "map", "block": {}}}}}`,
		`{"nope": 1}`,
	} {
		rs, err := Load([]byte(doc))
		if err == nil {
			_, err = Generate(rs[0], Options{Name: "X"})
		}
		if err == nil {
			t.Errorf("%s: no error", doc)
		}
	}
}
