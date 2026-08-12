package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrInvalidJSONL  = errors.New("invalid canonical transcript JSONL")
	ErrTooLarge      = errors.New("canonical transcript exceeds byte limit")
	ErrTooManyEvents = errors.New("canonical transcript exceeds event limit")
)

// DecodeCanonicalJSONL validates the exact durable transcript representation.
// It rejects arrays, blank lines, missing final newlines, invalid vocabulary,
// and non-contiguous sequence numbers. The bool reports that valid additional
// events existed after maxEvents; evidence writers may use that signal to
// replace the tail with explicit truncation provenance.
func DecodeCanonicalJSONL(content []byte, maxBytes, maxEvents int) ([]Event, bool, error) {
	if maxBytes <= 0 || maxEvents <= 0 {
		return nil, false, fmt.Errorf("positive transcript limits are required: %w", ErrInvalidJSONL)
	}
	if len(content) > maxBytes {
		return nil, false, ErrTooLarge
	}
	if len(content) == 0 || content[len(content)-1] != '\n' {
		return nil, false, fmt.Errorf("canonical transcript must end with a newline: %w", ErrInvalidJSONL)
	}
	lines := bytes.Split(content[:len(content)-1], []byte{'\n'})
	events := make([]Event, 0, min(len(lines), maxEvents))
	for index, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			return nil, false, fmt.Errorf("canonical transcript line %d is blank: %w", index+1, ErrInvalidJSONL)
		}
		if index >= maxEvents {
			return events, true, nil
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, false, fmt.Errorf("decode canonical transcript line %d: %w", index+1, errors.Join(ErrInvalidJSONL, err))
		}
		if err := ValidateCanonicalEvent(event); err != nil {
			return nil, false, fmt.Errorf("validate canonical transcript line %d: %w", index+1, errors.Join(ErrInvalidJSONL, err))
		}
		if event.Seq != index+1 {
			return nil, false, fmt.Errorf("canonical transcript line %d has sequence %d: %w", index+1, event.Seq, ErrInvalidJSONL)
		}
		events = append(events, event)
	}
	return events, false, nil
}
