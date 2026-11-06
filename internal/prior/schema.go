// Package prior generates, from the schema of a released provider binary
// (the output of terraform providers schema -json), the frozen PriorSchema
// of a resource, a model decoding states stored with it, and a
// resource.StateUpgrader built on them.
package prior

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Schema is the schema of one resource, as written by terraform providers
// schema -json.
type Schema struct {
	Version int64  `json:"version"`
	Block   *Block `json:"block"`
}

// Block is a schema block: the root of a resource or a nested block.
type Block struct {
	Attributes map[string]*Attribute   `json:"attributes"`
	BlockTypes map[string]*NestedBlock `json:"block_types"`
}

// Attribute is an attribute of a block or of a nested attribute object.
type Attribute struct {
	Type       json.RawMessage `json:"type"`
	NestedType *NestedType     `json:"nested_type"`
	Required   bool            `json:"required"`
	Optional   bool            `json:"optional"`
	Computed   bool            `json:"computed"`
	Sensitive  bool            `json:"sensitive"`
	WriteOnly  bool            `json:"write_only"`
}

// NestedType is the object of a nested attribute.
type NestedType struct {
	Attributes  map[string]*Attribute `json:"attributes"`
	NestingMode string                `json:"nesting_mode"`
}

// NestedBlock is a nested block and its nesting mode.
type NestedBlock struct {
	Block       *Block `json:"block"`
	NestingMode string `json:"nesting_mode"`
}

type providerSchemas struct {
	ProviderSchemas map[string]struct {
		ResourceSchemas map[string]*Schema `json:"resource_schemas"`
	} `json:"provider_schemas"`
}

// Resource is a resource found in a schema document.
type Resource struct {
	Provider string // provider source address, empty for a single schema
	Type     string // resource type name, empty for a single schema
	Schema   *Schema
}

// Load reads either the whole output of terraform providers schema -json or
// the schema of a single resource ({"version": ..., "block": ...}), and
// returns its resources sorted by type.
func Load(data []byte) ([]Resource, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("decoding the schema: %w", err)
	}
	if _, ok := probe["provider_schemas"]; !ok {
		if _, ok := probe["block"]; !ok {
			return nil, fmt.Errorf("not a provider schema: neither provider_schemas nor block found")
		}
		var s Schema
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("decoding the schema: %w", err)
		}
		return []Resource{{Schema: &s}}, nil
	}
	var ps providerSchemas
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("decoding the schema: %w", err)
	}
	var out []Resource
	for provider, p := range ps.ProviderSchemas {
		for typ, s := range p.ResourceSchemas {
			out = append(out, Resource{Provider: provider, Type: typ, Schema: s})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Provider < out[j].Provider
	})
	return out, nil
}

// Find returns the resource of the given type. provider, when not empty,
// selects among providers defining the same type; a short name (monprov)
// matches the last element of the source address.
func Find(resources []Resource, typ, provider string) (Resource, error) {
	if len(resources) == 1 && resources[0].Type == "" {
		return resources[0], nil
	}
	var found []Resource
	for _, r := range resources {
		if r.Type != typ {
			continue
		}
		if provider != "" && r.Provider != provider && providerName(r.Provider) != provider {
			continue
		}
		found = append(found, r)
	}
	switch len(found) {
	case 0:
		return Resource{}, fmt.Errorf("resource %q not found", typ)
	case 1:
		return found[0], nil
	}
	var names []string
	for _, r := range found {
		names = append(names, r.Provider)
	}
	return Resource{}, fmt.Errorf("resource %q is defined by several providers (%s): set the provider", typ, strings.Join(names, ", "))
}

// providerName returns the local name of a provider source address:
// registry.terraform.io/adejoux/monprov -> monprov.
func providerName(source string) string {
	return source[strings.LastIndex(source, "/")+1:]
}

// ctyType is a decoded type constraint of the schema JSON.
type ctyType struct {
	Kind  string // string, number, bool, dynamic, list, set, map, object, tuple
	Elem  *ctyType
	Attrs map[string]*ctyType
}

func parseType(raw json.RawMessage) (*ctyType, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing type")
	}
	var prim string
	if err := json.Unmarshal(raw, &prim); err == nil {
		switch prim {
		case "string", "number", "bool", "dynamic":
			return &ctyType{Kind: prim}, nil
		}
		return nil, fmt.Errorf("unknown type %q", prim)
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil || len(parts) < 2 {
		return nil, fmt.Errorf("invalid type %s", raw)
	}
	var kind string
	if err := json.Unmarshal(parts[0], &kind); err != nil {
		return nil, fmt.Errorf("invalid type %s", raw)
	}
	switch kind {
	case "list", "set", "map":
		elem, err := parseType(parts[1])
		if err != nil {
			return nil, err
		}
		return &ctyType{Kind: kind, Elem: elem}, nil
	case "object":
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal(parts[1], &attrs); err != nil {
			return nil, fmt.Errorf("invalid object type %s", raw)
		}
		t := &ctyType{Kind: kind, Attrs: map[string]*ctyType{}}
		for name, a := range attrs {
			at, err := parseType(a)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			t.Attrs[name] = at
		}
		return t, nil
	case "tuple":
		return &ctyType{Kind: kind}, nil
	}
	return nil, fmt.Errorf("unknown type %s", raw)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
