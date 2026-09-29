package githubtoken

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// GitHub resolves the host's GitHub token for a single operation.
func GitHub(ctx context.Context) string {
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			return token
		}
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output() //nolint:norawexec // Host credential lookup, never a Git operation.
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
