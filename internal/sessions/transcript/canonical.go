package transcript

import (
	"bytes"
	"encoding/json"
)

// ParseCanonicalBytes decodes the canonical transcript artifact wire format.
// Producers may persist either a JSON array or newline-delimited Event objects.
func ParseCanonicalBytes(data []byte) ([]Event, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []Event{}, nil
	}
	if trimmed[0] == '[' {
		var events []Event
		if err := json.Unmarshal(trimmed, &events); err != nil {
			return nil, err
		}
		return events, nil
	}
	lines := bytes.Split(trimmed, []byte("\n"))
	events := make([]Event, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}
