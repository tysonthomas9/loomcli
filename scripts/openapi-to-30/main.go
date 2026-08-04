// openapi-to-30 preprocesses api/openapi.yaml (OpenAPI 3.1) into a
// 3.0-compatible form so oapi-codegen v2.6 (which does not yet fully support
// 3.1 nullable syntax or const) can generate Go types from it.
//
// Transformations applied recursively throughout the tree:
//   - openapi: 3.1.0            ->  openapi: 3.0.3
//   - type: [T, "null"]         ->  type: T; nullable: true
//   - enum: [..., null]         ->  drop null entry; nullable: true
//   - const: X                  ->  enum: [X]
//
// Output is written to stdout.
//
// Usage: go run ./scripts/openapi-to-30 api/openapi.yaml > tmp/spec-3.0.yaml
package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: openapi-to-30 <spec.yaml>")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[1]) //nolint:gosec // G304: caller-controlled spec path
	if err != nil {
		fmt.Fprintf(os.Stderr, "read spec: %v\n", err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		fmt.Fprintf(os.Stderr, "parse spec: %v\n", err)
		os.Exit(1)
	}

	walk(&root)

	enc := yaml.NewEncoder(os.Stdout)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		fmt.Fprintf(os.Stderr, "encode spec: %v\n", err)
		os.Exit(1)
	}
	_ = enc.Close()
}

// walk rewrites yaml nodes in place to convert 3.1 features to 3.0.
func walk(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			walk(c)
		}
	case yaml.MappingNode:
		rewriteMapping(n)
		for _, c := range n.Content {
			walk(c)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			walk(c)
		}
	}
}

// rewriteMapping applies the 3.1 -> 3.0 transformations to a mapping node.
// Content layout: [key0, val0, key1, val1, ...].
func rewriteMapping(m *yaml.Node) {
	rewriteVersion(m)
	rewriteNullableType(m)
	rewriteNullableEnum(m)
	rewriteConst(m)
}

// rewriteVersion bumps the openapi version scalar from 3.1.x to 3.0.3.
func rewriteVersion(m *yaml.Node) {
	v := lookup(m, "openapi")
	if v == nil || v.Kind != yaml.ScalarNode {
		return
	}
	if len(v.Value) >= 3 && v.Value[:3] == "3.1" {
		v.Value = "3.0.3"
	}
}

// rewriteNullableType converts `type: [T, "null"]` into `type: T; nullable: true`.
func rewriteNullableType(m *yaml.Node) {
	t := lookup(m, "type")
	if t == nil || t.Kind != yaml.SequenceNode {
		return
	}
	kept, hasNull := filterNullNodes(t.Content)
	if len(kept) > 0 {
		// Replace the sequence with the first kept scalar.
		t.Kind = yaml.ScalarNode
		t.Tag = kept[0].Tag
		t.Value = kept[0].Value
		t.Content = nil
	}
	if hasNull {
		setOrReplace(m, "nullable", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	}
}

// rewriteNullableEnum drops a trailing null entry from an enum list and
// sets nullable: true when one was present.
func rewriteNullableEnum(m *yaml.Node) {
	e := lookup(m, "enum")
	if e == nil || e.Kind != yaml.SequenceNode {
		return
	}
	kept, hasNull := filterNullNodes(e.Content)
	if hasNull {
		e.Content = kept
		setOrReplace(m, "nullable", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	}
}

// rewriteConst converts a `const: X` node into `enum: [X]`.
func rewriteConst(m *yaml.Node) {
	c := lookup(m, "const")
	if c == nil {
		return
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{c}}
	deleteKey(m, "const")
	setOrReplace(m, "enum", seq)
}

// filterNullNodes removes null-typed scalars from a node slice, returning
// the remaining nodes and whether any nulls were dropped.
func filterNullNodes(nodes []*yaml.Node) (kept []*yaml.Node, hasNull bool) {
	for _, item := range nodes {
		if item.Kind == yaml.ScalarNode && (item.Tag == "!!null" || item.Value == "null" || item.Value == "~") {
			hasNull = true
			continue
		}
		kept = append(kept, item)
	}
	return kept, hasNull
}

// lookup returns the value node for a given key in a mapping, or nil.
func lookup(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setOrReplace sets a key to the given value in a mapping, replacing any existing entry.
func setOrReplace(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		val,
	)
}

// deleteKey removes a key from a mapping.
func deleteKey(m *yaml.Node, key string) {
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
