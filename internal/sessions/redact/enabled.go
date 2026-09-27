package redact

import (
	"os"
	"strings"
)

// Loom-local helpers for deciding when host-side persistence sinks should run
// the canonical redactor. The redaction algorithm itself stays in redact.go,
// which is ported from upstream; see ORIGIN.md.

// StatusRedacted is the artifact RedactionStatus value for content that passed
// through the canonical redactor before persistence.
const StatusRedacted = "redacted"

// RuntimeMetadataTextKeys names task-run runtime metadata fields that can hold
// runner-produced display text. Structural IDs, refs, SHAs, hashes, URLs, and
// PR numbers are intentionally excluded because entropy redaction would mangle
// legitimate identifiers.
var RuntimeMetadataTextKeys = []string{
	"response_text",
	"error_message",
	"diff",
	"diff_stat",
	"diffStat",
	"summary",
	"patch_summary",
}

// Enabled reports whether host-side persistence sinks should redact captured
// runner output. Default on. Set LOOM_REDACT_TRANSCRIPTS to off, 0, false, or
// no to disable redaction for local debugging.
func Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_REDACT_TRANSCRIPTS")))
	return v != "off" && v != "0" && v != "false" && v != "no"
}

// RedactRuntimeMetadataText redacts only allowlisted text-bearing metadata
// values in place. The caller owns map cloning before calling this helper.
func RedactRuntimeMetadataText(m map[string]string) {
	if !Enabled() {
		return
	}
	for _, key := range RuntimeMetadataTextKeys {
		if value := m[key]; value != "" {
			m[key] = String(value)
		}
	}
}
