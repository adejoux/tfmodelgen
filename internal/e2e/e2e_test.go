package e2e

import (
	"context"
	"testing"

	rsschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
	var m ProjectModel
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

	// Resource side: ProjectModel against the computed attributes.
	rsSchema := rsschema.Schema{Attributes: ProjectResourceAttributes()}
	if d := rsSchema.ValidateImplementation(ctx); d.HasError() {
		t.Fatalf("resource schema: %v", d)
	}
	var m ProjectModel
	if d := m.FromAPI(ctx, in); d.HasError() {
		t.Fatal(d)
	}
	rsState := tfsdk.State{Schema: rsSchema, Raw: tftypes.NewValue(rsSchema.Type().TerraformType(ctx), nil)}
	if d := rsState.Set(ctx, &m); d.HasError() {
		t.Fatalf("resource: set: %v", d)
	}

	// Data source side: ProjectDataSourceModel against the full schema.
	dsSchema := ProjectDataSourceSchema()
	if d := dsSchema.ValidateImplementation(ctx); d.HasError() {
		t.Fatalf("data source schema: %v", d)
	}
	ds := ProjectDataSourceModel{Expand: types.BoolValue(true)}
	ds.ID = types.StringValue("p-1")
	if d := ds.FromAPI(ctx, in); d.HasError() {
		t.Fatal(d)
	}
	dsState := tfsdk.State{Schema: dsSchema, Raw: tftypes.NewValue(dsSchema.Type().TerraformType(ctx), nil)}
	if d := dsState.Set(ctx, &ds); d.HasError() {
		t.Fatalf("data source: set: %v", d)
	}
	var back ProjectDataSourceModel
	if d := dsState.Get(ctx, &back); d.HasError() {
		t.Fatalf("data source: get: %v", d)
	}
	if !back.Members.Equal(ds.Members) || !back.Quotas.Equal(ds.Quotas) || !back.Expand.Equal(ds.Expand) {
		t.Fatal("data source: round trip changed the model")
	}
}

func TestDataSourceArguments(t *testing.T) {
	ctx := context.Background()
	s := ProjectDataSourceSchema()
	if a := s.Attributes["id"]; !a.IsRequired() || a.IsComputed() {
		t.Fatal("id must be a required argument")
	}
	if a := s.Attributes["expand"]; !a.IsOptional() || a.IsComputed() {
		t.Fatal("expand must be an optional argument")
	}
	if a := s.Attributes["name"]; !a.IsComputed() || a.IsOptional() || a.IsRequired() {
		t.Fatal("name must be computed")
	}

	var ds ProjectDataSourceModel
	ds.ID = types.StringValue("p 1")
	ds.Expand = types.BoolValue(false)
	if p, err := ds.APIPath(); err != nil || p != "/projects/p%201" {
		t.Fatalf("path = %q, %v", p, err)
	}
	if q := ds.APIQuery().Encode(); q != "expand=false" {
		t.Fatalf("query = %q", q)
	}

	// The argument keeps its configured value even when the response omits
	// or changes it.
	in, err := DecodeProjectAPI([]byte(`{"id":"normalised","name":"billing"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d := ds.FromAPI(ctx, in); d.HasError() {
		t.Fatal(d)
	}
	if ds.ID.ValueString() != "p 1" || ds.Name.ValueString() != "billing" {
		t.Fatalf("model: %+v", ds)
	}

	var empty ProjectDataSourceModel
	if _, err := empty.APIPath(); err == nil {
		t.Fatal("null id accepted")
	}
}

func TestRequestHelpers(t *testing.T) {
	if p := ProjectPath("a b/c"); p != "/projects/a%20b%2Fc" {
		t.Fatalf("path = %q", p)
	}
	expand := true
	if q := (ProjectQuery{Expand: &expand}).Values().Encode(); q != "expand=true" {
		t.Fatalf("query = %q", q)
	}
	if q := (ProjectQuery{}).Values(); len(q) != 0 {
		t.Fatalf("unset parameters sent: %v", q)
	}
	if p := ProjectsPath(); p != "/projects" {
		t.Fatalf("path = %q", p)
	}
	limit := int64(10)
	if q := (ProjectsQuery{Limit: &limit, Owner: "u-1"}).Values().Encode(); q != "limit=10&owner=u-1" {
		t.Fatalf("query = %q", q)
	}
}

func TestGettersAndEnums(t *testing.T) {
	in, err := DecodeProjectAPI([]byte(projectBody))
	if err != nil {
		t.Fatal(err)
	}
	if in.GetStatus() != ProjectStatusActive || in.GetReplicas() != 3 || in.GetDescription() != "" {
		t.Fatalf("getters: %q %d %q", in.GetStatus(), in.GetReplicas(), in.GetDescription())
	}
	if in.GetOwner().GetEmail() != "" || in.GetSettings().GetNotifications().GetEmail() != true {
		t.Fatal("nested getters")
	}
	var none *ProjectAPI
	if none.GetStatus() != "" || none.GetTags() != nil {
		t.Fatal("getters on a nil response")
	}
}

func TestProjectsArrayResponse(t *testing.T) {
	ctx := context.Background()
	in, err := DecodeProjectsAPI([]byte(`[{"id":"p-1","name":"a"},{"id":"p-2","name":"b","owner":"u-1"}]`))
	if err != nil {
		t.Fatal(err)
	}
	m := ProjectsDataSourceModel{Owner: types.StringValue("u-1"), Limit: types.Int64Null()}
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

	if q := m.APIQuery().Encode(); q != "owner=u-1" {
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
