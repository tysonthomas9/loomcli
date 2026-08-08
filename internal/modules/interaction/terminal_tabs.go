package interaction

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

var validTabSessionName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// TabMetadata is Interaction's persisted terminal-tab projection. PTYAlive
// and AttachedClients are populated by the service at read time and are not
// persisted by the outbound adapter.
type TabMetadata struct {
	SessionName                  string      `json:"session_name"`
	Workspace                    string      `json:"workspace,omitempty"`
	Label                        string      `json:"label"`
	Notes                        string      `json:"notes"`
	SortOrder                    int         `json:"sort_order"`
	Pinned                       bool        `json:"pinned"`
	IssueID                      string      `json:"issue_id,omitempty"`
	Kind                         string      `json:"kind,omitempty"`
	AgentID                      string      `json:"agent_id,omitempty"`
	Role                         string      `json:"role,omitempty"`
	Backend                      string      `json:"backend,omitempty"`
	InteractionSessionID         string      `json:"interaction_session_id,omitempty"`
	InteractionTerminalID        string      `json:"interaction_terminal_id,omitempty"`
	InteractionLeaseID           string      `json:"interaction_lease_id,omitempty"`
	InteractionLeaseFencingToken int64       `json:"interaction_lease_fencing_token,omitempty"`
	Writable                     bool        `json:"writable,omitempty"`
	Launch                       *LaunchSpec `json:"launch,omitempty"`
	CreatedAt                    time.Time   `json:"created_at"`
	UpdatedAt                    time.Time   `json:"updated_at"`
	PTYAlive                     bool        `json:"pty_alive"`
	AttachedClients              int         `json:"attached_clients"`
}

// LaunchSpec is the explicit command contract for a terminal session.
type LaunchSpec struct {
	Argv []string          `json:"argv,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	Cwd  string            `json:"cwd,omitempty"`
}

// TabMetadataReader is the narrow read port used by terminal event delivery.
type TabMetadataReader interface {
	Get(context.Context, string, string) (*TabMetadata, error)
}

// TabMetadataStore is the outbound persistence port used by terminal services.
// Its concrete Redis implementation lives in localredis.
type TabMetadataStore interface {
	TabMetadataReader
	List(context.Context, string) ([]TabMetadata, error)
	ListAll(context.Context) ([]TabMetadata, error)
	Set(context.Context, *TabMetadata) error
	Patch(context.Context, string, string, map[string]string) (*TabMetadata, error)
	Delete(context.Context, string, string) error
	EnsureDefaults(context.Context, string, []string) ([]TabMetadata, error)
	ListByIssue(context.Context, string) ([]TabMetadata, error)
	ListIssueSessionMap(context.Context) (map[string][]string, error)
}

// ValidateTerminalSessionName returns an error if a terminal tab session name is
// empty or contains unsupported characters.
func ValidateTerminalSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("session name is required")
	}
	if !validTabSessionName.MatchString(name) {
		return fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	return nil
}
