package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// orderedMap preserves the document order of a YAML mapping.
//
// The generator depends on this for byte-stable output: ranging over a Go map
// reorders paths, properties, and responses on every run, which would make the
// staleness check flap.
type orderedMap[T any] struct {
	keys   []string
	values map[string]T
}

// UnmarshalYAML decodes a mapping node, recording key order.
func (m *orderedMap[T]) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: expected mapping, got kind %d", n.Line, n.Kind)
	}
	m.keys = nil
	m.values = make(map[string]T, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		var val T
		if err := n.Content[i+1].Decode(&val); err != nil {
			return fmt.Errorf("line %d: key %q: %w", n.Content[i].Line, key, err)
		}
		if _, dup := m.values[key]; !dup {
			m.keys = append(m.keys, key)
		}
		m.values[key] = val
	}
	return nil
}

// Keys returns the mapping keys in document order.
func (m *orderedMap[T]) Keys() []string { return m.keys }

// Get returns the value for key.
func (m *orderedMap[T]) Get(key string) (T, bool) {
	v, ok := m.values[key]
	return v, ok
}

// Len returns the number of entries.
func (m *orderedMap[T]) Len() int { return len(m.keys) }

// stringList decodes a YAML value that may be either a scalar string or a
// sequence of strings. OpenAPI 3.1 allows `type: [string, "null"]`.
type stringList []string

// UnmarshalYAML accepts both a scalar and a sequence.
func (s *stringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*s = stringList{n.Value}
		return nil
	}
	var out []string
	if err := n.Decode(&out); err != nil {
		return err
	}
	*s = out
	return nil
}

// additionalProperties decodes the JSON Schema `additionalProperties` member,
// which is either a boolean (open or closed map) or a nested value schema.
// oapi-codegen and openapi-typescript both accept the boolean form; the
// markdown renderer only needs the schema form (to name a map's value type),
// so a boolean decodes to a nil Schema.
type additionalProperties struct {
	Schema *Schema
}

// UnmarshalYAML accepts both the boolean and the nested-schema forms.
func (a *additionalProperties) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return nil
	}
	var sc Schema
	if err := n.Decode(&sc); err != nil {
		return err
	}
	a.Schema = &sc
	return nil
}

// Spec is the subset of an OpenAPI document the markdown renderer consumes.
type Spec struct {
	OpenAPI    string                `yaml:"openapi"`
	Info       Info                  `yaml:"info"`
	Servers    []Server              `yaml:"servers"`
	Tags       []Tag                 `yaml:"tags"`
	Security   []map[string][]string `yaml:"security"`
	Paths      orderedMap[PathItem]  `yaml:"paths"`
	Components Components            `yaml:"components"`
}

// Info mirrors the OpenAPI info object.
type Info struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
}

// Server mirrors the OpenAPI server object.
type Server struct {
	URL         string `yaml:"url"`
	Description string `yaml:"description"`
}

// Tag mirrors the OpenAPI tag object. Declaration order drives section order
// in the generated document.
type Tag struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Components holds the reusable objects the renderer resolves $refs against.
type Components struct {
	SecuritySchemes orderedMap[SecurityScheme] `yaml:"securitySchemes"`
	Parameters      orderedMap[Parameter]      `yaml:"parameters"`
	Schemas         orderedMap[Schema]         `yaml:"schemas"`
}

// SecurityScheme mirrors the OpenAPI security scheme object.
type SecurityScheme struct {
	Type         string `yaml:"type"`
	Scheme       string `yaml:"scheme"`
	In           string `yaml:"in"`
	Name         string `yaml:"name"`
	BearerFormat string `yaml:"bearerFormat"`
	Description  string `yaml:"description"`
}

// PathItem holds the operations declared for one path. Only the HTTP methods
// Loom actually serves are modeled.
type PathItem struct {
	Get     *Operation  `yaml:"get"`
	Post    *Operation  `yaml:"post"`
	Put     *Operation  `yaml:"put"`
	Patch   *Operation  `yaml:"patch"`
	Delete  *Operation  `yaml:"delete"`
	Head    *Operation  `yaml:"head"`
	Options *Operation  `yaml:"options"`
	Params  []Parameter `yaml:"parameters"`
}

// methodOrder is the canonical method ordering used everywhere in the output.
var methodOrder = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

// operations returns the path's operations in canonical method order.
func (p PathItem) operations() []struct {
	Method string
	Op     *Operation
} {
	byMethod := map[string]*Operation{
		"GET": p.Get, "POST": p.Post, "PUT": p.Put, "PATCH": p.Patch,
		"DELETE": p.Delete, "HEAD": p.Head, "OPTIONS": p.Options,
	}
	var out []struct {
		Method string
		Op     *Operation
	}
	for _, m := range methodOrder {
		if op := byMethod[m]; op != nil {
			out = append(out, struct {
				Method string
				Op     *Operation
			}{m, op})
		}
	}
	return out
}

