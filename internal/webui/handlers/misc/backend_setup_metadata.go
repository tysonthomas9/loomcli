package misc

// SetupAction is one curated install or login command for a backend.
// Commands are server-controlled — never assembled from request input —
// and are previewed in the UI before execution. See
// docs/product/web-onboarding-spec.md "Terminal Command Safety".
type SetupAction struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Command     string `json:"command"`
	Interactive bool   `json:"interactive"`
}

// EnvVarHint advertises an environment variable a backend can use for
// authentication. RestartRequired is true when changing the variable
// requires a `loom serve` restart to take effect (the canonical answer
// for env-var auth — the running server's process environment is fixed
// at startup).
type EnvVarHint struct {
	Name            string `json:"name"`
	RestartRequired bool   `json:"restart_required"`
}

// BackendSetupMetadata is the curated, server-side description of how a
// backend gets installed and authenticated. The handler joins this with
// the live ops.BackendHealth signal at response time.
type BackendSetupMetadata struct {
	Description    string        `json:"description"`
	InstallActions []SetupAction `json:"install_actions,omitempty"`
	LoginActions   []SetupAction `json:"login_actions,omitempty"`
	EnvVars        []EnvVarHint  `json:"env_vars,omitempty"`
}

// backendSetupRegistry holds curated setup metadata keyed by backend
// name. Entries are intentionally minimal in this first pass; expand
// when adding a new supported backend.
var backendSetupRegistry = map[string]BackendSetupMetadata{
	"claude": {
		Description: "Anthropic Claude — code reasoning, refactoring, and pair-programming.",
		InstallActions: []SetupAction{
			{
				ID:          "npm-global",
				Label:       "Install Claude CLI with npm",
				Command:     "npm install -g @anthropic-ai/claude-code",
				Interactive: false,
			},
		},
		LoginActions: []SetupAction{
			{
				ID:          "claude-login",
				Label:       "Run claude login",
				Command:     "claude login",
				Interactive: true,
			},
		},
		EnvVars: []EnvVarHint{
			{Name: "ANTHROPIC_API_KEY", RestartRequired: true},
		},
	},
	"codex": {
		Description: "OpenAI Codex CLI — code generation and refactoring.",
		InstallActions: []SetupAction{
			{
				ID:          "npm-global",
				Label:       "Install Codex CLI with npm",
				Command:     "npm install -g @openai/codex",
				Interactive: false,
			},
		},
		LoginActions: []SetupAction{
			{
				ID:          "codex-login",
				Label:       "Run codex login",
				Command:     "codex login",
				Interactive: true,
			},
		},
		EnvVars: []EnvVarHint{
			{Name: "OPENAI_API_KEY", RestartRequired: true},
		},
	},
	"opencode": {
		Description: "Open-source coding assistant.",
		InstallActions: []SetupAction{
			{
				ID:          "manual",
				Label:       "Install OpenCode",
				Command:     "brew install opencode",
				Interactive: false,
			},
		},
		EnvVars: []EnvVarHint{
			{Name: "OPENAI_API_KEY", RestartRequired: true},
		},
	},
	"gemini": {
		Description: "Google Gemini CLI.",
		LoginActions: []SetupAction{
			{
				ID:          "gemini-login",
				Label:       "Run gemini auth login",
				Command:     "gemini auth login",
				Interactive: true,
			},
		},
		EnvVars: []EnvVarHint{
			{Name: "GOOGLE_API_KEY", RestartRequired: true},
		},
	},
	"cursor": {
		Description: "Cursor agent CLI.",
		EnvVars: []EnvVarHint{
			{Name: "CURSOR_API_KEY", RestartRequired: true},
		},
	},
}

// LookupBackendSetupMetadata returns curated setup metadata for the
// given backend name. The second return value is false when no entry
// exists; the frontend then renders the backend with status only.
func LookupBackendSetupMetadata(name string) (BackendSetupMetadata, bool) {
	m, ok := backendSetupRegistry[name]
	return m, ok
}
