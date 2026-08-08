package terminal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

const interactiveSessionLeaseTTL = 2 * time.Minute

// terminalInteractionLifecycle keeps the raw session credential only while
// the server launches the child. Every command derives a fresh one-command
// SessionAuthority; Close clears both the byte copy and the ephemeral launch
// overlay. Neither value is written to tab metadata or ordinary logs.
type terminalInteractionLifecycle struct {
	api         interaction.API
	resolver    InteractionSessionAuthorityResolver
	workspace   string
	sessionID   string
	agentID     string
	terminalID  string
	nodeID      string
	leaseID     string
	fence       int64
	rawToken    []byte
	launch      *tabmeta.LaunchSpec
	terminalSet bool
}

//nolint:cyclop,funlen // Keep session creation, terminal binding, lease transitions, and cleanup in one owner-fenced launch transaction.
func prepareTerminalInteractionLaunch(
	ctx context.Context,
	params *terminalWSParams,
	key webuterminal.SessionKey,
	agentID string,
	persisted *tabmeta.LaunchSpec,
	operator *authority.OperatorAuthority,
) (*tabmeta.LaunchSpec, *terminalInteractionLifecycle, error) {
	if params == nil || params.interaction.API == nil ||
		params.interaction.SessionAuthorities == nil || operator == nil {
		return nil, nil, fmt.Errorf(
			"interaction session lifecycle unavailable: %w",
			interaction.ErrUnavailable,
		)
	}
	workspace := strings.TrimSpace(key.Workspace)
	agentID = strings.TrimSpace(agentID)
	if workspace == "" || agentID == "" || persisted == nil ||
		strings.TrimSpace(params.interactionNode) == "" ||
		strings.TrimSpace(params.loomServerURL) == "" {
		return nil, nil, fmt.Errorf(
			"complete interactive launch identity is required: %w",
			interaction.ErrInvalid,
		)
	}

	meta, err := loadTerminalInteractionMetadata(ctx, params, key)
	if err != nil {
		return nil, nil, err
	}
	hasPriorSession := strings.TrimSpace(meta.InteractionSessionID) != ""
	hasPriorTerminal := strings.TrimSpace(meta.InteractionTerminalID) != ""
	hasPriorLease := strings.TrimSpace(meta.InteractionLeaseID) != "" ||
		meta.InteractionLeaseFencingToken != 0
	if hasPriorSession || hasPriorTerminal || hasPriorLease {
		if params.manager == nil {
			return nil, nil, fmt.Errorf(
				"terminal manager unavailable for prior lifecycle convergence: %w",
				interaction.ErrUnavailable,
			)
		}
		// A server restart loses the process-local PTY but not the exact
		// Interaction IDs in tab metadata. Force-converge that prior
		// generation through the central kill hook before replacing the IDs
		// with a successor session.
		if err := params.manager.Kill(key); err != nil {
			return nil, nil, fmt.Errorf(
				"converge prior interactive lifecycle before replacement: %w",
				err,
			)
		}
	}
	sessionID := "session-" + uuid.NewString()
	terminalID := "terminal-" + uuid.NewString()
	leaseID := "lease-" + uuid.NewString()
	start, err := params.interaction.API.StartSession(
		ctx,
		*operator,
		interaction.StartSessionCommand{
			WorkspaceKey: workspace,
			SessionID:    sessionID,
			AgentID:      agentID,
			NodeID:       params.interactionNode,
			Kind:         interaction.SessionKindInteractive,
			TaskID:       meta.IssueID,
			TerminalID:   terminalID,
			Phase:        "launching",
			Attempt:      1,
			LeaseID:      leaseID,
			LeaseTTL:     interactiveSessionLeaseTTL,
			Metadata:     terminalInteractionMetadata(key, meta),
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start interactive AgentSession: %w", err)
	}
	if start.Session == nil || start.Lease == nil || start.Token == nil {
		if start.Token != nil {
			start.Token.Close()
		}
		return nil, nil, fmt.Errorf(
			"interaction start omitted session authority material: %w",
			interaction.ErrInvalidPersistedState,
		)
	}

	lifecycle := &terminalInteractionLifecycle{
		api: params.interaction.API, resolver: params.interaction.SessionAuthorities,
		workspace: start.Session.WorkspaceKey, sessionID: start.Session.SessionID,
		agentID: start.Session.AgentID, terminalID: start.Session.TerminalID,
		nodeID: start.Lease.NodeID, leaseID: start.Lease.LeaseID,
		fence: start.Lease.FencingToken, rawToken: start.Token.Bytes(),
	}
	start.Token.Close()
	if len(lifecycle.rawToken) == 0 {
		lifecycle.Close()
		return nil, nil, fmt.Errorf(
			"interaction start returned an empty one-use credential: %w",
			interaction.ErrInvalidPersistedState,
		)
	}

	sessionAuth, err := lifecycle.resolve(ctx, interaction.ActionOpenTerminal)
	if err != nil {
		lifecycle.abort(ctx, "terminal_authority_failed")
		return nil, nil, fmt.Errorf("derive terminal-open authority: %w", err)
	}
	defer sessionAuth.SessionOwner().CloseLeaseCredential()
	_, err = lifecycle.api.OpenTerminal(ctx, sessionAuth, interaction.OpenTerminalCommand{
		WorkspaceKey: lifecycle.workspace,
		TerminalID:   lifecycle.terminalID,
		SessionID:    lifecycle.sessionID,
		AgentID:      lifecycle.agentID,
		NodeID:       lifecycle.nodeID,
		TaskID:       meta.IssueID,
		Title:        terminalInteractionTitle(meta, agentID),
		Kind:         "agent",
		PTYProvider:  "local-pty",
		StreamRef:    "terminal:" + key.String(),
		Metadata:     terminalInteractionMetadata(key, meta),
	})
	if err != nil {
		lifecycle.abort(ctx, "terminal_open_failed")
		return nil, nil, fmt.Errorf("open Interaction terminal: %w", err)
	}
	lifecycle.terminalSet = true
	if params.interaction.TerminalIdentities != nil {
		meta.Workspace = key.Workspace
		meta.SessionName = key.Name
		meta.InteractionSessionID = lifecycle.sessionID
		meta.InteractionTerminalID = lifecycle.terminalID
		meta.InteractionLeaseID = lifecycle.leaseID
		meta.InteractionLeaseFencingToken = lifecycle.fence
		if err := params.interaction.TerminalIdentities.PersistInteractionTabIdentity(ctx, key.Workspace, meta); err != nil {
			lifecycle.abort(ctx, "terminal_identity_persist_failed")
			return nil, nil, fmt.Errorf(
				"persist Interaction terminal identity: %w",
				err,
			)
		}
	}

	launch := cloneTerminalLaunch(persisted)
	token := interaction.NewLeaseToken(lifecycle.rawToken)
	envelope, err := interaction.SessionEnvelope(sessionAuth, token)
	token.Close()
	if err != nil {
		lifecycle.abort(ctx, "terminal_envelope_failed")
		return nil, nil, err
	}
	envelope[interaction.EnvInteractionAPIURL] = strings.TrimRight(params.loomServerURL, "/")
	if launch.Env == nil {
		launch.Env = make(map[string]string, len(envelope))
	}
	for name, value := range envelope {
		launch.Env[name] = value
	}
	lifecycle.launch = launch
	return launch, lifecycle, nil
}

func (lifecycle *terminalInteractionLifecycle) resolve(
	ctx context.Context,
	action authority.Action,
) (authority.SessionAuthority, error) {
	if lifecycle == nil || lifecycle.resolver == nil || len(lifecycle.rawToken) == 0 {
		return authority.SessionAuthority{}, interaction.ErrUnavailable
	}
	token := interaction.NewLeaseToken(lifecycle.rawToken)
	auth, err := lifecycle.resolver.ResolveSessionAuthority(
		ctx,
		action,
		interaction.SessionAuthorityProof{
			WorkspaceKey: lifecycle.workspace,
			SessionID:    lifecycle.sessionID,
			AgentID:      lifecycle.agentID,
			TerminalID:   lifecycle.terminalID,
			NodeID:       lifecycle.nodeID,
			LeaseID:      lifecycle.leaseID,
			FencingToken: lifecycle.fence,
			Token:        token,
		},
	)
	token.Close()
	return auth, err
}

func (lifecycle *terminalInteractionLifecycle) running(
	ctx context.Context,
	_ webuterminal.Attachment,
) error {
	auth, err := lifecycle.resolve(ctx, interaction.ActionUpdateTerminal)
	if err != nil {
		return fmt.Errorf("derive terminal-running authority: %w", err)
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	attached := 1
	_, err = lifecycle.api.UpdateTerminal(ctx, auth, interaction.UpdateTerminalCommand{
		WorkspaceKey:    lifecycle.workspace,
		TerminalID:      lifecycle.terminalID,
		Status:          interaction.TerminalRunning,
		AttachedClients: &attached,
	})
	if err != nil {
		return fmt.Errorf("mark Interaction terminal running: %w", err)
	}
	return nil
}

func (lifecycle *terminalInteractionLifecycle) fail(ctx context.Context, errorClass string) {
	if lifecycle == nil {
		return
	}
	auth, err := lifecycle.resolve(ctx, interaction.ActionFinishSession)
	if err != nil {
		slog.Warn("derive Interaction finish authority during terminal launch cleanup",
			"workspace", lifecycle.workspace, "session_id", lifecycle.sessionID,
			"terminal_opened", lifecycle.terminalSet, "err", err)
		return
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	if _, err = lifecycle.api.FinishSession(ctx, auth, interaction.FinishSessionCommand{
		WorkspaceKey: lifecycle.workspace,
		SessionID:    lifecycle.sessionID,
		Status:       interaction.SessionFailed,
		ErrorClass:   strings.TrimSpace(errorClass),
	}); err != nil {
		// If OpenTerminal never committed, there is no TerminalSession to
		// terminalize. The session lease will still expire into normal
		// Interaction recovery; report that distinct pre-open cleanup gap.
		slog.Warn("finish Interaction lifecycle during terminal launch cleanup",
			"workspace", lifecycle.workspace, "session_id", lifecycle.sessionID,
			"terminal_opened", lifecycle.terminalSet, "err", err)
	}
}

func (lifecycle *terminalInteractionLifecycle) abort(ctx context.Context, errorClass string) {
	lifecycle.fail(ctx, errorClass)
	lifecycle.Close()
}

func (lifecycle *terminalInteractionLifecycle) Close() {
	if lifecycle == nil {
		return
	}
	clear(lifecycle.rawToken)
	lifecycle.rawToken = nil
	if lifecycle.launch != nil && lifecycle.launch.Env != nil {
		for name := range lifecycle.launch.Env {
			if strings.HasPrefix(name, "LOOM_SESSION_") ||
				name == interaction.EnvInteractionAPIURL {
				delete(lifecycle.launch.Env, name)
			}
		}
	}
}

func loadTerminalInteractionMetadata(
	ctx context.Context,
	params *terminalWSParams,
	key webuterminal.SessionKey,
) (*tabmeta.TabMetadata, error) {
	if params.tabMetaStore == nil {
		return &tabmeta.TabMetadata{}, nil
	}
	meta, err := params.tabMetaStore.Get(ctx, key.Workspace, key.Name)
	if err != nil {
		return nil, fmt.Errorf("load terminal launch metadata: %w", err)
	}
	if meta == nil {
		return nil, errors.New("terminal launch metadata disappeared before Interaction start")
	}
	return meta, nil
}

func terminalInteractionMetadata(
	key webuterminal.SessionKey,
	meta *tabmeta.TabMetadata,
) map[string]string {
	values := map[string]string{
		"source":       "web_terminal",
		"terminal_tab": key.Name,
	}
	if meta != nil {
		if value := strings.TrimSpace(meta.Role); value != "" {
			values["role"] = value
		}
		if value := strings.TrimSpace(meta.Backend); value != "" {
			values["backend"] = value
		}
	}
	return values
}

func terminalInteractionTitle(meta *tabmeta.TabMetadata, agentID string) string {
	if meta != nil {
		if value := strings.TrimSpace(meta.Label); value != "" {
			return value
		}
	}
	return agentID
}

func cloneTerminalLaunch(value *tabmeta.LaunchSpec) *tabmeta.LaunchSpec {
	if value == nil {
		return nil
	}
	result := &tabmeta.LaunchSpec{
		Argv: append([]string(nil), value.Argv...),
		Cwd:  value.Cwd,
	}
	if value.Env != nil {
		result.Env = make(map[string]string, len(value.Env))
		for key, item := range value.Env {
			result.Env[key] = item
		}
	}
	return result
}
