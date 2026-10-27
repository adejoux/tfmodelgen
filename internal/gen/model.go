// Package gen turns the response of an OpenAPI operation into Terraform
// Plugin Framework models, schemas and conversion helpers.
package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/adejoux/tfmodelgen/internal/spec"
)

// Kind is the kind of a value.
type Kind int

const (
	KString Kind = iota
	KInt
	KFloat
	KBool
	KJSON // free-form value, kept as a JSON-encoded string
	KObject
	KList
	KMap
)

// Node describes a value of the response.
type Node struct {
	Kind     Kind
	Nullable bool
	Elem     *Node       // KList, KMap
	Object   *ObjectType // KObject
	Enum     []string
}

// ObjectType is an object of the response, generated as an API struct and a
// Terraform model.
type ObjectType struct {
	Name   string // Go base name, e.g. "ProjectSettings"
	Desc   string
	Fields []*Field
}

// Field is an object property of the response.
type Field struct {
	JSONName   string
	GoName     string
	TFName     string
	Node       *Node
	Desc       string
	Sensitive  bool
	Deprecated bool
	// Exposed tells whether the field is a Terraform attribute. Every field
	// is decoded in the API structs; only exposed root fields (see
	// Options.Attrs) and the objects they reach get a model and a schema.
	Exposed bool
}

// Param is a path or query parameter of the operation. Parameters are not
// Terraform attributes: they feed the generated request helpers.
type Param struct {
	Name       string
	In         string // "path" or "query"
	Required   bool
	Node       *Node // scalar
	GoName     string
	ArgName    string // Go argument name in the path function
	Desc       string
	Deprecated bool
}

// Endpoint is the generator input for one operation.
type Endpoint struct {
	Path        string
	Method      string
	Status      string
	OperationID string
	Root        *ObjectType
	Wrapped     bool // the response is not an object: Root has one synthetic field
	Objects     []*ObjectType
	Params      []*Param
	Warnings    []string
}

// Options configures Build.
type Options struct {
	Path   string
	Method string // default GET
	Status string // default: 200, then the first 2xx, then default
	Name   string // Go base name; default derived from operationId or path
	// ItemsAttr names the attribute holding a response that is not an object
	// (an array, for instance). Default "items".
	ItemsAttr string
	// Attrs lists the root response properties exposed as Terraform
	// attributes, by JSON or Terraform name. Empty means all of them.
	Attrs []string
}

// Build reads the operation and its response schema.
func Build(doc *spec.Document, o Options) (*Endpoint, error) {
	if o.Method == "" {
		o.Method = "GET"
	}
	if o.ItemsAttr == "" {
		o.ItemsAttr = "items"
	}
	ep, err := doc.Endpoint(o.Path, o.Method)
	if err != nil {
		return nil, err
	}
	base := o.Name
	if base == "" {
		base = DefaultName(ep.Path, ep.Operation.OperationID)
	}
	base = GoName(base)

	status, schema, err := responseSchema(doc, ep.Operation, o.Status)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", ep.Method, ep.Path, err)
	}

	b := &builder{
		doc:      doc,
		base:     base,
		names:    map[string]bool{},
		byRef:    map[string]*ObjectType{},
		refNames: map[string]string{},
		stack:    map[string]bool{},
	}
	out := &Endpoint{
		Path:        ep.Path,
		Method:      ep.Method,
		Status:      status,
		OperationID: ep.Operation.OperationID,
	}

	if schema.Ref != "" {
		b.refNames[schema.Ref] = base // the root type is named after -name
	}
	rootNode, err := b.node(schema, base, "$")
	if err != nil {
		return nil, err
	}
	if rootNode.Kind == KObject {
		out.Root = rootNode.Object
	} else {
		out.Wrapped = true
		out.Root = &ObjectType{Name: b.uniqueType(base)}
		out.Root.Fields = []*Field{{
			JSONName: "",
			GoName:   GoName(o.ItemsAttr),
			TFName:   TFName(o.ItemsAttr),
			Node:     rootNode,
			Desc:     describe(schema, rootNode),
			Exposed:  true,
		}}
		b.objects = append(b.objects, out.Root)
	}
	out.Root.Desc = firstNonEmpty(ep.Operation.Summary, ep.Operation.Description, out.Root.Desc)

	if err := b.params(out, ep); err != nil {
		return nil, err
	}
	b.checkFieldNames(out.Root, true)
	if err := selectAttrs(out, o.Attrs); err != nil {
		return nil, err
	}

	// Root first, then nested objects in discovery order.
	out.Objects = append(out.Objects, out.Root)
	for _, obj := range b.objects {
		if obj != out.Root {
			out.Objects = append(out.Objects, obj)
		}
	}
	out.Warnings = b.warnings
	return out, nil
}

