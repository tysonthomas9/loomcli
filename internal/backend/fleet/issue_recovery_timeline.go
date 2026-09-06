package fleet

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Timeline formatting belongs to Fleet. Loom checks native-event correspondence
// and the wire shape without interpreting raw before/after JSON or opaque IDs.
func validateRecoveryTimeline(rows, events []json.RawMessage) error {
	if len(rows) != len(events) {
		return fmt.Errorf("timeline event count mismatch")
	}
	seen := make(map[string]bool, len(rows))
	for i, raw := range rows {
		id, err := validateRecoveryTimelineRow(raw, events[i])
		if err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("duplicate timeline ID")
		}
		seen[id] = true
	}
	return nil
}

func validateRecoveryTimelineRow(raw, event json.RawMessage) (string, error) {
	var fields, native map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", err
	}
	if err := json.Unmarshal(event, &native); err != nil {
		return "", err
	}
	if len(fields) != 9 {
		return "", fmt.Errorf("invalid timeline fields")
	}
	values := make(map[string]string, 7)
	for _, key := range []string{"id", "event_id", "timestamp", "actor", "action", "category", "summary"} {
		value, err := recoveryString(fields, key, key != "summary")
		if err != nil {
			return "", err
		}
		values[key] = value
	}
	if !backend.ValidTimelineCursor(values["id"]) {
		return "", fmt.Errorf("invalid timeline ID")
	}
	for _, key := range []string{"event_id", "timestamp", "actor", "action"} {
		sourceKey := key
		if key == "event_id" {
			sourceKey = "id"
		}
		value, err := recoveryString(native, sourceKey, true)
		if err != nil || values[key] != value {
			return "", fmt.Errorf("timeline differs from native event")
		}
	}
	switch values["category"] {
	case "lifecycle", "assignment", "field_change", "dependency", "label", "comment", "time_management", "other":
	default:
		return "", fmt.Errorf("invalid timeline category")
	}
	if err := validateRecoveryTimelineMetadata(fields["metadata"], native["metadata"]); err != nil {
		return "", err
	}
	if err := validateRecoveryTimelineChanges(fields["changes"]); err != nil {
		return "", err
	}
	return values["id"], nil
}

func validateRecoveryTimelineMetadata(raw, native json.RawMessage) error {
	var actual, expected map[string]*string
	if err := json.Unmarshal(raw, &actual); err != nil || actual == nil {
		return fmt.Errorf("invalid timeline metadata")
	}
	if err := json.Unmarshal(native, &expected); err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("timeline metadata differs from native event")
	}
	return nil
}

func validateRecoveryTimelineChanges(raw json.RawMessage) error {
	var changes *[]json.RawMessage
	if err := json.Unmarshal(raw, &changes); err != nil || changes == nil {
		return fmt.Errorf("invalid timeline changes")
	}
	var previous string
	for i, change := range *changes {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(change, &fields); err != nil {
			return err
		}
		if len(fields) != 3 {
			return fmt.Errorf("invalid timeline change fields")
		}
		var field string
		for _, key := range []string{"field", "before", "after"} {
			value, err := recoveryString(fields, key, false)
			if err != nil {
				return err
			}
			if key == "field" {
				field = value
			}
		}
		// Go string order is raw UTF-8 byte order, including the empty field name.
		if i > 0 && field <= previous {
			return fmt.Errorf("timeline changes not strictly ordered")
		}
		previous = field
	}
	return nil
}
