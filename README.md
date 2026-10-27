# tfmodelgen

`tfmodelgen` reads an OpenAPI 3.0 or 3.1 document (JSON) and generates, for the response of one operation, a Go file for a Terraform provider built on the [Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework).

A GET operation typically serves twice:

- a **data source** reads it: its arguments are the request parameters (`id`, filters), its computed attributes come from the response;
- a **resource** calls it internally, for instance to poll `GET /projects/{id}` after a create until the project is ready, then store its `status`.

The generated file has two sides:

- **Go side**, for the provider code that calls the API:
  - `ProjectAPI` and nested structs decoding the whole response, with pointer fields so that absent and `null` values stay distinct from zero values;
  - nil-safe getters (`api.GetStatus()`, `api.GetOwner().GetEmail()`) and constants for string enums (`ProjectStatusActive`);
  - `DecodeProjectAPI(body)`, `ProjectPath(id)` and, with query parameters, `ProjectQuery{...}.Values()`.
- **Terraform side**, restricted with `-attrs` to the properties the provider exposes:
  - `ProjectModel` with the response attributes and `FromAPI(ctx, api)`;
  - data sources (`-schema datasource`, the default): `ProjectDataSourceModel`, which embeds `ProjectModel` and adds the request parameters as arguments, `ProjectDataSourceSchema()`, and `APIPath()` / `APIQuery()` built from the configuration;
  - resources (`-schema resource`): `ProjectResourceAttributes()`, the computed attributes of `ProjectModel` to merge into the schema of a resource that embeds it.

## Install

```sh
go install github.com/adejoux/tfmodelgen@latest
```

## Usage

List the GET operations of a document:

```sh
tfmodelgen -spec openapi.json
```

Generate a data source for one operation:

```sh
tfmodelgen -spec openapi.json -path '/projects/{id}' -name Project \
  -package provider -out internal/provider
# GET /projects/{id} -> internal/provider/project_model_gen.go
```

Generate the status model of a resource from the same operation:

```sh
tfmodelgen -spec openapi.json -path '/projects/{id}' -name ProjectStatus -attrs status \
  -schema resource -package provider -out internal/provider
```

Or from the provider sources:

```go
//go:generate go run github.com/adejoux/tfmodelgen -spec ../../openapi.json -path /projects/{id} -name Project -package provider
//go:generate go run github.com/adejoux/tfmodelgen -spec ../../openapi.json -path /projects/{id} -name ProjectStatus -attrs status -schema resource -package provider
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-spec` | | OpenAPI document, JSON (required) |
| `-path` | | Operation path as written in the document; without it, operations are listed |
| `-method` | `GET` | HTTP method |
| `-status` | `200`, then the first `2xx`, then `default` | Response to use |
| `-name` | from `operationId` (`getProject` → `Project`) | Go base name of the generated identifiers |
| `-attrs` | all | Root response properties exposed as Terraform attributes, comma-separated, JSON or Terraform names |
| `-schema` | `datasource` | Schemas to generate: `datasource`, `resource`, `both` or `none` |
| `-package` | `provider` | Go package |
| `-out` | `.` | Output directory, `-` for standard output |
| `-file` | `<name>_model_gen.go` | Output file name |
| `-items-attr` | `items` | Attribute holding a response that is not an object (an array, for instance) |

`-attrs` only restricts the Terraform side: the API structs always decode the whole response, so the provider can read any field in Go. Several operations can be generated into the same package, since every identifier is prefixed with `-name`.

## In a data source

```go
func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = ProjectDataSourceSchema()
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m ProjectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	path, err := m.APIPath()
	if err != nil {
		resp.Diagnostics.AddError("Invalid configuration", err.Error())
		return
	}
	body, err := d.client.Get(ctx, path, m.APIQuery()) // your API client
	if err == nil {
		var api *ProjectAPI
		if api, err = DecodeProjectAPI(body); err == nil {
			resp.Diagnostics.Append(m.FromAPI(ctx, api)...)
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the project", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
```

In the data source schema, a path parameter is a required argument and a query parameter an optional one (required when the document says so). A parameter with the same name as a response attribute shares it: `id` is both the argument and the attribute. `FromAPI` on the data source model keeps the configured arguments, as Terraform requires, even when the API normalises or omits them.

## In a resource

Embed the model and merge the attributes:

```go
type projectResourceModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	ProjectStatusModel // status, computed
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id":   schema.StringAttribute{Computed: true},
		"name": schema.StringAttribute{Required: true},
	}
	maps.Copy(attrs, ProjectStatusResourceAttributes())
	resp.Schema = schema.Schema{Attributes: attrs}
}
```

Read the status after the create call:

```go
func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := r.client.CreateProject(ctx, plan.Name.ValueString()) // your API call
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the project", err.Error())
		return
	}
	plan.ID = types.StringValue(id)

	// Poll the GET operation until the project is active.
	var api *ProjectStatusAPI
	for api.GetStatus() != ProjectStatusStatusActive {
		body, err := r.client.Get(ctx, ProjectStatusPath(id), nil)
		if err == nil {
			api, err = DecodeProjectStatusAPI(body)
		}
		if err != nil {
			resp.Diagnostics.AddError("Cannot read the project", err.Error())
			return
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Timeout", "the project did not become active")
			return
		case <-time.After(5 * time.Second):
		}
	}

	resp.Diagnostics.Append(plan.FromAPI(ctx, api)...) // promoted from ProjectStatusModel
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
```

The embedded attributes must not clash with the resource's own: if the resource already has a `name` attribute, do not list `name` in `-attrs`. Embedding needs the Plugin Framework v1.12 or later; the struct is embedded by value, never by pointer.

Nested values are `types.Object`, `types.List` and `types.Map`; read them back with the generated models:

```go
var settings ProjectSettingsModel
diags := m.Settings.As(ctx, &settings, basetypes.ObjectAsOptions{})
```

## Converting objects one by one

Every nested model, list elements included, has its own conversion
methods: `FromAPI` fills the model from one API object, `ObjectValue()`
returns it as a `types.Object`, `ObjectType()` gives its Terraform type.
Convert each element, filter or enrich it, then build the list:

```go
api, err := DecodeProjectsAPI(body) // []*ProjectsProjectSummaryAPI

var items []ProjectsProjectSummaryModel
for _, obj := range *api {
	if obj.GetOwner() == "" { // any filter, here: skip unowned projects
		continue
	}
	var item ProjectsProjectSummaryModel
	resp.Diagnostics.Append(item.FromAPI(ctx, obj)...)
	items = append(items, item)
}

list, diags := types.ListValueFrom(ctx, ProjectsProjectSummaryModel{}.ObjectType(), items)
resp.Diagnostics.Append(diags...)
m.Items = list
```

`types.SetValueFrom` and `types.MapValueFrom` work the same way. To append
`attr.Value` elements instead, use `item.ObjectValue()` and
`types.ListValue(ProjectsProjectSummaryModel{}.ObjectType(), elems)`.

## Mapping

| OpenAPI | API struct | Terraform model | Schema |
| --- | --- | --- | --- |
| `string` (any format, enum) | `*string` | `types.String` | `StringAttribute` |
| `integer` | `*int64` | `types.Int64` | `Int64Attribute` |
| `number` | `*float64` | `types.Float64` | `Float64Attribute` |
| `boolean` | `*bool` | `types.Bool` | `BoolAttribute` |
| `object` with `properties` | `*XAPI` | `types.Object` | `SingleNestedAttribute` |
| `array` | `[]T` | `types.List` | `ListAttribute` / `ListNestedAttribute` |
| `object` with `additionalProperties` only | `map[string]T` | `types.Map` | `MapAttribute` / `MapNestedAttribute` |
| free-form or ambiguous schema | `json.RawMessage` | `types.String` (JSON) | `StringAttribute` |

- Nullability: OpenAPI 3.1 `type: [T, "null"]`, `oneOf`/`anyOf` with a `null` branch, and 3.0 `nullable: true`. Every response attribute is nullable on the Terraform side, since the API may omit it; absent and `null` give a null value, never `""` or `0`.
- `$ref` (local `#/components/...` only), `allOf` of objects (merged), `oneOf`/`anyOf` with a single non-null alternative.
- Attribute names are converted to snake case (`createdAt` → `created_at`). Terraform meta-arguments get a suffix (`count` → `count_value`).
- Path and query parameters become the arguments of `<Name>Path` and the fields of `<Name>Query` (pointers when optional), and the arguments of the data source model. They are never attributes of `<Name>Model`, so the resource model does not get them. Header and cookie parameters are ignored.
- `format: password` and `writeOnly` give `Sensitive: true`; `writeOnly` properties are left out of the response. `deprecated` sets a deprecation message. Descriptions, titles and enum values go into the schema descriptions.
- Dates stay strings (RFC 3339 as returned by the API).

Everything that cannot be mapped exactly is reported as a warning on standard error and generated as a JSON-encoded string: `oneOf`/`anyOf` with several alternatives, several types, free-form objects, recursive references, `allOf` mixing non-objects.

## Limits

- Only the response is generated: no request body, no conversion from the model to the API.
- Response attributes are all `Computed`. In a resource, add plan modifiers yourself where needed (`UseStateForUnknown` for values that do not change, for instance).
- `-attrs` selects root properties; a nested object is exposed as a whole.
- JSON documents only (convert YAML with `yq -o=json` first). External `$ref` are not supported.

## Development

```sh
go test -race ./...
go test ./internal/gen -update   # regenerate the golden files in internal/e2e
```

`internal/e2e` holds the code generated from `testdata/openapi.json`. Its tests compile it against the Plugin Framework, decode sample responses, check the data source arguments, embed the status model in a resource model, and set the models into data source and resource states, which checks that models and schemas match.