// DefaultName derives a Go base name from the operationId ("getProject" ->
// "Project") or, failing that, from the last static path segment.
func DefaultName(path, operationID string) string {
	if operationID != "" {
		ws := words(operationID)
		if len(ws) > 1 {
			switch strings.ToLower(ws[0]) {
			case "get", "list", "read", "fetch", "find", "retrieve", "describe", "show":
				ws = ws[1:]
			}
		}
		return GoName(strings.Join(ws, "_"))
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if s := segs[i]; s != "" && !strings.HasPrefix(s, "{") {
			return GoName(s)
		}
	}
	return "Response"
}

// responseSchema picks the JSON schema of the success response.
func responseSchema(doc *spec.Document, op *spec.Operation, status string) (string, *spec.Schema, error) {
	var codes []string
	if status != "" {
		codes = []string{status, strings.ToUpper(status)}
	} else {
		codes = append(codes, "200")
		var others []string
		for code := range op.Responses {
			if code != "200" && strings.HasPrefix(code, "2") {
				others = append(others, code)
			}
		}
		sort.Strings(others)
		codes = append(append(codes, others...), "default")
	}
	for _, code := range codes {
		r, ok := op.Responses[code]
		if !ok || r == nil {
			continue
		}
		r, err := doc.Response(r)
		if err != nil {
			return "", nil, err
		}
		mt := jsonContent(r.Content)
		if mt == nil || mt.Schema == nil {
			return "", nil, fmt.Errorf("response %s has no JSON schema", code)
		}
		return code, mt.Schema, nil
	}
	if status != "" {
		return "", nil, fmt.Errorf("no %s response", status)
	}
	return "", nil, fmt.Errorf("no success response")
}

func jsonContent(content map[string]*spec.MediaType) *spec.MediaType {
	if mt, ok := content["application/json"]; ok {
		return mt
	}
	keys := make([]string, 0, len(content))
	for k := range content {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.Contains(k, "json") {
			return content[k]
		}
	}
	return nil
}

type builder struct {
	doc      *spec.Document
	base     string
	objects  []*ObjectType
	names    map[string]bool
	byRef    map[string]*ObjectType
	refNames map[string]string
	stack    map[string]bool
	warnings []string
}

func (b *builder) warn(where, format string, args ...any) {
	b.warnings = append(b.warnings, where+": "+fmt.Sprintf(format, args...))
}

func (b *builder) uniqueType(name string) string {
	name = GoName(name)
	candidate := name
	for i := 2; b.names[candidate]; i++ {
		candidate = fmt.Sprintf("%s%d", name, i)
	}
	b.names[candidate] = true
	return candidate
}

func jsonNode(nullable bool) *Node { return &Node{Kind: KJSON, Nullable: nullable} }

