package driverfreeze

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

// FreezeRemoteAt checks a provider-fetched capture before Snapshot.
// Both the commit and tree must match the remote runner's reported hashes.
func FreezeRemoteAt(ctx context.Context, journalPath string, in CaptureRequest, treeHash, fetched string) (loomgit.Revision, error) {
	if !fullObjectID(in.Base) || !fullObjectID(in.CaptureSHA) || !fullObjectID(treeHash) {
		return loomgit.Revision{}, fmt.Errorf("remote capture requires full base, commit and tree hashes")
	}
	if in.Worktree == "" {
		return loomgit.Revision{}, fmt.Errorf("remote capture requires host repository")
	}
	runner, err := gitexec.New(in.Worktree, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return loomgit.Revision{}, err
	}
	baseRef, err := refname.AttemptBase(in.Workspace, in.Attempt)
	if err != nil {
		return loomgit.Revision{}, err
	}
	base, err := runner.Run(ctx, "rev-parse", "--verify", baseRef+"^{commit}")
	if err != nil || strings.TrimSpace(string(base)) != in.Base {
		return loomgit.Revision{}, fmt.Errorf("remote capture base differs from recorded attempt base: %w", err)
	}
	if fetched != in.CaptureSHA {
		return loomgit.Revision{}, fmt.Errorf("provider capture hash mismatch: got %s", fetched)
	}
	tree, err := runner.Run(ctx, "rev-parse", "--verify", fetched+"^{tree}")
	if err != nil || strings.TrimSpace(string(tree)) != treeHash {
		return loomgit.Revision{}, fmt.Errorf("provider capture tree hash mismatch: %w", err)
	}
	if _, err := runner.Run(ctx, "merge-base", "--is-ancestor", in.Base, fetched); err != nil {
		return loomgit.Revision{}, fmt.Errorf("provider capture is not based on attempt base: %w", err)
	}
	return FreezeCaptureAt(ctx, journalPath, in)
}

func fullObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
