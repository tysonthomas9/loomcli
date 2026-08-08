package entity

import (
	"fmt"
	"sort"
	"strings"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

// Workspace name and repo validation constants.
const (
	MaxWorkspaceNameLen = workspacemodule.MaxNameLength
	MaxRepoNameLen      = 128
	MaxRemoteNameLen    = 255
	DefaultRemote       = "origin"
	DefaultBranch       = "main"
)

// Workspace represents a named workspace containing one or more repositories.
// Fields are organized into logical groups for maintainability.
type Workspace struct {
	// ===== Core Identification =====
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`

	// ===== Configuration =====
	Backend string `json:"backend,omitempty"`

	// ===== Repositories =====
	Repos []Repo `json:"repos"`

	// ===== Timestamps =====
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

// Validate checks if the workspace has valid field values.
func (w *Workspace) Validate() error {
	if w.ID == "" {
		return fmt.Errorf("id is required")
	}
	if err := workspacemodule.ValidateName(w.Name); err != nil {
		switch kind, _ := workspacemodule.NameValidationKindOf(err); kind {
		case workspacemodule.NameRequired:
			return fmt.Errorf("name is required")
		case workspacemodule.NameTooLong:
			return fmt.Errorf("name exceeds maximum length of %d characters", MaxWorkspaceNameLen)
		default:
			return fmt.Errorf("name must contain only alphanumeric characters, hyphens, and underscores")
		}
	}
	if w.Path == "" {
		return fmt.Errorf("path is required")
	}
	for i := range w.Repos {
		if err := w.Repos[i].Validate(); err != nil {
			return fmt.Errorf("repos[%d]: %w", i, err)
		}
	}
	return nil
}

// RepoByName returns a pointer to the first repo with the given name, or nil if not found.
func (w *Workspace) RepoByName(name string) *Repo {
	for i := range w.Repos {
		if w.Repos[i].Name == name {
			return &w.Repos[i]
		}
	}
	return nil
}

// GroupNames returns a deduplicated, sorted list of all group names across all repos.
func (w *Workspace) GroupNames() []string {
	seen := make(map[string]struct{})
	for _, r := range w.Repos {
		for _, g := range r.Groups {
			seen[g] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for g := range seen {
		names = append(names, g)
	}
	sort.Strings(names)
	return names
}

// Repo represents a repository within a workspace.
type Repo struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	DefaultBranch string   `json:"default_branch,omitempty"`
	Remote        string   `json:"remote,omitempty"`
	Groups        []string `json:"groups,omitempty"`
	SourceRepoID  string   `json:"source_repo_id,omitempty"`
}

// Validate checks if the repo has valid field values.
func (r *Repo) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(r.Name) > MaxRepoNameLen {
		return fmt.Errorf("name exceeds maximum length of %d characters", MaxRepoNameLen)
	}
	if r.Path == "" {
		return fmt.Errorf("path is required")
	}
	if r.Remote != "" {
		if len(r.Remote) > MaxRemoteNameLen {
			return fmt.Errorf("remote exceeds maximum length of %d characters", MaxRemoteNameLen)
		}
		if strings.HasPrefix(r.Remote, "-") {
			return fmt.Errorf("remote must not start with '-'")
		}
		for _, c := range r.Remote {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
				return fmt.Errorf("remote contains invalid character %q", string(c))
			}
		}
	}
	return nil
}

// EffectiveRemote returns the remote name, defaulting to "origin" if empty.
func (r *Repo) EffectiveRemote() string {
	if r.Remote != "" {
		return r.Remote
	}
	return DefaultRemote
}

// EffectiveDefaultBranch returns the default branch, defaulting to "main" if empty.
func (r *Repo) EffectiveDefaultBranch() string {
	if r.DefaultBranch != "" {
		return r.DefaultBranch
	}
	return DefaultBranch
}

// EffectiveSourceRepoID returns the source repo ID, defaulting to Name if empty.
func (r *Repo) EffectiveSourceRepoID() string {
	if r.SourceRepoID != "" {
		return r.SourceRepoID
	}
	return r.Name
}

// WorkspaceConfig holds per-workspace configuration and preferences,
// separate from Workspace identity and topology. This struct is intentionally
// minimal; fields will be added as V2 settings grow (daemon config, branch
// strategy, security policies, etc.).
type WorkspaceConfig struct {
	Backend string `json:"backend,omitempty"`
}