// node converts a schema. hint is the Go name used for an inline object.
func (b *builder) node(s *spec.Schema, hint, where string) (*Node, error) {
	if s == nil {
		b.warn(where, "no schema, mapped to a JSON-encoded string")
		return jsonNode(true), nil
	}
	if s.Never {
		b.warn(where, "schema false, mapped to a JSON-encoded string")
		return jsonNode(true), nil
	}
	if s.Ref == "" {
		return b.resolved(s, hint, where, "")
	}

	if b.stack[s.Ref] {
		b.warn(where, "recursive reference to %s, mapped to a JSON-encoded string", s.Ref)
		return jsonNode(true), nil
	}
	if obj := b.byRef[s.Ref]; obj != nil {
		return &Node{Kind: KObject, Object: obj, Nullable: s.Nullable}, nil
	}
	target, err := b.doc.Schema(s.Ref)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	name, ok := b.refNames[s.Ref]
	if !ok {
		refName, _ := spec.RefName(s.Ref)
		name = b.base + GoName(refName)
	}
	b.stack[s.Ref] = true
	n, err := b.resolved(target, name, where, s.Ref)
	delete(b.stack, s.Ref)
	if err != nil {
		return nil, err
	}
	if s.Nullable {
		n.Nullable = true
	}
	return n, nil
}

// resolved converts a schema that is not a $ref.
func (b *builder) resolved(s *spec.Schema, hint, where, ref string) (*Node, error) {
	nullable := s.Nullable

	if len(s.AllOf) > 0 {
		merged, ok, err := b.mergeAllOf(s, 0)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if !ok {
			b.warn(where, "allOf mixing non-object schemas, mapped to a JSON-encoded string")
			return jsonNode(true), nil
		}
		s = merged
	}

	for _, alts := range [][]*spec.Schema{s.OneOf, s.AnyOf} {
		if len(alts) == 0 {
			continue
		}
		var rest []*spec.Schema
		for _, a := range alts {
			if isNullSchema(a) {
				nullable = true
				continue
			}
			rest = append(rest, a)
		}
		if len(rest) == 1 && len(s.Type) == 0 && s.Properties.Len() == 0 {
			n, err := b.node(rest[0], hint, where)
			if err != nil {
				return nil, err
			}
			n.Nullable = n.Nullable || nullable
			return n, nil
		}
		if len(rest) > 1 {
			b.warn(where, "oneOf/anyOf with several alternatives, mapped to a JSON-encoded string")
			return jsonNode(true), nil
		}
	}

	var types []string
	for _, t := range s.Type {
		if t == "null" {
			nullable = true
			continue
		}
		types = append(types, t)
	}
	if len(types) > 1 {
		b.warn(where, "several types %v, mapped to a JSON-encoded string", types)
		return jsonNode(true), nil
	}
	t := ""
	if len(types) == 1 {
		t = types[0]
	}
	if t == "" {
		switch {
		case s.Properties.Len() > 0 || (s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil):
			t = "object"
		case s.Items != nil:
			t = "array"
		case len(s.Enum) > 0:
			t = enumType(s.Enum)
		}
	}

	enum := enumValues(s.Enum)
	switch t {
	case "string":
		return &Node{Kind: KString, Nullable: nullable, Enum: enum}, nil
	case "integer":
		return &Node{Kind: KInt, Nullable: nullable, Enum: enum}, nil
	case "number":
		return &Node{Kind: KFloat, Nullable: nullable, Enum: enum}, nil
	case "boolean":
		return &Node{Kind: KBool, Nullable: nullable}, nil
	case "array":
		if s.Items == nil {
			b.warn(where, "array without items, mapped to a JSON-encoded string")
			return jsonNode(true), nil
		}
		elem, err := b.node(s.Items, hint+"Item", where+"[]")
		if err != nil {
			return nil, err
		}
		return &Node{Kind: KList, Nullable: nullable, Elem: elem}, nil
	case "object":
		if s.Properties.Len() == 0 {
			if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
				elem, err := b.node(s.AdditionalProperties.Schema, hint+"Value", where+"{}")
				if err != nil {
					return nil, err
				}
				return &Node{Kind: KMap, Nullable: nullable, Elem: elem}, nil
			}
			b.warn(where, "free-form object, mapped to a JSON-encoded string")
			return jsonNode(true), nil
		}
		if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
			b.warn(where, "additionalProperties ignored next to properties")
		}
		obj, err := b.object(s, hint, where, ref)
		if err != nil {
			return nil, err
		}
		return &Node{Kind: KObject, Nullable: nullable, Object: obj}, nil
	case "":
		b.warn(where, "untyped schema, mapped to a JSON-encoded string")
		return jsonNode(true), nil
	default:
		return nil, fmt.Errorf("%s: unsupported type %q", where, t)
	}
}

