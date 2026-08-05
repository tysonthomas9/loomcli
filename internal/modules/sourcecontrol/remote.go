package sourcecontrol

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/platform/repositoryremote"
)

func normalizeTokenFreeRemote(value string) (string, error) {
	remote, err := repositoryremote.Normalize(value)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return remote, nil
}

// ValidateTokenFreeRemote returns the canonical repository remote accepted by
// Source Control. Workspace admission calls this before persisting a
// repository projection so secrets can never enter the durable store and the
// later resolver cannot observe a value that this owner would reject.
func ValidateTokenFreeRemote(value string) (string, error) {
	return normalizeTokenFreeRemote(value)
}
