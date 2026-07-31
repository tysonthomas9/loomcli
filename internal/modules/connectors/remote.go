package connectors

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
