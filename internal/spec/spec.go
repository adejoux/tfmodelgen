// Package spec reads the parts of an OpenAPI 3.x document (JSON) needed to
// describe the response of an operation: paths, operations, parameters,
// responses and schemas, with local $ref resolution.
//
// Property order is preserved so that generated code follows the document.
package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Document is an OpenAPI document.
type Document struct {
	OpenAPI    string               `json:"openapi"`
	Paths      map[string]*PathItem `json:"paths"`
	Components Components           `json:"components"`
}

// Components holds the reusable objects referenced with $ref.
type Components struct {
	Schemas    map[string]*Schema    `json:"schemas"`
	Parameters map[string]*Parameter `json:"parameters"`
	Responses  map[string]*Response  `json:"responses"`
}

// PathItem describes the operations available on a path.
type PathItem struct {
	Parameters []*Parameter `json:"parameters"`
	Get        *Operation   `json:"get"`
	Put        *Operation   `json:"put"`
	Post       *Operation   `json:"post"`
	Delete     *Operation   `json:"delete"`
	Patch      *Operation   `json:"patch"`
	Head       *Operation   `json:"head"`
	Options    *Operation   `json:"options"`
}

// Operation returns the operation for an HTTP method, or nil.
func (p *PathItem) Operation(method string) *Operation {
	switch strings.ToUpper(method) {
	case "GET":
		return p.Get
	case "PUT":
		return p.Put
	case "POST":
		return p.Post
	case "DELETE":
		return p.Delete
	case "PATCH":
		return p.Patch
	case "HEAD":
		return p.Head
	case "OPTIONS":
		return p.Options
	}
	return nil
}

// Operation is an API operation.
type Operation struct {
	OperationID string               `json:"operationId"`
	Summary     string               `json:"summary"`
	Description string               `json:"description"`
	Parameters  []*Parameter         `json:"parameters"`
	Responses   map[string]*Response `json:"responses"`
	Deprecated  bool                 `json:"deprecated"`
}

// Parameter is an operation parameter.
type Parameter struct {
	Ref         string  `json:"$ref"`
	Name        string  `json:"name"`
	In          string  `json:"in"`
	Description string  `json:"description"`
	Required    bool    `json:"required"`
	Deprecated  bool    `json:"deprecated"`
	Schema      *Schema `json:"schema"`
}

// Response is an operation response.
type Response struct {
	Ref         string                `json:"$ref"`
	Description string                `json:"description"`
	Content     map[string]*MediaType `json:"content"`
}

// MediaType is the content of a response for one media type.
type MediaType struct {
	Schema *Schema `json:"schema"`
}

// Schema is the subset of JSON Schema / OpenAPI schema used by the generator.
type Schema struct {
	Ref                  string                `json:"$ref"`
	Type                 Types                 `json:"type"`
	Format               string                `json:"format"`
	Title                string                `json:"title"`
	Description          string                `json:"description"`
	Nullable             bool                  `json:"nullable"` // OpenAPI 3.0
	Deprecated           bool                  `json:"deprecated"`
	ReadOnly             bool                  `json:"readOnly"`
	WriteOnly            bool                  `json:"writeOnly"`
	Properties           *Properties           `json:"properties"`
	Required             []string              `json:"required"`
	Items                *Schema               `json:"items"`
	AdditionalProperties *AdditionalProperties `json:"additionalProperties"`
	AllOf                []*Schema             `json:"allOf"`
	OneOf                []*Schema             `json:"oneOf"`
	AnyOf                []*Schema             `json:"anyOf"`
	Enum                 []any                 `json:"enum"`

	// Never is set for the boolean schema false.
	Never bool `json:"-"`
}

// UnmarshalJSON accepts boolean schemas (true: anything, false: nothing).
func (s *Schema) UnmarshalJSON(b []byte) error {
	switch string(bytes.TrimSpace(b)) {
	case "true":
		*s = Schema{}
		return nil
	case "false":
		*s = Schema{Never: true}
		return nil
	}
	type plain Schema
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*s = Schema(p)
	return nil
}

// Types is the schema type keyword: a string, or an array in OpenAPI 3.1.
type Types []string

// UnmarshalJSON accepts a string or an array of strings.
func (t *Types) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*t = Types{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("type must be a string or an array of strings")
	}
	*t = many
	return nil
}

// Properties holds object properties in document order.
type Properties struct {
	Names   []string
	Schemas map[string]*Schema
}

// UnmarshalJSON decodes the properties object, keeping key order.
func (p *Properties) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("properties must be an object")
	}
	p.Names = nil
	p.Schemas = map[string]*Schema{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name, ok := tok.(string)
		if !ok {
			return fmt.Errorf("invalid property name %v", tok)
		}
		var s Schema
		if err := dec.Decode(&s); err != nil {
			return fmt.Errorf("property %q: %w", name, err)
		}
		if _, dup := p.Schemas[name]; !dup {
			p.Names = append(p.Names, name)
		}
		p.Schemas[name] = &s
	}
	_, err = dec.Token()
	return err
}

// Len returns the number of properties.
func (p *Properties) Len() int {
	if p == nil {
		return 0
	}
	return len(p.Names)
}

