# tfmodelgen

`tfmodelgen` reads an OpenAPI 3.0 or 3.1 document (JSON) and generates, for the response of one operation, a Go file for an existing Terraform provider built on the [Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework):

- **API structs** decoding the JSON response (`ProjectAPI`, with pointers so that absent and `null` values stay distinct from zero values);
- **Terraform models** with `tfsdk` tags (`ProjectModel`, `ProjectSettingsModel`…) and the `AttrTypes()` of nested objects;
- **schemas** for a data source and/or a resource (`ProjectDataSourceSchema()`, `ProjectDataSourceAttributes()` to embed the attributes in an existing schema);
- **helpers**: `DecodeProjectAPI(body)`, `(*ProjectModel).FromAPI(ctx, api)`, and `APIPath()` / `APIQuery()` built from the path and query parameters.

## Install

```sh
go install github.com/adejoux/tfmodelgen@latest
```

## Usage

List the GET operations of a document:

```sh
tfmodelgen -spec openapi.json
```

Generate the file for one operation:

```sh
tfmodelgen -spec openapi.json -path '/projects/{id}' -name Project \
  -package provider -out internal/provider
# GET /projects/{id} -> internal/provider/project_model_gen.go
```

Or from the provider sources:

```go
//go:generate go run github.com/adejoux/tfmodelgen -spec ../../openapi.json -path /projects/{id} -name Project -package provider
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-spec` | | OpenAPI document, JSON (required) |
| `-path` | | Operation path as written in the document; without it, operations are listed |
| `-method` | `GET` | HTTP method |
| `-status` | `200`, then the first `2xx`, then `default` | Response to use |
| `-name` | from `operationId` (`getProject` → `Project`) | Go base name of the generated types |
| `-package` | `provider` | Go package |
| `-out` | `.` | Output directory, `-` for standard output |
| `-file` | `<name>_model_gen.go` | Output file name |
| `-schema` | `datasource` | `datasource`, `resource`, `both` or `none` |
| `-items-attr` | `items` | Attribute holding a response that is not an object (an array, for instance) |

Several operations can be generated into the same package: every generated identifier is prefixed with `-name`.

## In a data source

```go
func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = ProjectDataSourceSchema()
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m ProjectModel
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
	if err != nil {
		resp.Diagnostics.AddError("API error", err.Error())
		return
	}
	api, err := DecodeProjectAPI(body)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API response", err.Error())
		return
	}

	resp.Diagnostics.Append(m.FromAPI(ctx, api)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
```

Nested values are `types.Object`, `types.List` and `types.Map`; read them back with the generated models:

```go
var settings ProjectSettingsModel
diags := m.Settings.As(ctx, &settings, basetypes.ObjectAsOptions{})

var members []ProjectUserModel
diags = m.Members.ElementsAs(ctx, &members, false)
```

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
- Path parameters become `Required` attributes, query parameters `Optional` (`Required` when the document says so). A parameter with the same name as a response property shares its attribute. Header and cookie parameters are ignored.
- `format: password` and `writeOnly` give `Sensitive: true`; `writeOnly` properties are left out of the response. `deprecated` sets a deprecation message. Descriptions, titles and enum values go into the schema descriptions.
- Dates stay strings (RFC 3339 as returned by the API).

Everything that cannot be mapped exactly is reported as a warning on standard error and generated as a JSON-encoded string: `oneOf`/`anyOf` with several alternatives, several types, free-form objects, recursive references, `allOf` mixing non-objects.

## Limits

- Only the response is generated: no request body, no conversion from the model to the API.
- In a resource schema every response attribute is `Computed`. Add plan modifiers (`UseStateForUnknown`…) and the configurable attributes yourself.
- JSON documents only (convert YAML with `yq -o=json` first). External `$ref` are not supported.

## Development

```sh
go test -race ./...
go test ./internal/gen -update   # regenerate the golden files in internal/e2e
```

`internal/e2e` holds the code generated from `testdata/openapi.json`. Its tests compile it against the Plugin Framework, decode sample responses, and set the models into data source and resource states, which checks that models and schemas match.
