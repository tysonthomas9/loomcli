package cli

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ResolveSecretsInBytes walks YAML data, resolving $secret: references in string scalars.
// Returns data unchanged if no $secret: references are found.
func ResolveSecretsInBytes(data []byte, resolver *SecretResolver) ([]byte, error) {
	if len(data) == 0 || !bytes.Contains(data, []byte("$secret:")) {
		return data, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing YAML for secret resolution: %w", err)
	}

	if err := walkSecretNodes(&doc, resolver); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("re-encoding YAML after secret resolution: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("closing YAML encoder: %w", err)
	}

	return buf.Bytes(), nil
}

// walkSecretNodes recursively walks a yaml.Node tree and resolves $secret: references.
func walkSecretNodes(node *yaml.Node, resolver *SecretResolver) error {
	if node == nil {
		return nil
	}

	switch node.Kind {
	case yaml.ScalarNode:
		// Only process string scalars (mirror walkNodes guard from config_envsubst.go)
		if node.Tag != "" && node.Tag != "!!str" {
			return nil
		}
		if !strings.Contains(node.Value, "$secret:") {
			return nil
		}
		resolved, err := resolver.ResolveAllInString(node.Value)
		if err != nil {
			return fmt.Errorf("line %d col %d: %w", node.Line, node.Column, err)
		}
		node.Value = resolved

	case yaml.DocumentNode, yaml.MappingNode, yaml.SequenceNode:
		for _, child := range node.Content {
			if err := walkSecretNodes(child, resolver); err != nil {
				return err
			}
		}

	case yaml.AliasNode:
		// Aliases point to already-walked nodes; skip to avoid double resolution.
	}

	return nil
}