// Operation mirrors the OpenAPI operation object.
type Operation struct {
	OperationID string                 `yaml:"operationId"`
	Tags        []string               `yaml:"tags"`
	Summary     string                 `yaml:"summary"`
	Description string                 `yaml:"description"`
	Deprecated  bool                   `yaml:"deprecated"`
	Security    *[]map[string][]string `yaml:"security"`
	Parameters  []Parameter            `yaml:"parameters"`
	RequestBody *RequestBody           `yaml:"requestBody"`
	Responses   orderedMap[Response]   `yaml:"responses"`
}

// Parameter mirrors the OpenAPI parameter object; Ref is set for `$ref` entries.
type Parameter struct {
	Ref         string  `yaml:"$ref"`
	Name        string  `yaml:"name"`
	In          string  `yaml:"in"`
	Required    bool    `yaml:"required"`
	Deprecated  bool    `yaml:"deprecated"`
	Description string  `yaml:"description"`
	Schema      *Schema `yaml:"schema"`
}

// RequestBody mirrors the OpenAPI request body object.
type RequestBody struct {
	Required    bool                  `yaml:"required"`
	Description string                `yaml:"description"`
	Content     orderedMap[MediaType] `yaml:"content"`
}

// Response mirrors the OpenAPI response object.
type Response struct {
	Description string                `yaml:"description"`
	Headers     orderedMap[Header]    `yaml:"headers"`
	Content     orderedMap[MediaType] `yaml:"content"`
}

// Header mirrors the OpenAPI header object.
type Header struct {
	Description string  `yaml:"description"`
	Schema      *Schema `yaml:"schema"`
}

// MediaType mirrors the OpenAPI media type object.
type MediaType struct {
	Schema *Schema `yaml:"schema"`
}

// Schema is the subset of JSON Schema used by api/openapi.yaml.
type Schema struct {
	Ref                  string               `yaml:"$ref"`
	Type                 stringList           `yaml:"type"`
	Format               string               `yaml:"format"`
	Description          string               `yaml:"description"`
	Enum                 []any                `yaml:"enum"`
	Const                any                  `yaml:"const"`
	Required             []string             `yaml:"required"`
	Properties           orderedMap[Schema]   `yaml:"properties"`
	Items                *Schema              `yaml:"items"`
	AllOf                []Schema             `yaml:"allOf"`
	AdditionalProperties additionalProperties `yaml:"additionalProperties"`
	Nullable             bool                 `yaml:"nullable"`
	Deprecated           bool                 `yaml:"deprecated"`
}

// isEmpty reports whether the schema carries no renderable information.
func (s *Schema) isEmpty() bool {
	return s == nil || (s.Ref == "" && len(s.Type) == 0 && len(s.Enum) == 0 &&
		s.Const == nil && s.Properties.Len() == 0 && s.Items == nil && len(s.AllOf) == 0)
}

// refName returns the trailing component of a local `$ref`, or "" when the
// reference points outside `#/components/...`.
func refName(ref string) string {
	if !strings.HasPrefix(ref, "#/components/") {
		return ""
	}
	idx := strings.LastIndex(ref, "/")
	return ref[idx+1:]
}

// parseSpec decodes an OpenAPI document.
func parseSpec(data []byte) (*Spec, error) {
	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	if s.Paths.Len() == 0 {
		return nil, fmt.Errorf("parse spec: no paths declared")
	}
	return &s, nil
}

// resolveParam dereferences a `$ref` parameter against components.parameters.
// Unresolvable refs are returned as-is so the renderer can flag them instead of
// silently dropping a documented parameter.
func (s *Spec) resolveParam(p Parameter) Parameter {
	if p.Ref == "" {
		return p
	}
	name := refName(p.Ref)
	if name == "" {
		return p
	}
	resolved, ok := s.Components.Parameters.Get(name)
	if !ok {
		return p
	}
	return resolved
}

// SpecOperation is one flattened method+path pair.
type SpecOperation struct {
	Method string
	Path   string
	Op     *Operation
	// PathParams are parameters declared on the path item rather than the
	// operation; they apply to every operation under that path.
	PathParams []Parameter
}

// operations flattens the spec into method+path pairs in document order.
func (s *Spec) operations() []SpecOperation {
	var out []SpecOperation
	for _, path := range s.Paths.Keys() {
		item, _ := s.Paths.Get(path)
		for _, mo := range item.operations() {
			out = append(out, SpecOperation{
				Method:     mo.Method,
				Path:       path,
				Op:         mo.Op,
				PathParams: item.Params,
			})
		}
	}
	return out
}
