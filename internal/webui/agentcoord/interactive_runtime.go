package agentcoord

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type canonicalInteractiveAgentRuntime struct {
	runtime InteractiveRuntimeController
}

// NewCanonicalInteractiveAgentRuntime projects the local PTY controller into
// the narrow lifecycle port used by canonical Agents HTTP commands.
func NewCanonicalInteractiveAgentRuntime(runtime InteractiveRuntimeController) InteractiveAgentRuntime {
	return &canonicalInteractiveAgentRuntime{runtime: runtime}
}

func (runtime *canonicalInteractiveAgentRuntime) StopAgent(
	ctx context.Context,
	workspace,
	agentID string,
) error {
	if runtime == nil || runtime.runtime == nil {
		return nil
	}
	sessions, err := runtime.runtime.OwnedAgentSessions(ctx, workspace, agentID)
	if err != nil {
		return fmt.Errorf("list owned interactive runtimes: %w", err)
	}
	for _, session := range sessions {
		if err := runtime.runtime.Kill(session.Key); err != nil {
			return fmt.Errorf("stop owned interactive runtime %s: %w", session.Key.String(), err)
		}
	}
	return nil
}

// InteractiveRuntimeSession is one process-local terminal placement whose
// ownership was established from server-owned tab metadata, not from a
// caller-supplied Fleet AgentSession.TerminalID.
type InteractiveRuntimeSession struct {
	Key                   interaction.TerminalKey
	InteractionSessionID  string
	InteractionTerminalID string
	StreamRef             string
	Live                  bool
	Closed                bool
}

// InteractiveRuntimeController is the process-local ownership seam for
// interactive agent terminals. Interactive agents run in the web terminal's
// PTY manager under Interaction runtime ownership.
type InteractiveRuntimeController interface {
	OwnedAgentSessions(ctx context.Context, workspace, agentID string) ([]InteractiveRuntimeSession, error)
	Kill(key interaction.TerminalKey) error
}

// InteractiveRuntimeTab is the minimum server-owned tab metadata needed to
// bind an interactive agent assignment to a process-local PTY.
type InteractiveRuntimeTab struct {
	SessionName           string
	Kind                  string
	AgentID               string
	InteractionSessionID  string
	InteractionTerminalID string
	PTYAlive              bool
}

// InteractiveRuntimeTabSource is the server-owned tab-metadata read surface
// used to bind a PTY session name to its agent assignment.
type InteractiveRuntimeTabSource interface {
	ListInteractiveRuntimeTabs(ctx context.Context, workspace string) ([]InteractiveRuntimeTab, error)
}

// InteractiveRuntimePTYSource is the process-local PTY surface required by
// lifecycle control.
type InteractiveRuntimePTYSource interface {
	IsLive(key interaction.TerminalKey) bool
	IsClosed(key interaction.TerminalKey) bool
	Kill(key interaction.TerminalKey) error
}

type tabOwnedInteractiveRuntime struct {
	tabs InteractiveRuntimeTabSource
	ptys InteractiveRuntimePTYSource
}

// NewInteractiveRuntimeController binds process-local PTYs to agents through
// the web server's own tab metadata. Fleet session rows remain durable
// lifecycle evidence, but never grant authority to kill an arbitrary PTY.
func NewInteractiveRuntimeController(
	tabs InteractiveRuntimeTabSource,
	ptys InteractiveRuntimePTYSource,
) InteractiveRuntimeController {
	return &tabOwnedInteractiveRuntime{tabs: tabs, ptys: ptys}
}

func (r *tabOwnedInteractiveRuntime) OwnedAgentSessions(
	ctx context.Context,
	workspace string,
	agentID string,
) ([]InteractiveRuntimeSession, error) {
	if r == nil || r.tabs == nil || r.ptys == nil {
		return nil, nil
	}
	tabs, err := r.tabs.ListInteractiveRuntimeTabs(ctx, workspace)
	if err != nil {
		return nil, err
	}

	seen := make(map[interaction.TerminalKey]struct{}, len(tabs))
	owned := make([]InteractiveRuntimeSession, 0, len(tabs))
	for i := range tabs {
		tab := &tabs[i]
		if tab.Kind != "agent" || tab.AgentID != agentID || strings.TrimSpace(tab.SessionName) == "" {
			continue
		}
		key := interaction.TerminalKey{WorkspaceKey: workspace, TerminalID: tab.SessionName}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		live := r.ptys.IsLive(key)
		closed := r.ptys.IsClosed(key)
		// ListTabs marks metadata created by this server process attachable even
		// before the first WebSocket creates its PTY. Preserve that key so a
		// Stop can fence the startup window with a second idempotent Kill.
		hasInteractionIdentity := strings.TrimSpace(tab.InteractionSessionID) != "" &&
			strings.TrimSpace(tab.InteractionTerminalID) != ""
		if !live && !closed && !tab.PTYAlive && !hasInteractionIdentity {
			continue
		}
		seen[key] = struct{}{}
		owned = append(owned, InteractiveRuntimeSession{
			Key: key, InteractionSessionID: tab.InteractionSessionID,
			InteractionTerminalID: tab.InteractionTerminalID,
			StreamRef:             "terminal:" + key.String(),
			Live:                  live, Closed: closed,
		})
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].Key.TerminalID < owned[j].Key.TerminalID })
	return owned, nil
}

func (r *tabOwnedInteractiveRuntime) Kill(key interaction.TerminalKey) error {
	if r == nil || r.ptys == nil {
		return nil
	}
	return r.ptys.Kill(key)
}
