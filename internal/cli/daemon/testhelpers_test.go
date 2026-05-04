package daemon

import (
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// --- Type aliases from config ---

type AgentEntry = config.AgentEntry
type RoleConfig = config.RoleConfig
type DaemonConfig = config.DaemonConfig
type DaemonSettings = config.DaemonSettings
type RestartPolicy = config.RestartPolicy
type RepoConfig = config.RepoConfig

func intPtr(v int) *int    { return config.IntPtr(v) }
func boolPtr(v bool) *bool { return config.BoolPtr(v) }

// --- Type aliases from supervisor ---

type AgentProcess = supervisor.AgentProcess
type ConcurrencyTracker = supervisor.ConcurrencyTracker
type YieldRequest = supervisor.YieldRequest
type SupervisedAgentStatus = supervisor.SupervisedAgentStatus

var NewConcurrencyTracker = supervisor.NewConcurrencyTracker

// Stop reason constants from supervisor
const (
	StopReasonShutdown      = supervisor.StopReasonShutdown
	StopReasonMaxRetries    = supervisor.StopReasonMaxRetries
	StopReasonFatalError    = supervisor.StopReasonFatalError
	StopReasonConfigRemoved = supervisor.StopReasonConfigRemoved
)

// --- Type aliases from cli ---

type LockInfo = cli.LockInfo
type Checkpoint = config.Checkpoint

const LockFileName = cli.LockFileName

var (
	ResolveLockDir   = cli.ResolveLockDir
	LoadCheckpoint   = config.LoadCheckpoint
	SaveCheckpoint   = config.SaveCheckpoint
	WriteYieldFile   = supervisor.WriteYieldFile
	ReadYieldFile    = supervisor.ReadYieldFile
	ClearYieldFile   = supervisor.ClearYieldFile
	IsYieldRequested = supervisor.IsYieldRequested
)

// Yield constants
const (
	YieldFileName         = supervisor.YieldFileName
	DefaultYieldTimeout   = supervisor.DefaultYieldTimeout
	DefaultSigtermTimeout = supervisor.DefaultSigtermTimeout
)

// Supervisor helpers
var mergeRoleConfig = supervisor.MergeRoleConfig

// builtInRoles is unused but referenced in some tests
var builtInRoles = map[string]bool{"plan": true, "task": true, "review": true}
