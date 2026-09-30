package loomgit

import (
	"fmt"
	"os/exec"

	"github.com/tysonthomas9/loomcli/internal/loomgit/gitversion"
)

func CheckGitVersion() error {
	output, err := exec.Command("git", "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: Loom Git requires Git %s or newer: %w", gitversion.ErrorCode, gitversion.Minimum, err)
	}
	return gitversion.Check(string(output))
}
