package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Comments retain their native bytes and workspace-wide IDs; they are not
// inferred from issue history or merged with optimistic client drafts.
func validateRecoveryComments(rows []json.RawMessage, issues map[string]map[string]json.RawMessage) error {
	seen := make(map[string]bool, len(rows))
	for _, raw := range rows {
		id, err := validateRecoveryComment(raw, issues)
		if err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("duplicate comment")
		}
		seen[id] = true
	}
	return nil
}

func validateRecoveryComment(raw json.RawMessage, issues map[string]map[string]json.RawMessage) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", err
	}
	if len(fields) != 5 {
		return "", fmt.Errorf("invalid comment fields")
	}
	values := make(map[string]string, 5)
	for _, key := range []string{"id", "issue_id", "author", "body", "created_at"} {
		value, err := recoveryString(fields, key, true)
		if err != nil {
			return "", err
		}
		values[key] = value
	}
	for _, key := range []string{"id", "author", "body"} {
		if strings.TrimSpace(values[key]) == "" {
			return "", fmt.Errorf("blank comment %s", key)
		}
	}
	if issues[values["issue_id"]] == nil || len(values["body"]) > 10000 || !validRecoveryTimestamp(values["created_at"]) {
		return "", fmt.Errorf("invalid comment record")
	}
	return values["id"], nil
}
