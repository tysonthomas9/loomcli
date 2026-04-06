package terminal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// ConfigClient is an interface for testing pool/config operations.
type ConfigClient interface {
	Status() (*rpc.StatusResponse, error)
}

// ConfigConnectionGetter abstracts connection pool access for the terminal
// service (same shape as handlers/misc.configConnectionGetter).
type ConfigConnectionGetter interface {
	Get(ctx context.Context) (ConfigClient, error)
	Put(client ConfigClient)
}

// validTerminalSession matches alphanumeric characters, hyphens, and underscores.
var validTerminalSession = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validBackends is the list of supported AI backend names.
var validBackends = []string{"claude", "codex", "opencode", "gemini", "cursor"}

// isValidBackend checks if the backend name is in the allowed list.
func isValidBackend(name string) bool {
	for _, b := range validBackends {
		if b == name {
			return true
		}
	}
	return false
}

// shellCommand returns the user's default shell, falling back to /bin/bash.
func shellCommand() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/bash"
}

// sessionKillGracePeriod is the delay before a scheduled session kill executes.
const sessionKillGracePeriod = 30 * time.Second

// Seed prompt limits.
const (
	maxDescriptionLen = 800
	maxDesignLen      = 500
	maxBlockers       = 5
)

// issueSessionPattern matches issue-linked session names: "issue-{project}-{number}".
var issueSessionPattern = regexp.MustCompile(`^issue-(.+)-(\d+)$`)

// ExtractIssueID converts a sanitized session name back to an issue ID.
// e.g., "issue-loomcli-fghge-1" → "loomcli-fghge.1"
func ExtractIssueID(sessionName string) string {
	m := issueSessionPattern.FindStringSubmatch(sessionName)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}

// projectFile is a local YAML struct mirroring cli.ProjectFile.
// We define it locally to avoid coupling terminal to the cli package.
type projectFile struct {
	Backend string    `yaml:"backend,omitempty"`
	Daemon  yaml.Node `yaml:"daemon,omitempty"`
	Roles   yaml.Node `yaml:"roles,omitempty"`
}

// getWorkspacePath acquires a daemon connection and returns the workspace path.
func getWorkspacePath(pool ConfigConnectionGetter, ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	client, err := pool.Get(ctx)
	if err != nil {
		return "", err
	}
	defer pool.Put(client)

	status, err := client.Status()
	if err != nil {
		return "", err
	}
	return status.WorkspacePath, nil
}

// loadProjectFile reads and parses loom.yaml from dir.
// Returns an empty projectFile if the file does not exist.
func loadProjectFile(dir string) (*projectFile, error) {
	path := filepath.Join(dir, "loom.yaml")
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return &projectFile{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var pf projectFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &pf, nil
}
