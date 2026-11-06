package e2e

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rsschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// The current schema of demo_widget, version 1: size was renamed capacity,
// settings got timeout_seconds, the rule blocks became a rules attribute
// with target.kind flattened, description is new, the other attributes were
// dropped.
var settingsV1 = map[string]attr.Type{"mode": types.StringType, "max_items": types.Int64Type, "timeout_seconds": types.Int64Type}
var ruleV1 = map[string]attr.Type{"priority": types.Int64Type, "action": types.StringType, "target_kind": types.StringType}

type widgetModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Capacity    types.Int64  `tfsdk:"capacity"`
	Description types.String `tfsdk:"description"`
	Settings    types.Object `tfsdk:"settings"`
	Rules       types.List   `tfsdk:"rules"`
}

type widgetResource struct {
	prior *WidgetModelV0 // captured by the converter
}

func (r *widgetResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "demo_widget"
}

func (r *widgetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = rsschema.Schema{
		Version: 1,
		Attributes: map[string]rsschema.Attribute{
			"id":          rsschema.StringAttribute{Computed: true},
			"name":        rsschema.StringAttribute{Required: true},
			"capacity":    rsschema.Int64Attribute{Optional: true},
			"description": rsschema.StringAttribute{Optional: true},
			"settings": rsschema.SingleNestedAttribute{Optional: true, Attributes: map[string]rsschema.Attribute{
				"mode":            rsschema.StringAttribute{Optional: true},
				"max_items":       rsschema.Int64Attribute{Optional: true},
				"timeout_seconds": rsschema.Int64Attribute{Optional: true},
			}},
			"rules": rsschema.ListNestedAttribute{Optional: true, NestedObject: rsschema.NestedAttributeObject{Attributes: map[string]rsschema.Attribute{
				"priority":    rsschema.Int64Attribute{Required: true},
				"action":      rsschema.StringAttribute{Optional: true},
				"target_kind": rsschema.StringAttribute{Optional: true},
			}}},
		},
	}
}

func (r *widgetResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: WidgetStateUpgraderV0(func(ctx context.Context, prior WidgetModelV0) (widgetModel, diag.Diagnostics) {
			r.prior = &prior
			var diags diag.Diagnostics
			m := widgetModel{
				ID:          prior.ID,
				Name:        prior.Name,
				Capacity:    prior.Size,
				Description: types.StringNull(),
				Settings:    types.ObjectNull(settingsV1),
			}
			if s := prior.Settings; s != nil {
				var d diag.Diagnostics
				m.Settings, d = types.ObjectValue(settingsV1, map[string]attr.Value{
					"mode": s.Mode, "max_items": s.MaxItems, "timeout_seconds": types.Int64Null(),
				})
				diags.Append(d...)
			}
			var rules []attr.Value
			for _, rule := range prior.Rule {
				kind := types.StringNull()
				if rule.Target != nil {
					kind = rule.Target.Kind
				}
				v, d := types.ObjectValue(ruleV1, map[string]attr.Value{"priority": rule.Priority, "action": rule.Action, "target_kind": kind})
				diags.Append(d...)
				rules = append(rules, v)
			}
			m.Rules = types.ListNull(types.ObjectType{AttrTypes: ruleV1})
			if prior.Rule != nil {
				var d diag.Diagnostics
				m.Rules, d = types.ListValue(types.ObjectType{AttrTypes: ruleV1}, rules)
				diags.Append(d...)
			}
			return m, diags
		}),
	}
}

