package sessions

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"time"
)

// validAgentName matches agent names: alphanumeric, underscore, dot, hyphen.
var validAgentName = regexp.MustCompile(`^[a-zA-Z0-9_.\-]+$`)

// GenerateSessionID creates a unique session identifier.
// Format: YYYYMMDD-HHMMSS-<agent>-<taskshort>-<8hexrand>
//
// The taskShort may be empty (e.g., when the task is unknown at session
// creation time), in which case the ID contains a double-dash segment.
func GenerateSessionID(agentName, taskShort string) (string, error) {
	if agentName == "" {
		return "", fmt.Errorf("agent name must not be empty")
	}
	if !validAgentName.MatchString(agentName) {
		return "", fmt.Errorf("invalid agent name %q: must match %s", agentName, validAgentName.String())
	}

	// 8 hex chars from crypto/rand.
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating random suffix: %w", err)
	}
	suffix := fmt.Sprintf("%08x", buf)

	now := time.Now().UTC()
	ts := now.Format("20060102-150405")

	return fmt.Sprintf("%s-%s-%s-%s", ts, agentName, taskShort, suffix), nil
}
