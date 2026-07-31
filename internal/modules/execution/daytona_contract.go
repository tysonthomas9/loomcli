package execution

import "context"

// DaytonaProviderBroker is the narrow provider port available to an already
// owner-fenced Daytona TaskRun. It accepts secret-free intent and returns only
// typed, credential-free execution evidence.
type DaytonaProviderBroker interface {
	ExecuteDaytona(context.Context, DaytonaProviderCommand) (DaytonaProviderResult, error)
}

// DaytonaProviderCommand is derived by the TaskRun HTTP facade after exact
// lease/fence verification. Caller-supplied identifiers are absent from the
// wire request.
type DaytonaProviderCommand struct {
	WorkspaceKey string                `json:"workspaceKey"`
	TaskRunID    string                `json:"taskRunId"`
	WorkItemID   string                `json:"workItemId"`
	DriverRunID  string                `json:"driverRunId"`
	Intent       DaytonaProviderIntent `json:"intent"`
}

// DaytonaProviderIntent is the complete secret-free request admitted from a
// Daytona task runner.
type DaytonaProviderIntent struct {
	SchemaVersion string                  `json:"schemaVersion"`
	RepositoryURL string                  `json:"repositoryUrl"`
	BaseRef       string                  `json:"baseRef,omitempty"`
	TaskPrompt    string                  `json:"taskPrompt"`
	Backend       string                  `json:"backend"`
	Model         string                  `json:"model,omitempty"`
	Mode          string                  `json:"mode,omitempty"`
	Delivery      DaytonaProviderDelivery `json:"delivery"`
}

type DaytonaProviderDelivery struct {
	OpenPullRequest bool   `json:"openPullRequest"`
	BaseBranch      string `json:"baseBranch,omitempty"`
	OutputBranch    string `json:"outputBranch,omitempty"`
	Draft           bool   `json:"draft,omitempty"`
}

type DaytonaProviderResult struct {
	SchemaVersion     string                     `json:"schemaVersion"`
	Status            string                     `json:"status"`
	ExitCode          int                        `json:"exitCode"`
	ErrorClass        string                     `json:"errorClass,omitempty"`
	ErrorMessage      string                     `json:"errorMessage,omitempty"`
	Logs              string                     `json:"logs,omitempty"`
	Transcript        string                     `json:"transcript,omitempty"`
	TranscriptEntries []DaytonaTranscriptEntry   `json:"transcriptEntries,omitempty"`
	Usage             DaytonaProviderUsage       `json:"usage"`
	Sandbox           DaytonaSandboxReceipt      `json:"sandbox"`
	Patch             *DaytonaPatchReceipt       `json:"patch,omitempty"`
	PullRequest       *DaytonaPullRequestReceipt `json:"pullRequest,omitempty"`
}

type DaytonaTranscriptEntry struct {
	Sequence  int64  `json:"sequence"`
	Timestamp string `json:"timestamp"`
	Role      string `json:"role"`
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ToolName  string `json:"toolName,omitempty"`
	ToolUseID string `json:"toolUseId,omitempty"`
	Output    string `json:"output,omitempty"`
	UUID      string `json:"uuid,omitempty"`
}

type DaytonaProviderUsage struct {
	InputTokens      int64   `json:"inputTokens,omitempty"`
	OutputTokens     int64   `json:"outputTokens,omitempty"`
	CacheReadTokens  int64   `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64   `json:"cacheWriteTokens,omitempty"`
	EstimatedCostUSD float64 `json:"estimatedCostUsd,omitempty"`
}

type DaytonaSandboxReceipt struct {
	Provider string `json:"provider"`
	ID       string `json:"id,omitempty"`
	WorkDir  string `json:"workDir,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	RepoRef  string `json:"repoRef,omitempty"`
}

type DaytonaPatchReceipt struct {
	Content  string `json:"content"`
	DiffStat string `json:"diffStat,omitempty"`
	BaseRef  string `json:"baseRef,omitempty"`
	HeadSHA  string `json:"headSha,omitempty"`
}

type DaytonaPullRequestReceipt struct {
	URL        string `json:"url"`
	Number     int64  `json:"number"`
	BaseBranch string `json:"baseBranch"`
	HeadBranch string `json:"headBranch"`
	CommitSHA  string `json:"commitSha"`
}