// AdditionalProperties is a boolean or a schema.
type AdditionalProperties struct {
	Allowed bool
	Schema  *Schema
}

// UnmarshalJSON accepts a boolean or a schema.
func (a *AdditionalProperties) UnmarshalJSON(b []byte) error {
	var allowed bool
	if err := json.Unmarshal(b, &allowed); err == nil {
		*a = AdditionalProperties{Allowed: allowed}
		return nil
	}
	var s Schema
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*a = AdditionalProperties{Allowed: true, Schema: &s}
	return nil
}

// Load reads an OpenAPI 3.x document in JSON.
func Load(path string) (*Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes an OpenAPI 3.x document in JSON.
func Parse(b []byte) (*Document, error) {
	var d Document
	if err := json.Unmarshal(b, &d); err != nil {
		if t := bytes.TrimSpace(b); len(t) > 0 && t[0] != '{' {
			return nil, fmt.Errorf("the document is not JSON (YAML is not supported): %w", err)
		}
		return nil, fmt.Errorf("decoding OpenAPI document: %w", err)
	}
	if !strings.HasPrefix(d.OpenAPI, "3.") {
		return nil, fmt.Errorf("unsupported OpenAPI version %q (3.0 or 3.1 expected)", d.OpenAPI)
	}
	return &d, nil
}

// Endpoint is an operation with its path item.
type Endpoint struct {
	Path      string
	Method    string
	Item      *PathItem
	Operation *Operation
}

// Operations lists the operations of a method, sorted by path.
func (d *Document) Operations(method string) []Endpoint {
	var out []Endpoint
	for path, item := range d.Paths {
		if item == nil {
			continue
		}
		if op := item.Operation(method); op != nil {
			out = append(out, Endpoint{Path: path, Method: strings.ToUpper(method), Item: item, Operation: op})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Endpoint returns the operation of method on path.
func (d *Document) Endpoint(path, method string) (Endpoint, error) {
	item, ok := d.Paths[path]
	if !ok || item == nil {
		return Endpoint{}, fmt.Errorf("path %q not found in the document", path)
	}
	op := item.Operation(method)
	if op == nil {
		return Endpoint{}, fmt.Errorf("no %s operation on %q", strings.ToUpper(method), path)
	}
	return Endpoint{Path: path, Method: strings.ToUpper(method), Item: item, Operation: op}, nil
}

const (
	schemasPrefix    = "#/components/schemas/"
	parametersPrefix = "#/components/parameters/"
	responsesPrefix  = "#/components/responses/"
	maxRefDepth      = 32
)

// RefName returns the component name of a local schema reference.
func RefName(ref string) (string, error) {
	if !strings.HasPrefix(ref, schemasPrefix) {
		return "", fmt.Errorf("unsupported $ref %q: only %s... references are supported", ref, schemasPrefix)
	}
	return unescape(strings.TrimPrefix(ref, schemasPrefix)), nil
}

// Schema returns the component schema of a reference.
func (d *Document) Schema(ref string) (*Schema, error) {
	name, err := RefName(ref)
	if err != nil {
		return nil, err
	}
	s, ok := d.Components.Schemas[name]
	if !ok || s == nil {
		return nil, fmt.Errorf("$ref %q: schema %q not found", ref, name)
	}
	return s, nil
}

// Deref follows a chain of schema references.
func (d *Document) Deref(s *Schema) (*Schema, error) {
	for i := 0; s != nil && s.Ref != ""; i++ {
		if i >= maxRefDepth {
			return nil, fmt.Errorf("$ref chain too deep at %q", s.Ref)
		}
		next, err := d.Schema(s.Ref)
		if err != nil {
			return nil, err
		}
		s = next
	}
	return s, nil
}

// Parameter resolves a parameter reference.
func (d *Document) Parameter(p *Parameter) (*Parameter, error) {
	for i := 0; p != nil && p.Ref != ""; i++ {
		if i >= maxRefDepth || !strings.HasPrefix(p.Ref, parametersPrefix) {
			return nil, fmt.Errorf("unsupported parameter $ref %q", p.Ref)
		}
		name := unescape(strings.TrimPrefix(p.Ref, parametersPrefix))
		next, ok := d.Components.Parameters[name]
		if !ok {
			return nil, fmt.Errorf("parameter $ref %q not found", p.Ref)
		}
		p = next
	}
	return p, nil
}

// Response resolves a response reference.
func (d *Document) Response(r *Response) (*Response, error) {
	for i := 0; r != nil && r.Ref != ""; i++ {
		if i >= maxRefDepth || !strings.HasPrefix(r.Ref, responsesPrefix) {
			return nil, fmt.Errorf("unsupported response $ref %q", r.Ref)
		}
		name := unescape(strings.TrimPrefix(r.Ref, responsesPrefix))
		next, ok := d.Components.Responses[name]
		if !ok {
			return nil, fmt.Errorf("response $ref %q not found", r.Ref)
		}
		r = next
	}
	return r, nil
}

// unescape decodes a JSON pointer token.
func unescape(s string) string {
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(s)
}
