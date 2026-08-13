package filesystem

import "regexp"

var (
	legacyAgentName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	storedAgentName = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$`)
)

func validateAgentName(name string) error {
	if name == "" {
		return newInvalid("missing agent name")
	}
	if !legacyAgentName.MatchString(name) && !storedAgentName.MatchString(name) {
		return newInvalid("invalid agent name")
	}
	return nil
}
