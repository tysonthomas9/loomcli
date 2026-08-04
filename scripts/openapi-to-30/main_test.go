package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWalkRewritesOpenAPI31Features(t *testing.T) {
	input := `openapi: 3.1.0
components:
  schemas:
    Name:
      type: [string, "null"]
    Status:
      enum: [open, closed, null]
    Kind:
      const: task
    Nested:
      items:
        - type: [integer, null]
`
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(input), &root); err != nil {
		t.Fatal(err)
	}

	walk(&root)

	doc := root.Content[0]
	if got := lookup(doc, "openapi").Value; got != "3.0.3" {
		t.Fatalf("openapi = %q", got)
	}
	schemas := lookup(lookup(lookup(doc, "components"), "schemas"), "Name")
	if got := lookup(schemas, "type"); got.Kind != yaml.ScalarNode || got.Value != "string" {
		t.Fatalf("Name.type = %#v", got)
	}
	if got := lookup(schemas, "nullable"); got == nil || got.Value != "true" {
		t.Fatalf("Name.nullable = %#v", got)
	}

	status := lookup(lookup(lookup(doc, "components"), "schemas"), "Status")
	enum := lookup(status, "enum")
	if len(enum.Content) != 2 {
		t.Fatalf("Status enum length = %d", len(enum.Content))
	}
	if got := lookup(status, "nullable"); got == nil || got.Value != "true" {
		t.Fatalf("Status.nullable = %#v", got)
	}

	kind := lookup(lookup(lookup(doc, "components"), "schemas"), "Kind")
	if lookup(kind, "const") != nil {
		t.Fatalf("const key was not removed")
	}
	if enum := lookup(kind, "enum"); enum == nil || len(enum.Content) != 1 || enum.Content[0].Value != "task" {
		t.Fatalf("Kind.enum = %#v", enum)
	}
}

func TestRewriteHelpersNoopAndReplacementPaths(t *testing.T) {
	walk(nil)
	walk(&yaml.Node{Kind: yaml.ScalarNode, Value: "scalar"})

	m := &yaml.Node{Kind: yaml.MappingNode}
	if got := lookup(m, "missing"); got != nil {
		t.Fatalf("lookup missing = %#v", got)
	}
	setOrReplace(m, "nullable", boolNode("false"))
	setOrReplace(m, "nullable", boolNode("true"))
	if got := lookup(m, "nullable"); got.Value != "true" {
		t.Fatalf("replacement value = %q", got.Value)
	}
	deleteKey(m, "missing")
	deleteKey(m, "nullable")
	if got := lookup(m, "nullable"); got != nil {
		t.Fatalf("deleteKey left value %#v", got)
	}

	kept, hasNull := filterNullNodes([]*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"},
		{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "~"},
	})
	if !hasNull || len(kept) != 1 || kept[0].Value != "value" {
		t.Fatalf("filterNullNodes = kept=%#v hasNull=%v", kept, hasNull)
	}

	kept, hasNull = filterNullNodes([]*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"}})
	if hasNull || len(kept) != 1 {
		t.Fatalf("filterNullNodes no-null = kept=%#v hasNull=%v", kept, hasNull)
	}
}

func boolNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: value}
}
