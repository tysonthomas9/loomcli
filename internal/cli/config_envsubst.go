package cli

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExpandEnvVars expands environment variable references in a string.
// Supported syntax:
//   - ${VAR}          — replaced with the variable's value; error if unset (empty-but-set is valid)
//   - ${VAR:-default} — replaced with os.Getenv("VAR") if non-empty, otherwise "default"
//   - ${VAR:?message} — replaced with os.Getenv("VAR"); error with "message" if empty/unset
//   - $$              — literal $
func ExpandEnvVars(s string) (string, error) {
	var buf strings.Builder
	buf.Grow(len(s))

	i := 0
	for i < len(s) {
		if s[i] != '$' {
			buf.WriteByte(s[i])
			i++
			continue
		}

		// Escaped $$
		if i+1 < len(s) && s[i+1] == '$' {
			buf.WriteByte('$')
			i += 2
			continue
		}

		// Check for ${
		if i+1 >= len(s) || s[i+1] != '{' {
			buf.WriteByte(s[i])
			i++
			continue
		}

		// Find closing brace and expand the expression
		start := i
		end := findClosingBrace(s, i+2)
		if end == -1 {
			return "", fmt.Errorf("unclosed ${ at position %d", start)
		}

		expanded, err := expandExpr(s[i+2:end], start)
		if err != nil {
			return "", err
		}
		buf.WriteString(expanded)
		i = end + 1
	}

	return buf.String(), nil
}

// findClosingBrace returns the index of the closing } for a ${ expression,
// starting the search from pos (just after the opening ${). Returns -1 if not found.
func findClosingBrace(s string, pos int) int {
	depth := 1
	for j := pos; j < len(s); j++ {
		if s[j] == '{' {
			depth++
		} else if s[j] == '}' {
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// expandExpr resolves a single ${...} expression (the content between the braces).
// start is the position of the opening $ for error messages.
func expandExpr(expr string, start int) (string, error) {
	if expr == "" {
		return "", fmt.Errorf("empty variable name in ${} at position %d", start)
	}
	if strings.Contains(expr, "${") {
		return "", fmt.Errorf("nested variable references are not supported at position %d", start)
	}

	varName, defaultVal, mode := parseExpr(expr)

	if varName == "" {
		return "", fmt.Errorf("empty variable name at position %d", start)
	}

	val, found := os.LookupEnv(varName)
	if val != "" {
		return val, nil
	}

	switch mode {
	case exprDefault:
		return defaultVal, nil
	case exprError:
		return "", fmt.Errorf("required variable %s: %s", varName, defaultVal)
	default: // exprRequired
		if found {
			return "", nil
		}
		return "", fmt.Errorf("environment variable %q is not set", varName)
	}
}

type exprMode int

const (
	exprRequired exprMode = iota
	exprDefault
	exprError
)

// parseExpr splits an expression into variable name, modifier value, and mode.
func parseExpr(expr string) (varName string, modVal string, mode exprMode) {
	if idx := strings.Index(expr, ":-"); idx >= 0 {
		return expr[:idx], expr[idx+2:], exprDefault
	}
	if idx := strings.Index(expr, ":?"); idx >= 0 {
		return expr[:idx], expr[idx+2:], exprError
	}
	return expr, "", exprRequired
}

// ExpandConfigBytes parses YAML data into a node tree, expands ${...} patterns
// in all string scalar values, and re-encodes the tree. This preserves YAML
// structure and provides line/column info in error messages.
func ExpandConfigBytes(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing YAML for env expansion: %w", err)
	}

	if err := walkNodes(&doc); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("re-encoding YAML after env expansion: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("closing YAML encoder: %w", err)
	}

	return buf.Bytes(), nil
}

// walkNodes recursively walks a yaml.Node tree and expands env vars in string scalars.
func walkNodes(node *yaml.Node) error {
	if node == nil {
		return nil
	}

	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!str" || (node.Tag == "" && node.Style != 0) || isImplicitString(node) {
			expanded, err := ExpandEnvVars(node.Value)
			if err != nil {
				return fmt.Errorf("line %d: %w", node.Line, err)
			}
			node.Value = expanded
		}

	case yaml.DocumentNode, yaml.MappingNode, yaml.SequenceNode:
		for _, child := range node.Content {
			if err := walkNodes(child); err != nil {
				return err
			}
		}

	case yaml.AliasNode:
		// Aliases point to already-walked nodes; skip to avoid double expansion.
	}

	return nil
}

// isImplicitString checks if a scalar node is an implicitly-typed string
// (i.e., not a number, bool, null, or timestamp that YAML would auto-detect).
func isImplicitString(node *yaml.Node) bool {
	if node.Kind != yaml.ScalarNode {
		return false
	}
	if node.Tag != "" && node.Tag != "!!str" {
		return false
	}
	return strings.Contains(node.Value, "${")
}
