package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// ValidateIssueRecoverySelection checks both the native document echo and its
// validated transport metadata. It is a scope check, not a replacement for the
// Fleet adapter's full native manifest validator.
func ValidateIssueRecoverySelection(snapshot IssueRecoverySnapshot, selected string) error {
	if snapshot.SelectedIssueID != selected || (selected != "" && !ValidRecoveryIssueSelection(selected)) {
		return fmt.Errorf("recovery selection metadata mismatch")
	}
	data := snapshot.Document
	if len(data) > 16<<20 || !utf8.Valid(data) {
		return fmt.Errorf("invalid recovery document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkRecoverySelectionJSON(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing recovery data")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	raw, ok := root["history"]
	if !ok {
		return fmt.Errorf("missing recovery history")
	}
	if selected == "" {
		if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("unexpected selected history")
		}
		return nil
	}
	var history map[string]json.RawMessage
	if err := json.Unmarshal(raw, &history); err != nil {
		return err
	}
	var echo *string
	if err := json.Unmarshal(history["issue_id"], &echo); err != nil || echo == nil || *echo != selected {
		return fmt.Errorf("recovery document selection mismatch")
	}
	return nil
}

func walkRecoverySelectionJSON(decoder *json.Decoder, depth int) error {
	if depth > 512 {
		return fmt.Errorf("recovery document nesting exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate recovery field")
			}
			seen[name] = true
			if err := walkRecoverySelectionJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := walkRecoverySelectionJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid recovery delimiter")
	}
	_, err = decoder.Token()
	return err
}
