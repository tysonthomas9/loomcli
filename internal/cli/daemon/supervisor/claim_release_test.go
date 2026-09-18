package supervisor

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// PUPPET-467: releaseAssignedTaskClaim must prefer the full claim release over
// the lock-only one, must skip a task another live agent already reclaimed, and
// must never decline silently.

type releaseRecorder struct {
	*clitest.MockIssueBackend
	claimReleases  []string
	lockReleases   []string
	claimReleaseFn func() error
}

func (b *releaseRecorder) ReleaseClaim(_ context.Context, id, actor string) error {
	b.claimReleases = append(b.claimReleases, id+"/"+actor)
	if b.claimReleaseFn != nil {
		return b.claimReleaseFn()
	}
	return nil
}

func (b *releaseRecorder) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	b.lockReleases = append(b.lockReleases, id+"/"+actor)
	return nil
}

// lockOnlyReleaser implements only the weaker interface.
type lockOnlyReleaser struct {
	*clitest.MockIssueBackend
	lockReleases []string
}

func (b *lockOnlyReleaser) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	b.lockReleases = append(b.lockReleases, id+"/"+actor)
	return nil
}

func newReleaseAgent(worktree string) *AgentProcess {
	return &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: worktree, Role: "task"}}
}

// captureWarnings redirects slog to a buffer for the duration of the test.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestReleaseAssignedTaskClaim_PrefersClaimRelease(t *testing.T) {
	ib := &releaseRecorder{MockIssueBackend: clitest.NewMockIssueBackend()}
	s := &Supervisor{IssueBackend: ib}
	ap := newReleaseAgent("decomposer")

	s.releaseAssignedTaskClaim(ap, "PUPPET-467")

	if want := []string{"PUPPET-467/decomposer"}; len(ib.claimReleases) != 1 || ib.claimReleases[0] != want[0] {
		t.Fatalf("claimReleases = %v, want %v", ib.claimReleases, want)
	}
	if len(ib.lockReleases) != 0 {
		t.Fatalf("lockReleases = %v, want none — the lock-only path leaves the task in_progress", ib.lockReleases)
	}
}

func TestReleaseAssignedTaskClaim_FallsBackToLockRelease(t *testing.T) {
	ib := &lockOnlyReleaser{MockIssueBackend: clitest.NewMockIssueBackend()}
	s := &Supervisor{IssueBackend: ib}

	s.releaseAssignedTaskClaim(newReleaseAgent("decomposer"), "PUPPET-467")

	if len(ib.lockReleases) != 1 || ib.lockReleases[0] != "PUPPET-467/decomposer" {
		t.Fatalf("lockReleases = %v, want [PUPPET-467/decomposer]", ib.lockReleases)
	}
}

func TestReleaseAssignedTaskClaim_UnsupportedBackendWarns(t *testing.T) {
	buf := captureWarnings(t)
	s := &Supervisor{IssueBackend: clitest.NewMockIssueBackend()}

	s.releaseAssignedTaskClaim(newReleaseAgent("decomposer"), "PUPPET-467")

	if !strings.Contains(buf.String(), "agent task claim not released") {
		t.Fatalf("expected a Warn about the unsupported backend, got: %s", buf.String())
	}
}

func TestReleaseAssignedTaskClaim_ReleaseErrorWarns(t *testing.T) {
	buf := captureWarnings(t)
	ib := &releaseRecorder{
		MockIssueBackend: clitest.NewMockIssueBackend(),
		claimReleaseFn:   func() error { return errors.New("fleet-db unreachable") },
	}
	s := &Supervisor{IssueBackend: ib}

	s.releaseAssignedTaskClaim(newReleaseAgent("decomposer"), "PUPPET-467")

	out := buf.String()
	if !strings.Contains(out, "agent task claim release failed") || !strings.Contains(out, "fleet-db unreachable") {
		t.Fatalf("expected a Warn carrying the release error, got: %s", out)
	}
}

// TestReleaseAssignedTaskClaim_SkipsWhenAnotherAgentHolds covers the reclaim
// race: every agent authenticates as the same fleet-db actor, so the server
// cannot tell an exiting agent from the one that just took its task. This
// daemon can, for the agents it owns.
func TestReleaseAssignedTaskClaim_SkipsWhenAnotherAgentHolds(t *testing.T) {
	ib := &releaseRecorder{MockIssueBackend: clitest.NewMockIssueBackend()}
	exiting := newReleaseAgent("decomposer")
	reclaimer := newReleaseAgent("worker-2")
	reclaimer.AssignedTaskID = "PUPPET-467"
	s := &Supervisor{IssueBackend: ib, Agents: []*AgentProcess{exiting, reclaimer}}

	s.releaseAssignedTaskClaim(exiting, "PUPPET-467")

	if len(ib.claimReleases) != 0 || len(ib.lockReleases) != 0 {
		t.Fatalf("released a task another live agent holds: claim=%v lock=%v", ib.claimReleases, ib.lockReleases)
	}
}

// The exiting agent's own AssignedTaskID must not count as "another agent".
func TestReleaseAssignedTaskClaim_SelfHoldDoesNotSkip(t *testing.T) {
	ib := &releaseRecorder{MockIssueBackend: clitest.NewMockIssueBackend()}
	exiting := newReleaseAgent("decomposer")
	exiting.AssignedTaskID = "PUPPET-467"
	s := &Supervisor{IssueBackend: ib, Agents: []*AgentProcess{exiting}}

	s.releaseAssignedTaskClaim(exiting, "PUPPET-467")

	if len(ib.claimReleases) != 1 {
		t.Fatalf("claimReleases = %v, want the agent's own task to be released", ib.claimReleases)
	}
}

type actorReportingBackend struct {
	*clitest.MockIssueBackend
	actor string
}

func (b *actorReportingBackend) ConfiguredActor() string { return b.actor }

func TestClaimActorFor(t *testing.T) {
	ap := newReleaseAgent("decomposer")

	s := &Supervisor{IssueBackend: &actorReportingBackend{MockIssueBackend: clitest.NewMockIssueBackend(), actor: "loom"}}
	if got := s.claimActorFor(ap); got != "loom" {
		t.Errorf("claimActorFor = %q, want loom (the identity ClaimIssue registered the worker under)", got)
	}

	s = &Supervisor{IssueBackend: &actorReportingBackend{MockIssueBackend: clitest.NewMockIssueBackend(), actor: ""}}
	if got := s.claimActorFor(ap); got != "decomposer" {
		t.Errorf("claimActorFor = %q, want the worktree fallback", got)
	}

	s = &Supervisor{IssueBackend: clitest.NewMockIssueBackend()}
	if got := s.claimActorFor(ap); got != "decomposer" {
		t.Errorf("claimActorFor = %q, want the worktree fallback", got)
	}
}

var _ backend.IssueBackend = (*releaseRecorder)(nil)