func (b *builder) object(s *spec.Schema, hint, where, ref string) (*ObjectType, error) {
	// Every response field is nullable on the Terraform side, required or
	// not: the API may omit it.
	obj := &ObjectType{Name: b.uniqueType(hint), Desc: strings.TrimSpace(s.Description)}
	for _, name := range s.Properties.Names {
		ps := s.Properties.Schemas[name]
		at := where + "." + name
		if !validJSONTag(name) {
			b.warn(at, "property name cannot be used in a json tag, skipped")
			continue
		}
		if ps != nil && ps.WriteOnly {
			continue // never present in a response
		}
		n, err := b.node(ps, obj.Name+GoName(name), at)
		if err != nil {
			return nil, err
		}
		target, err := b.doc.Deref(ps)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", at, err)
		}
		f := &Field{
			JSONName:   name,
			GoName:     GoName(name),
			TFName:     TFName(name),
			Node:       n,
			Desc:       describe(withFallback(ps, target), n),
			Sensitive:  isSensitive(ps) || isSensitive(target),
			Deprecated: (ps != nil && ps.Deprecated) || (target != nil && target.Deprecated),
			Exposed:    true,
		}
		if strings.HasSuffix(f.TFName, "_value") && reservedTF[strings.TrimSuffix(f.TFName, "_value")] {
			b.warn(at, "%q is a Terraform meta-argument, attribute renamed %q", strings.TrimSuffix(f.TFName, "_value"), f.TFName)
		}
		obj.Fields = append(obj.Fields, f)
	}
	b.checkFieldNames(obj, false)
	if ref != "" {
		b.byRef[ref] = obj
	}
	b.objects = append(b.objects, obj)
	return obj, nil
}

// checkFieldNames makes Go and Terraform names unique inside an object and
// avoids clashes with generated methods.
func (b *builder) checkFieldNames(obj *ObjectType, root bool) {
	reservedGo := map[string]bool{"AttrTypes": true}
	if root {
		reservedGo = map[string]bool{"FromAPI": true}
	}
	goNames, tfNames := map[string]bool{}, map[string]bool{}
	for _, f := range obj.Fields {
		if reservedGo[f.GoName] {
			f.GoName += "Field"
		}
		name := f.GoName
		for i := 2; goNames[name]; i++ {
			name = fmt.Sprintf("%s%d", f.GoName, i)
		}
		if name != f.GoName {
			b.warn(obj.Name, "Go field %s renamed %s to avoid a clash", f.GoName, name)
			f.GoName = name
		}
		goNames[name] = true

		tf := f.TFName
		for i := 2; tfNames[tf]; i++ {
			tf = fmt.Sprintf("%s_%d", f.TFName, i)
		}
		if tf != f.TFName {
			b.warn(obj.Name, "attribute %s renamed %s to avoid a clash", f.TFName, tf)
			f.TFName = tf
		}
		tfNames[tf] = true
	}
}

