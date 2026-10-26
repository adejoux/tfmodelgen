package e2e

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const projectBody = `{
  "createdAt": "2026-10-26T10:00:00Z",
  "updatedAt": null,
  "id": "p-1",
  "name": "billing",
  "status": "active",
  "replicas": 3,
  "cpu": 0.5,
  "public": false,
  "count": 7,
  "apiKey": "s3cr3t",
  "tags": ["a", "b"],
  "labels": {"team": "core"},
  "owner": null,
  "members": [
    {"id": "u-1", "email": "a@example.com", "displayName": "Alice"},
    {"id": "u-2", "email": "b@example.com", "displayName": null}
  ],
  "settings": {"retentionDays": 30, "notifications": {"email": true, "webhooks": []}},
  "quotas": {"cpu": {"limit": 10, "used": 4}},
  "matrix": [[1, null], null],
  "metadata": {"env": "prod", "n": 1},
  "value": 42,
  "unknownField": "ignored"
}`

func TestProjectFromAPI(t *testing.T) {
	ctx := context.Background()
	in, err := DecodeProjectAPI([]byte(projectBody))
	if err != nil {
		t.Fatal(err)
	}
	m := ProjectModel{ID: types.StringValue("p-1"), Expand: types.BoolValue(true)}
	if diags := m.FromAPI(ctx, in); diags.HasError() {
		t.Fatal(diags)
	}

	checks := []struct {
		name string
		got  bool
	}{
		{"name", m.Name.Equal(types.StringValue("billing"))},
		{"explicit null", m.UpdatedAt.IsNull()},
		{"absent field", m.Description.IsNull()},
		{"int", m.Replicas.Equal(types.Int64Value(3))},
		{"float", m.CPU.Equal(types.Float64Value(0.5))},
		{"false bool is not null", m.Public.Equal(types.BoolValue(false))},
		{"renamed meta-argument", m.Count.Equal(types.Int64Value(7))},
		{"null object", m.Owner.IsNull()},
		{"list", len(m.Tags.Elements()) == 2},
		{"map", len(m.Labels.Elements()) == 1},
		{"list of objects", len(m.Members.Elements()) == 2},
		{"map of objects", len(m.Quotas.Elements()) == 1},
		{"list of lists", len(m.Matrix.Elements()) == 2 && m.Matrix.Elements()[1].IsNull()},
		{"free-form JSON", m.Metadata.Equal(types.StringValue(`{"env": "prod", "n": 1}`))},
		{"oneOf as JSON", m.Value.Equal(types.StringValue("42"))},
		{"absent JSON", m.Parent.IsNull()},
		{"request-only attribute untouched", m.Expand.Equal(types.BoolValue(true))},
	}
	for _, c := range checks {
		if !c.got {
			t.Errorf("%s: unexpected value", c.name)
		}
	}

	var settings ProjectSettingsModel
	if d := m.Settings.As(ctx, &settings, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatal(d)
	}
	var notif ProjectSettingsNotificationsModel
	if d := settings.Notifications.As(ctx, &notif, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatal(d)
	}
	if !settings.RetentionDays.Equal(types.Int64Value(30)) || !notif.Email.ValueBool() ||
		notif.Webhooks.IsNull() || len(notif.Webhooks.Elements()) != 0 {
		t.Fatalf("nested settings: %+v %+v", settings, notif)
	}

	var members []ProjectUserModel
	if d := m.Members.ElementsAs(ctx, &members, false); d.HasError() {
		t.Fatal(d)
	}
	if members[0].DisplayName.ValueString() != "Alice" || !members[1].DisplayName.IsNull() {
		t.Fatalf("members: %+v", members)
	}
}

// The model must fit both generated schemas: setting it in a state checks
// every attribute name and type.
func TestProjectModelMatchesSchemas(t *testing.T) {
	ctx := context.Background()
	in, err := DecodeProjectAPI([]byte(projectBody))
	if err != nil {
		t.Fatal(err)
	}
	var m ProjectModel
	m.Expand = types.BoolNull()
	if d := m.FromAPI(ctx, in); d.HasError() {
		t.Fatal(d)
	}

	dsSchema := ProjectDataSourceSchema()
	if d := dsSchema.ValidateImplementation(ctx); d.HasError() {
		t.Fatalf("data source schema: %v", d)
	}
	rsSchema := ProjectResourceSchema()
	if d := rsSchema.ValidateImplementation(ctx); d.HasError() {
		t.Fatalf("resource schema: %v", d)
	}

	states := map[string]tfsdk.State{
		"data source": {Schema: dsSchema, Raw: tftypes.NewValue(dsSchema.Type().TerraformType(ctx), nil)},
		"resource":    {Schema: rsSchema, Raw: tftypes.NewValue(rsSchema.Type().TerraformType(ctx), nil)},
	}
	for name, state := range states {
		if d := state.Set(ctx, &m); d.HasError() {
			t.Fatalf("%s: set: %v", name, d)
		}
		var back ProjectModel
		if d := state.Get(ctx, &back); d.HasError() {
			t.Fatalf("%s: get: %v", name, d)
		}
		if !back.Members.Equal(m.Members) || !back.Quotas.Equal(m.Quotas) || !back.Settings.Equal(m.Settings) {
			t.Fatalf("%s: round trip changed the model", name)
		}
	}
}

func TestProjectRequestHelpers(t *testing.T) {
	m := ProjectModel{ID: types.StringValue("a b/c"), Expand: types.BoolValue(true)}
	p, err := m.APIPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != "/projects/a%20b%2Fc" {
		t.Fatalf("path = %q", p)
	}
	if q := m.APIQuery().Encode(); q != "expand=true" {
		t.Fatalf("query = %q", q)
	}
	if _, err := (&ProjectModel{ID: types.StringUnknown()}).APIPath(); err == nil {
		t.Fatal("unknown id accepted")
	}
	if q := (&ProjectModel{}).APIQuery(); len(q) != 0 {
		t.Fatalf("null parameters in query: %v", q)
	}
}

func TestProjectsArrayResponse(t *testing.T) {
	ctx := context.Background()
	in, err := DecodeProjectsAPI([]byte(`[{"id":"p-1","name":"a"},{"id":"p-2","name":"b","owner":"u-1"}]`))
	if err != nil {
		t.Fatal(err)
	}
	m := ProjectsModel{Owner: types.StringValue("u-1"), Limit: types.Int64Value(10)}
	if d := m.FromAPI(ctx, in); d.HasError() {
		t.Fatal(d)
	}
	var items []ProjectsProjectSummaryModel
	if d := m.Items.ElementsAs(ctx, &items, false); d.HasError() {
		t.Fatal(d)
	}
	if len(items) != 2 || items[1].Owner.ValueString() != "u-1" || !items[0].Owner.IsNull() {
		t.Fatalf("items: %+v", items)
	}
	if !m.Owner.Equal(types.StringValue("u-1")) {
		t.Fatal("query parameter overwritten by the response")
	}
	if q := m.APIQuery().Encode(); q != "limit=10&owner=u-1" {
		t.Fatalf("query = %q", q)
	}

	s := ProjectsDataSourceSchema()
	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if d := state.Set(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
}

func TestDecodeError(t *testing.T) {
	if _, err := DecodeProjectAPI([]byte(`{"replicas": "three"}`)); err == nil {
		t.Fatal("type mismatch accepted")
	}
}
