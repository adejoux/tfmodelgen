package e2e

import (
	"context"
	"maps"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// projectResourceModel is the model of a resource that does not expose the
// GET operation: it only adds the status read after a create or an update.
type projectResourceModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	ProjectStatusModel
}

func projectResourceSchema() schema.Schema {
	attrs := map[string]schema.Attribute{
		"id":   schema.StringAttribute{Computed: true},
		"name": schema.StringAttribute{Required: true},
	}
	maps.Copy(attrs, ProjectStatusResourceAttributes())
	return schema.Schema{Attributes: attrs}
}

func TestStatusEmbeddedInResourceModel(t *testing.T) {
	ctx := context.Background()
	s := projectResourceSchema()
	if d := s.ValidateImplementation(ctx); d.HasError() {
		t.Fatal(d)
	}

	// After the create call, the provider polls GET /projects/{id}.
	body := []byte(`{"id":"p-1","name":"billing","status":"active","updatedAt":"2026-10-27T09:00:00Z","members":[]}`)
	api, err := DecodeProjectStatusAPI(body)
	if err != nil {
		t.Fatal(err)
	}
	if api.GetStatus() != ProjectStatusStatusActive {
		t.Fatalf("status = %q", api.GetStatus())
	}

	plan := projectResourceModel{ID: types.StringValue("p-1"), Name: types.StringValue("billing")}
	if d := plan.FromAPI(ctx, api); d.HasError() { // promoted from ProjectStatusModel
		t.Fatal(d)
	}
	if plan.Status.ValueString() != "active" || plan.UpdatedAt.IsNull() || plan.Name.ValueString() != "billing" {
		t.Fatalf("model: %+v", plan)
	}

	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if d := state.Set(ctx, &plan); d.HasError() {
		t.Fatal(d)
	}
	var back projectResourceModel
	if d := state.Get(ctx, &back); d.HasError() {
		t.Fatal(d)
	}
	if !back.Status.Equal(plan.Status) || !back.ID.Equal(plan.ID) {
		t.Fatalf("round trip: %+v", back)
	}
	if ProjectStatusPath("p-1") != "/projects/p-1" {
		t.Fatal("path")
	}
}
