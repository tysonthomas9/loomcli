package execution

import (
	"context"
	"time"
)

type DriverRunDependencies struct {
	Submissions DriverRunSubmissionPort
	ChildStarts DriverRunChildStartPort
	Cascades    DriverRunCascadePort
	Claims      DriverRunClaimPort
	Heartbeats  DriverRunHeartbeatPort
	WorkItems   DriverRunWorkItemPort
	Finalizer   DriverRunFinalizePort
	Recovery    DriverRunRecoveryPort
	Awaits      DriverAwaitPort
	Queries     DriverRunQueryPort
	Resolutions DriverAwaitResolutionPort

	// TerminalWorkRecovery is a system-only convergence seam for descendants
	// and claims left behind after a DriverRun has reached terminal state.
	TerminalWorkRecovery DriverRunTerminalWorkRecoveryPort
}

type DriverRunSubmissionPort interface {
	SubmitDriverRun(context.Context, SubmitDriverRunCommand) (*DriverRun, error)
}

// DriverRunChildStartPort owns one parent-owner-fenced child creation or
// exact replay transaction. It must validate the current parent owner and
// lineage depth in the same transaction that creates the child receipt.
type DriverRunChildStartPort interface {
	StartChildDriverRun(context.Context, StartChildDriverRunCommand) (StartChildDriverRunResult, error)
}

// DriverRunCascadePort owns the terminal-parent recursive child cascade as
// one atomic backend command. Implementations must never degrade this into a
// list followed by per-child mutations.
type DriverRunCascadePort interface {
	CascadeChildDriverRuns(context.Context, CascadeChildDriverRunsCommand) (CascadeChildDriverRunsResult, error)
}

// DriverRunTerminalWorkRecoveryPort owns one atomic, idempotent convergence
// command for TaskRuns and Work Item claim generations left behind by a
// terminal DriverRun. Implementations must preserve any successor generation
// rather than releasing it through a stale parent recovery.
type DriverRunTerminalWorkRecoveryPort interface {
	RecoverTerminalDriverRunWork(context.Context, RecoverTerminalDriverRunWorkCommand) (RecoverTerminalDriverRunWorkResult, error)
}

type DriverRunClaimPort interface {
	ClaimDriverRun(context.Context, ClaimDriverRunCommand) (*DriverRun, error)
}

type DriverRunHeartbeatPort interface {
	HeartbeatDriverRun(context.Context, DriverRunHeartbeatCommand) (*DriverRun, error)
}

// DriverRunWorkItemPort owns the atomic owner-fenced claim and release of a
// Work Item on behalf of one live DriverRun. Implementations must validate the
// parent owner in the same transaction that mutates the issue projection and
// commits the action receipt.
type DriverRunWorkItemPort interface {
	ClaimDriverRunWorkItem(context.Context, ClaimDriverRunWorkItemCommand) (DriverRunWorkItemMutationResult, error)
	ReleaseDriverRunWorkItem(context.Context, ReleaseDriverRunWorkItemCommand) (DriverRunWorkItemMutationResult, error)
}

type DriverRunFinalizePort interface {
	FinalizeDriverRun(context.Context, FinalizeDriverRunCommand) (*DriverRun, error)
}

type DriverRunRecoveryPort interface {
	RecoverDriverRuns(context.Context, RecoverDriverRunsCommand) (*DriverRunRecoveryResult, error)
}

type DriverRunQueryPort interface {
	GetDriverRun(context.Context, string, string) (*DriverRun, error)
}

type DriverAwaitResolutionPort interface {
	ResolveAndResumeDriverAwait(context.Context, ResolveDriverAwaitCommand) error
}

// DriverAwaitPort is the persistence boundary used by the await use case.
// RegisterAndCheck is atomic in FleetDB; Suspend remains owner-fenced. The
// service closes the accepted register-to-suspend resolution window with the
// satisfied-read and idempotent ResumeAwaiting operations.
type DriverAwaitPort interface {
	RegisterAndCheckDriverAwait(context.Context, string, DriverAwaitRegistration) (*DriverAwaitRegistrationResult, error)
	GetSatisfiedDriverAwait(context.Context, string, string) (*DriverAwaitInstance, error)
	SuspendDriverRun(context.Context, string, Owner, string) (*DriverRun, error)
	ResumeAwaitingDriverRun(context.Context, string, string, string, string) (*DriverRun, error)
}

type DriverAwaitRegistration struct {
	InstanceKey  string
	RunID        string
	Pattern      string
	ActorAllow   []string
	Deadline     time.Time
	RegisteredAt time.Time
}

type DriverAwaitRegistrationResult struct {
	Instance  *DriverAwaitInstance
	Satisfied bool
}
