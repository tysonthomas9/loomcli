package sessions

import "github.com/tysonthomas9/loomcli/internal/sessions/redact"

// RedactString masks secrets in free-form text for UI display. Handlers that
// already import sessions can use this instead of reaching into the redact
// subpackage and burning an import-fanout slot.
func RedactString(s string) string {
	return redact.String(s)
}
