package fleetdb

import "time"

func bodyPtr[T any](body map[string]any, key string, value *T) {
	if value != nil {
		body[key] = *value
	}
}

func bodyTimeRFC3339NanoPtr(body map[string]any, key string, value *time.Time) {
	if value != nil {
		body[key] = value.Format(time.RFC3339Nano)
	}
}
