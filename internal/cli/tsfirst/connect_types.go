package tsfirst

import "io"

type connectUsage struct {
	InputTokens              int64 `json:"input_tokens,omitempty"`
	OutputTokens             int64 `json:"output_tokens,omitempty"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	TotalTokens              int64 `json:"total_tokens,omitempty"`
}

type connectResult struct {
	Root              string              `json:"root"`
	Agent             string              `json:"agent"`
	Instance          string              `json:"instance"`
	Session           string              `json:"session"`
	Backend           string              `json:"backend,omitempty"`
	Model             string              `json:"model,omitempty"`
	ProviderModel     string              `json:"provider_model,omitempty"`
	ProviderSessionID string              `json:"provider_session_id,omitempty"`
	ProviderMetadata  map[string]any      `json:"provider_metadata,omitempty"`
	OperationID       string              `json:"operation_id,omitempty"`
	DurationMS        int64               `json:"duration_ms,omitempty"`
	Usage             *connectUsage       `json:"usage,omitempty"`
	WorkDir           string              `json:"work_dir,omitempty"`
	EnvFile           string              `json:"env_file,omitempty"`
	Env               []string            `json:"env,omitempty"`
	Message           string              `json:"message,omitempty"`
	Response          string              `json:"response,omitempty"`
	TranscriptPath    string              `json:"transcript_path,omitempty"`
	ToolRuntime       *connectToolRuntime `json:"tool_runtime,omitempty"`
}

type connectOptions struct {
	Dir      string
	Agent    string
	Instance string
	Session  string
	EnvFile  string
	Message  string
	Stream   io.Writer
}

type localInvocationResult struct {
	Response          string
	ProviderSessionID string
	ProviderModel     string
	ProviderMetadata  map[string]any
	Usage             *connectUsage
	ToolRuntime       *connectToolRuntime
}

type connectToolRuntime struct {
	Status     string             `json:"status"`
	Message    string             `json:"message,omitempty"`
	TypedTools []connectTypedTool `json:"typed_tools,omitempty"`
}

type connectTypedTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Version     string         `json:"version,omitempty"`
	SourcePath  string         `json:"source_path,omitempty"`
	SourceHash  string         `json:"source_hash,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Handler     string         `json:"handler,omitempty"`
	Runtime     string         `json:"runtime,omitempty"`
	Repos       []string       `json:"repos,omitempty"`
	Env         []string       `json:"env,omitempty"`
	ReadOnly    bool           `json:"read_only,omitempty"`
}
