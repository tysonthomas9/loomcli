package backend

import (
	"encoding/base64"
	"errors"
	"strings"
)

var ErrMutationSourceChanged = errors.New("mutation source identity changed")

// ValidSourceIdentity validates only the opaque envelope, never its contents.
func ValidSourceIdentity(value string) bool {
	return validSourceEnvelope(value, "s1.")
}

// ValidMutationCursor accepts only a concrete opaque durable cursor envelope.
func ValidMutationCursor(value string) bool { return validSourceEnvelope(value, "c2.") }

func validSourceEnvelope(value, prefix string) bool {
	if len(value) > 1024 || !strings.HasPrefix(value, prefix) {
		return false
	}
	encoded := strings.TrimPrefix(value, prefix)
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	return err == nil && len(data) > 0 && base64.RawURLEncoding.EncodeToString(data) == encoded
}

// ValidTimelineCursor validates the ordinary history record envelope, not an SSE checkpoint.
func ValidTimelineCursor(value string) bool { return validSourceEnvelope(value, "c1.") }
