package execution

import "time"

// persistedCommandTimeMatches accounts for PostgreSQL's microsecond
// timestamptz precision while retaining exact comparisons for backends that
// preserve nanoseconds. Command timestamps are audit data, not authority, but
// a valid durable receipt must still match the originating command.
func persistedCommandTimeMatches(got, want time.Time) bool {
	return !got.IsZero() && !want.IsZero() &&
		got.Truncate(time.Microsecond).Equal(want.Truncate(time.Microsecond))
}

// ResourceKind keeps DriverRun and TaskRun fencing explicit. They must not be
// treated as interchangeable merely because both use Fleet leases.
type ResourceKind string

const (
	ResourceDriverRun ResourceKind = "driver_run"
	ResourceTaskRun   ResourceKind = "task_run"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusBlocked   Status = "blocked"
	StatusCancelled Status = "cancelled"
)

func (status Status) IsTerminal() bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusBlocked, StatusCancelled:
		return true
	default:
		return false
	}
}

// Owner is the exact resource-bound lease/fence envelope returned by the
// authoritative claim transaction.
type Owner struct {
	ResourceKind ResourceKind
	ResourceID   string
	NodeID       string
	LeaseID      string
	// LeaseToken is the opaque credential presented to the authoritative
	// fenced persistence command. It is deliberately excluded from JSON and
	// must never be copied into results, logs, or authority values.
	LeaseToken   string `json:"-"`
	FencingToken int64
}

type PreflightCommand struct {
	WorkspaceKey         string
	RequestID            string
	RunnerRef            string
	RequiredCapabilities []string
}

type PreflightResult struct {
	RunnerRef  string
	Ready      bool
	ReasonCode string
	CheckedAt  time.Time
}

// ClaimAndLaunchCommand is an intent command. Its store leg must atomically
// claim the Work Item and create/claim the Execution start record under
// RequestID before any external process is launched.
type ClaimAndLaunchCommand struct {
	WorkspaceKey string
	RequestID    string
	WorkItemID   string
	DriverRunID  string
	DriverStepID string
	TaskRunID    string
	RunnerRef    string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
	LeaseTTL     time.Duration
	Input        []byte
}

type ClaimStart struct {
	Owner       Owner
	WorkItemID  string
	DriverRunID string
	TaskRunID   string
	RunnerRef   string
	StartedAt   time.Time
	Replay      bool
}

type LaunchReceipt struct {
	ProcessRef string
	StartedAt  time.Time
}

type ClaimAndLaunchResult struct {
	Claim   ClaimStart
	Launch  LaunchReceipt
	Outcome ExitClassification
}

type HeartbeatCommand struct {
	WorkspaceKey    string
	Owner           Owner
	At              time.Time
	RuntimeRef      string
	LogsRef         string
	ArtifactsRef    string
	RuntimeMetadata map[string]string
}

type HeartbeatResult struct {
	Owner     Owner
	ExpiresAt time.Time
}

type AppendLogCommand struct {
	WorkspaceKey string
	RequestID    string
	Owner        Owner
	Stream       string
	Text         string
	Timestamp    time.Time
}

type LogEntry struct {
	TaskRunID string
	Sequence  int64
	Stream    string
	Text      string
	Timestamp time.Time
}

type ExitClassification struct {
	Status     Status
	ErrorClass string
	Retryable  bool
	Summary    string
}

type ClassifyCommand struct {
	WorkspaceKey string
	Owner        Owner
	ExitCode     int
	Signal       string
	BackendError string
	TimedOut     bool
	Cancelled    bool
}

type FinalizeCommand struct {
	WorkspaceKey        string
	RequestID           string
	Owner               Owner
	Classification      ExitClassification
	ExitCode            *int
	LogsRef             string
	ArtifactsRef        string
	RequiredArtifactIDs []string
	RequireArtifacts    bool
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheWriteTokens    int64
	EstimatedCostUSD    float64
	RuntimeMetadata     map[string]string
	CloseWorkItem       bool
	CloseReason         string
	FinishedAt          time.Time
}

type FinalizeResult struct {
	Owner      Owner
	Status     Status
	FinishedAt time.Time
	Replay     bool
}

type RecoverCommand struct {
	WorkspaceKey string
	RequestID    string
	ResourceKind ResourceKind
	ResourceID   string
	ObservedAt   time.Time
	MaxAge       time.Duration
	Reason       string
}

type RecoverResult struct {
	ResourceKind ResourceKind
	ResourceID   string
	Recovered    bool
	Status       Status
	Replay       bool
}

type AwaitCommand struct {
	WorkspaceKey string
	RequestID    string
	Owner        Owner
	InstanceKey  string
	Pattern      string
	Deadline     time.Time
}

type AwaitResult struct {
	InstanceKey string
	Resolved    bool
	EventID     string
	Replay      bool
}
