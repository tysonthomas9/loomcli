package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"gopkg.in/yaml.v3"
)

// DaemonSettings holds daemon-specific config fields.
type DaemonSettings struct {
	PIDFile       string        `yaml:"pid_file,omitempty"`
	LogDir        string        `yaml:"log_dir,omitempty"`
	RestartPolicy RestartPolicy `yaml:"restart_policy,omitempty"`
}

// RestartPolicy defines how the daemon restarts failed agents.
type RestartPolicy struct {
	MaxRetries     *int `yaml:"max_retries,omitempty"`
	BackoffInitial *int `yaml:"backoff_initial,omitempty"` // seconds
	BackoffMax     *int `yaml:"backoff_max,omitempty"`     // seconds
}

// RoleConfig defines an agent role (built-in like "plan"/"task", or custom).
type RoleConfig struct {
	Description string `yaml:"description,omitempty"`
	PromptFile  string `yaml:"prompt_file,omitempty"`
	Model       string `yaml:"model,omitempty"`
	TaskFilter  string `yaml:"task_filter,omitempty"`
}

// AgentEntry defines a single agent assignment.
type AgentEntry struct {
	Worktree string `yaml:"worktree"`
	Role     string `yaml:"role"`
	Auto     bool   `yaml:"auto,omitempty"`
}

// ProjectFile represents the project-local loom.yaml.
type ProjectFile struct {
	Daemon *DaemonSettings       `yaml:"daemon,omitempty"`
	Roles  map[string]RoleConfig `yaml:"roles,omitempty"`
	Agents []AgentEntry          `yaml:"agents,omitempty"`
}

// DaemonConfig is the merged, resolved configuration used by callers.
type DaemonConfig struct {
	Daemon DaemonSettings
	Roles  map[string]RoleConfig
	Agents []AgentEntry
}

// PromptData is the template context for custom prompt files.
type PromptData struct {
	AgentName    string
	WorktreeName string
	Role         string
	TaskID       string
}

// LoadProjectFile reads and parses the project-local loom.yaml from dir.
// Returns (nil, nil) if the file does not exist.
func LoadProjectFile(dir string) (*ProjectFile, error) {
	path := filepath.Join(dir, "loom.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading project file %s: %w", path, err)
	}

	var pf ProjectFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parsing project file %s: %w", path, err)
	}
	return &pf, nil
}

// LoadDaemonConfig merges global (~/.loom/config.yaml) and local (loom.yaml) config.
// Local values override global. Returns defaults if neither file exists.
func LoadDaemonConfig(projectDir string) (*DaemonConfig, error) {
	// Load global config
	globalCfg, err := LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading global config: %w", err)
	}

	// Load project-local config
	projectFile, err := LoadProjectFile(projectDir)
	if err != nil {
		return nil, fmt.Errorf("loading project config: %w", err)
	}

	// Start with defaults
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			PIDFile: ".loom/daemon.pid",
			LogDir:  ".loom/logs",
			RestartPolicy: RestartPolicy{
				MaxRetries:     intPtr(3),
				BackoffInitial: intPtr(2),
				BackoffMax:     intPtr(300),
			},
		},
		Roles: make(map[string]RoleConfig),
	}

	// Overlay global daemon settings
	if globalCfg != nil && globalCfg.Daemon != nil {
		overlayDaemonSettings(&dc.Daemon, globalCfg.Daemon)
	}

	// Overlay local daemon settings (local wins)
	if projectFile != nil {
		if projectFile.Daemon != nil {
			overlayDaemonSettings(&dc.Daemon, projectFile.Daemon)
		}

		// Merge roles: local replaces entire role entry by key
		for k, v := range projectFile.Roles {
			dc.Roles[k] = v
		}

		// Agents come from local only
		dc.Agents = projectFile.Agents
	}

	// Validate agent entries
	for i, a := range dc.Agents {
		if a.Worktree == "" {
			return nil, fmt.Errorf("agent[%d]: worktree is required", i)
		}
		if a.Role == "" {
			return nil, fmt.Errorf("agent[%d]: role is required", i)
		}
	}

	return dc, nil
}

// overlayDaemonSettings applies explicitly-set values from src onto dst.
func overlayDaemonSettings(dst *DaemonSettings, src *DaemonSettings) {
	if src.PIDFile != "" {
		dst.PIDFile = src.PIDFile
	}
	if src.LogDir != "" {
		dst.LogDir = src.LogDir
	}
	if src.RestartPolicy.MaxRetries != nil {
		dst.RestartPolicy.MaxRetries = src.RestartPolicy.MaxRetries
	}
	if src.RestartPolicy.BackoffInitial != nil {
		dst.RestartPolicy.BackoffInitial = src.RestartPolicy.BackoffInitial
	}
	if src.RestartPolicy.BackoffMax != nil {
		dst.RestartPolicy.BackoffMax = src.RestartPolicy.BackoffMax
	}
}

func intPtr(v int) *int { return &v }

// ResolveRole looks up a role by name in the merged config.
// Returns the RoleConfig and true if found, zero value and false if not.
func (dc *DaemonConfig) ResolveRole(name string) (RoleConfig, bool) {
	rc, ok := dc.Roles[name]
	return rc, ok
}

// LoadPromptTemplate reads a prompt template file and executes it with the given data.
// Returns the rendered prompt string.
func LoadPromptTemplate(path string, data PromptData) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading prompt template %s: %w", path, err)
	}

	tmpl, err := template.New(filepath.Base(path)).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parsing prompt template %s: %w", path, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing prompt template %s: %w", path, err)
	}

	return buf.String(), nil
}
