package e2e

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Converting each object on its own, then building the list.
func TestUnitConversionIntoList(t *testing.T) {
	ctx := context.Background()
	api, err := DecodeProjectsAPI([]byte(`[{"id":"p-1","name":"a"},{"id":"p-2","name":"b","owner":"u-1"},null]`))
	if err != nil {
		t.Fatal(err)
	}

	// With models: filter or enrich each element, then ListValueFrom.
	var models []ProjectsProjectSummaryModel
	for _, obj := range *api {
		if obj.GetID() == "p-1" {
			continue
		}
		var m ProjectsProjectSummaryModel
		if d := m.FromAPI(ctx, obj); d.HasError() {
			t.Fatal(d)
		}
		models = append(models, m)
	}
	list, d := types.ListValueFrom(ctx, ProjectsProjectSummaryModel{}.ObjectType(), models)
	if d.HasError() {
		t.Fatal(d)
	}
	if len(list.Elements()) != 2 {
		t.Fatalf("elements = %d", len(list.Elements()))
	}

	// With object values: append each element to []attr.Value.
	var elems []attr.Value
	for _, obj := range *api {
		var m ProjectsProjectSummaryModel
		m.FromAPI(ctx, obj)
		v, d := m.ObjectValue()
		if d.HasError() {
			t.Fatal(d)
		}
		elems = append(elems, v)
	}
	list2, d := types.ListValue(ProjectsProjectSummaryModel{}.ObjectType(), elems)
	if d.HasError() {
		t.Fatal(d)
	}

	// The list built by hand fits the generated model and its schema.
	m := ProjectsDataSourceModel{Owner: types.StringValue("u-1"), Limit: types.Int64Null()}
	m.Items = list2
	var back []ProjectsProjectSummaryModel
	if d := m.Items.ElementsAs(ctx, &back, false); d.HasError() {
		t.Fatal(d)
	}
	if len(back) != 3 || back[1].Owner.ValueString() != "u-1" || !back[2].ID.IsNull() {
		t.Fatalf("elements: %+v", back)
	}
}
