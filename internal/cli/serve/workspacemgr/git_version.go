package workspacemgr

import "github.com/tysonthomas9/loomcli/internal/loomgit"

func CheckGitVersion() error {
	return loomgit.CheckGitVersion()
}
