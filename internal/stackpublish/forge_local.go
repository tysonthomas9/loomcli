package stackpublish

import (
	"context"
	"errors"
)

var errLocalForgeNoPRSupport = errors.New("local forge has no PR support")

// LocalForge publishes stack branches to a filesystem-backed origin. It has no
// pull-request surface; callers must gate PR phases through SupportsPullRequests.
type LocalForge struct {
	origin string
}

var _ Forge = (*LocalForge)(nil)

func NewLocalForge(origin string) *LocalForge {
	return &LocalForge{origin: origin}
}

func (*LocalForge) SupportsPullRequests() bool { return false }

func (*LocalForge) ListStackPRs(context.Context, string, string, string) ([]PR, error) {
	return nil, errLocalForgeNoPRSupport
}

func (*LocalForge) CreatePR(context.Context, string, string, string, string, string, string) (PR, error) {
	return PR{}, errLocalForgeNoPRSupport
}

func (*LocalForge) UpdatePRBase(context.Context, string, string, int, string) error {
	return errLocalForgeNoPRSupport
}

func (*LocalForge) ClosePR(context.Context, string, string, int, string) error {
	return errLocalForgeNoPRSupport
}

func (l *LocalForge) PushBranches(ctx context.Context, repoPath string, pushes []BranchPush) error {
	if len(pushes) == 0 {
		return nil
	}
	remote := l.origin
	if remote == "" {
		remote = "origin"
	}
	args := []string{"push", "--atomic", remote}
	for _, p := range pushes {
		args = append(args, "refs/heads/"+p.Branch+":refs/heads/"+p.Branch)
	}
	for _, p := range pushes {
		if p.ExpectedSHA != "" {
			args = append(args, "--force-with-lease=refs/heads/"+p.Branch+":"+p.ExpectedSHA)
		}
	}
	_, err := runGit(ctx, repoPath, append(envWith(), "GIT_TERMINAL_PROMPT=0"), args...)
	return err
}

func (*LocalForge) QueuedPRNumbers(context.Context, string, string) (map[int]bool, error) {
	return nil, errLocalForgeNoPRSupport
}

func (*LocalForge) PRStatuses(context.Context, string, string, string) (map[string]PRStatus, error) {
	return nil, errLocalForgeNoPRSupport
}

func (*LocalForge) UpdatePRBody(context.Context, string, string, int, string) error {
	return errLocalForgeNoPRSupport
}