// mergeAllOf merges object schemas. ok is false when a part is not an object.
func (b *builder) mergeAllOf(s *spec.Schema, depth int) (*spec.Schema, bool, error) {
	if depth > 16 {
		return nil, false, fmt.Errorf("allOf nested too deeply")
	}
	merged := &spec.Schema{
		Type:        spec.Types{"object"},
		Description: s.Description,
		Nullable:    s.Nullable,
		Deprecated:  s.Deprecated,
		Properties:  &spec.Properties{Schemas: map[string]*spec.Schema{}},
	}
	add := func(part *spec.Schema) {
		if part.Properties != nil {
			for _, name := range part.Properties.Names {
				if _, dup := merged.Properties.Schemas[name]; !dup {
					merged.Properties.Names = append(merged.Properties.Names, name)
				}
				merged.Properties.Schemas[name] = part.Properties.Schemas[name]
			}
		}
		merged.Required = append(merged.Required, part.Required...)
		if merged.Description == "" {
			merged.Description = part.Description
		}
	}
	own := *s
	own.AllOf = nil
	parts := append([]*spec.Schema{}, s.AllOf...)
	parts = append(parts, &own)
	for _, p := range parts {
		p, err := b.doc.Deref(p)
		if err != nil {
			return nil, false, err
		}
		if p == nil {
			continue
		}
		if len(p.AllOf) > 0 {
			inner, ok, err := b.mergeAllOf(p, depth+1)
			if err != nil || !ok {
				return nil, ok, err
			}
			p = inner
		}
		for _, t := range p.Type {
			if t != "object" && t != "null" {
				return nil, false, nil
			}
		}
		if len(p.OneOf) > 0 || len(p.AnyOf) > 0 {
			return nil, false, nil
		}
		add(p)
	}
	return merged, true, nil
}

