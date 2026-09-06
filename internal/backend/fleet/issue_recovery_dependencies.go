package fleet

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

var recoveryDependencyTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

// Dependency records belong to the complete native workspace image. They do
// not pass through the UI compatibility model or synthesize missing edges.
func validateRecoveryDependencies(rows []json.RawMessage, issues map[string]map[string]json.RawMessage) error {
	seen := make(map[[3]string]bool, len(rows))
	for _, raw := range rows {
		key, err := validateRecoveryDependency(raw, issues)
		if err != nil {
			return err
		}
		if seen[key] {
			return fmt.Errorf("duplicate dependency")
		}
		seen[key] = true
	}
	return nil
}

func validateRecoveryDependency(raw json.RawMessage, issues map[string]map[string]json.RawMessage) ([3]string, error) {
	var fields map[string]json.RawMessage
	var key [3]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		return key, err
	}
	if len(fields) != 5 {
		return key, fmt.Errorf("invalid dependency fields")
	}
	values := make(map[string]string, 5)
	for _, name := range []string{"issue_id", "depends_on_id", "type", "created_at", "created_by"} {
		value, err := recoveryString(fields, name, true)
		if err != nil {
			return key, err
		}
		values[name] = value
	}
	key = [3]string{values["issue_id"], values["depends_on_id"], values["type"]}
	if issues[key[0]] == nil || issues[key[1]] == nil || key[0] == key[1] {
		return key, fmt.Errorf("invalid dependency reference")
	}
	switch key[2] {
	case "blocks", "parent-child", "related", "duplicate-of", "superseded-by":
	default:
		return key, fmt.Errorf("invalid dependency type")
	}
	// Go accepts noncanonical fractions, hour widths and offsets that the
	// browser rejects. Check lexical form before parsing calendar semantics.
	if !recoveryDependencyTimestamp.MatchString(values["created_at"]) {
		return key, fmt.Errorf("invalid dependency timestamp")
	}
	created, err := time.Parse(time.RFC3339Nano, values["created_at"])
	if err != nil || created.IsZero() {
		return key, fmt.Errorf("invalid dependency timestamp")
	}
	return key, nil
}
