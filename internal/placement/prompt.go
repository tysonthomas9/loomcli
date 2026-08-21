package placement

import (
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// LeadPromptText resolves the prompt text that should be uploaded for a lead
// role before the sandbox PTY starts.
func LeadPromptText(role *domain.Role) (string, error) {
	if role == nil {
		return "", nil
	}
	if role.Prompt != "" {
		return role.Prompt, nil
	}
	path := strings.TrimSpace(role.PromptFile)
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- role prompt_file resolution is intentional here.
	if err != nil {
		return "", err
	}
	return string(data), nil
}