func (r *widgetResource) Create(context.Context, resource.CreateRequest, *resource.CreateResponse) {}
func (r *widgetResource) Read(context.Context, resource.ReadRequest, *resource.ReadResponse)       {}
func (r *widgetResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}
func (r *widgetResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

type demoProvider struct{ widget *widgetResource }

func (p *demoProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "demo"
}
func (p *demoProvider) Schema(context.Context, provider.SchemaRequest, *provider.SchemaResponse) {}
func (p *demoProvider) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {
}
func (p *demoProvider) DataSources(context.Context) []func() datasource.DataSource { return nil }
func (p *demoProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{func() resource.Resource { return p.widget }}
}

// A state written by the release using schema version 0, as stored in a
// .tfstate file.
const widgetStateV0 = `{
  "id": "w-1",
  "name": "front",
  "enabled": true,
  "size": 3,
  "ratio": 0.75,
  "labels": {"team": "core"},
  "tags": ["a", "b"],
  "ports": [80, 443],
  "password": "s3cr3t",
  "token": null,
  "extra": {"value": "x", "type": "string"},
  "endpoint": {"host": "example.com", "port": 8443},
  "routes": [{"cidr": "10.0.0.0/8", "metric": 10}],
  "settings": {"mode": "fast", "max_items": 20},
  "members": [{"email": "a@example.com", "role": null}],
  "rule": [
    {"priority": 1, "action": "allow", "target": {"kind": "host"}},
    {"priority": 2, "action": null, "target": null}
  ],
  "timeouts": null
}`

func upgrade(t *testing.T, raw string) (*widgetResource, widgetModel) {
	t.Helper()
	ctx := context.Background()
	w := &widgetResource{}
	server, err := providerserver.NewProtocol6WithError(&demoProvider{widget: w})()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
		TypeName: "demo_widget",
		Version:  0,
		RawState: &tfprotov6.RawState{JSON: []byte(raw)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		t.Errorf("%s: %s: %s", d.Severity, d.Summary, d.Detail)
	}
	if t.Failed() {
		t.FailNow()
	}

	var current rsschema.Schema
	var sresp resource.SchemaResponse
	w.Schema(ctx, resource.SchemaRequest{}, &sresp)
	current = sresp.Schema
	val, err := resp.UpgradedState.Unmarshal(current.Type().TerraformType(ctx))
	if err != nil {
		t.Fatal(err)
	}
	var m widgetModel
	if d := (tfsdk.State{Schema: current, Raw: val}).Get(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	return w, m
}

func TestUpgradeFromV0(t *testing.T) {
	ctx := context.Background()
	if d := WidgetSchemaV0().ValidateImplementation(ctx); d.HasError() {
		t.Fatal(d)
	}
	w, m := upgrade(t, widgetStateV0)

	// The prior state is fully decoded.
	p := w.prior
	if p.Enabled.ValueBool() != true || p.Size.ValueInt64() != 3 || p.Ratio.ValueFloat64() != 0.75 {
		t.Errorf("primitives: %+v", p)
	}
	if p.Labels["team"].ValueString() != "core" || len(p.Tags) != 2 || p.Ports[1].ValueInt64() != 443 {
		t.Errorf("collections: %v %v %v", p.Labels, p.Tags, p.Ports)
	}
	if !p.Token.IsNull() || p.Password.ValueString() != "s3cr3t" {
		t.Errorf("token %v, password %v", p.Token, p.Password)
	}
	if s, ok := p.Extra.UnderlyingValue().(types.String); !ok || s.ValueString() != "x" {
		t.Errorf("extra: %v", p.Extra)
	}
	if p.Endpoint == nil || p.Endpoint.Host.ValueString() != "example.com" || p.Endpoint.Port.ValueInt64() != 8443 {
		t.Errorf("endpoint: %+v", p.Endpoint)
	}
	if len(p.Routes) != 1 || p.Routes[0].CIDR.ValueString() != "10.0.0.0/8" {
		t.Errorf("routes: %+v", p.Routes)
	}
	if len(p.Members) != 1 || p.Members[0].Email.ValueString() != "a@example.com" || !p.Members[0].Role.IsNull() {
		t.Errorf("members: %+v", p.Members)
	}
	if len(p.Rule) != 2 || p.Rule[0].Target.Kind.ValueString() != "host" || p.Rule[1].Target != nil || !p.Rule[1].Action.IsNull() {
		t.Errorf("rule: %+v", p.Rule)
	}
	if p.Timeouts != nil {
		t.Errorf("timeouts: %+v", p.Timeouts)
	}

	// The upgraded state follows the current schema.
	if m.ID.ValueString() != "w-1" || m.Capacity.ValueInt64() != 3 || !m.Description.IsNull() {
		t.Errorf("model: %+v", m)
	}
	settings := m.Settings.Attributes()
	if settings["mode"].(types.String).ValueString() != "fast" || !settings["timeout_seconds"].IsNull() {
		t.Errorf("settings: %v", settings)
	}
	rules := m.Rules.Elements()
	if len(rules) != 2 || rules[0].(types.Object).Attributes()["target_kind"].(types.String).ValueString() != "host" ||
		!rules[1].(types.Object).Attributes()["target_kind"].IsNull() {
		t.Errorf("rules: %v", rules)
	}
}

func TestUpgradeFromV0Nulls(t *testing.T) {
	_, m := upgrade(t, `{"id": "w-2", "name": "back", "rule": []}`)
	if !m.Capacity.IsNull() || !m.Settings.IsNull() || m.Rules.IsNull() || len(m.Rules.Elements()) != 0 {
		t.Errorf("model: %+v", m)
	}
}
