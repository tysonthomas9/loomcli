package loomgit

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit/gitversion"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

func CheckGitVersion() error {
	output, err := gitexec.Version()
	if err != nil {
		return fmt.Errorf("%s: Loom Git requires Git %s or newer: %w", gitversion.ErrorCode, gitversion.Minimum, err)
	}
	return gitversion.Check(output)
}
