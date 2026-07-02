package terminal

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func runTerminalGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed git executable; validated worktree service args.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), ctxErr, strings.TrimSpace(string(out)))
		}
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