// selectAttrs restricts the Terraform attributes of the root model.
func selectAttrs(out *Endpoint, attrs []string) error {
	want := map[string]bool{}
	for _, a := range attrs {
		if a = strings.TrimSpace(a); a != "" {
			want[a] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	if out.Wrapped {
		return fmt.Errorf("-attrs needs a response that is an object")
	}
	matched := map[string]bool{}
	var available []string
	for _, f := range out.Root.Fields {
		available = append(available, f.JSONName)
		f.Exposed = false
		for _, name := range []string{f.JSONName, f.TFName} {
			if want[name] {
				f.Exposed = true
				matched[name] = true
			}
		}
	}
	var unknown []string
	for a := range want {
		if !matched[a] {
			unknown = append(unknown, a)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown attributes %s; response properties: %s",
			strings.Join(unknown, ", "), strings.Join(available, ", "))
	}
	return nil
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true,
	"defer": true, "else": true, "fallthrough": true, "for": true, "func": true, "go": true,
	"goto": true, "if": true, "import": true, "interface": true, "map": true, "package": true,
	"range": true, "return": true, "select": true, "struct": true, "switch": true, "type": true,
	"var": true,
	// packages used by the generated helpers
	"url": true, "strconv": true, "strings": true, "fmt": true, "json": true,
}

// params collects the path and query parameters of the operation.
func (b *builder) params(out *Endpoint, ep spec.Endpoint) error {
	type key struct{ name, in string }
	var order []key
	byKey := map[key]*spec.Parameter{}
	for _, list := range [][]*spec.Parameter{ep.Item.Parameters, ep.Operation.Parameters} {
		for _, p := range list {
			p, err := b.doc.Parameter(p)
			if err != nil {
				return err
			}
			k := key{p.Name, p.In}
			if _, seen := byKey[k]; !seen {
				order = append(order, k)
			}
			byKey[k] = p // operation parameters override path-level ones
		}
	}

	placeholders := pathPlaceholders(ep.Path)
	inPath := map[string]bool{}
	for _, name := range placeholders {
		inPath[name] = true
	}
	goNames := map[string]map[string]bool{"path": {}, "query": {}}
	argNames := map[string]bool{}
	add := func(p *Param) {
		name := p.GoName
		for i := 2; goNames[p.In][name]; i++ {
			name = fmt.Sprintf("%s%d", p.GoName, i)
		}
		p.GoName = name
		goNames[p.In][name] = true
		if p.In == "path" {
			arg := lowerFirst(name)
			if goKeywords[arg] {
				arg += "Param"
			}
			for i := 2; argNames[arg]; i++ {
				arg = fmt.Sprintf("%s%d", lowerFirst(name), i)
			}
			argNames[arg] = true
			p.ArgName = arg
		}
		out.Params = append(out.Params, p)
	}

	declared := map[string]bool{}
	for _, k := range order {
		p := byKey[k]
		where := fmt.Sprintf("parameter %s (%s)", p.Name, p.In)
		if p.In != "path" && p.In != "query" {
			b.warn(where, "only path and query parameters are used, skipped")
			continue
		}
		if p.In == "path" && !inPath[p.Name] {
			b.warn(where, "not in the path template, skipped")
			continue
		}
		n, err := b.node(p.Schema, b.base+GoName(p.Name)+"Param", where)
		if err != nil {
			return err
		}
		if n.Kind > KBool {
			if p.In == "path" {
				return fmt.Errorf("%s: path parameters must be strings, integers, numbers or booleans", where)
			}
			b.warn(where, "non-scalar query parameter, skipped")
			continue
		}
		if p.In == "path" {
			declared[p.Name] = true
		}
		add(&Param{
			Name:       p.Name,
			In:         p.In,
			Required:   p.Required || p.In == "path",
			Node:       n,
			GoName:     GoName(p.Name),
			Desc:       strings.TrimSpace(p.Description),
			Deprecated: p.Deprecated,
		})
	}
	for _, name := range placeholders {
		if !declared[name] {
			b.warn("parameter "+name+" (path)", "not declared, assumed to be a string")
			add(&Param{Name: name, In: "path", Required: true, Node: &Node{Kind: KString}, GoName: GoName(name)})
		}
	}
	return nil
}

// pathPlaceholders returns the {name} placeholders of a path template.
func pathPlaceholders(path string) []string {
	var out []string
	for {
		i := strings.Index(path, "{")
		if i < 0 {
			return out
		}
		j := strings.Index(path[i:], "}")
		if j < 0 {
			return out
		}
		out = append(out, path[i+1:i+j])
		path = path[i+j+1:]
	}
}

func isNullSchema(s *spec.Schema) bool {
	return s != nil && s.Ref == "" && len(s.Type) == 1 && s.Type[0] == "null"
}

func isSensitive(s *spec.Schema) bool {
	return s != nil && (s.Format == "password" || s.WriteOnly)
}

// withFallback returns s, or the referenced schema when s is a bare $ref
// without its own description.
func withFallback(s, target *spec.Schema) *spec.Schema {
	if s == nil || (strings.TrimSpace(s.Description) == "" && strings.TrimSpace(s.Title) == "") {
		return target
	}
	return s
}

func describe(s *spec.Schema, n *Node) string {
	var desc string
	if s != nil {
		desc = firstNonEmpty(s.Description, s.Title)
	}
	desc = strings.TrimSpace(desc)
	var extra []string
	if len(n.Enum) > 0 {
		quoted := make([]string, len(n.Enum))
		for i, v := range n.Enum {
			quoted[i] = "`" + v + "`"
		}
		extra = append(extra, "Possible values: "+strings.Join(quoted, ", ")+".")
	}
	if n.Kind == KJSON {
		extra = append(extra, "JSON-encoded value.")
	}
	if len(extra) > 0 {
		if desc != "" && !strings.HasSuffix(desc, ".") {
			desc += "."
		}
		desc = strings.TrimSpace(desc + " " + strings.Join(extra, " "))
	}
	return desc
}

func enumType(values []any) string {
	switch values[0].(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		for _, v := range values {
			if f, ok := v.(float64); ok && f != float64(int64(f)) {
				return "number"
			}
		}
		return "integer"
	}
	return ""
}

func enumValues(values []any) []string {
	var out []string
	for _, v := range values {
		if v == nil {
			continue
		}
		out = append(out, fmt.Sprint(v))
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
