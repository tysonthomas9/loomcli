package escapes

import (
	"context"
	"os/exec"
	run "os/exec"
)

const gitBinary = "git"

func calls(ctx context.Context) {
	_ = exec.Command("git", "status")
	_ = exec.CommandContext(ctx, "git", "status")
	_ = exec.CommandContext(context.WithoutCancel(ctx), "git", "status")
	_ = exec.CommandContext(ctx,
		"git", "status")
	_ = run.Command("git", "status")
	_ = exec.Command(gitBinary, "status")
	_ = exec.Command(`git`, "status")
	_ = exec.Command("gh", "git")
	_ = exec.CommandContext(ctx, "ls", "git")
}
