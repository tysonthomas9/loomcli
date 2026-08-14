package interaction

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const interactiveTerminalLeaseTTL = 2 * time.Minute

type terminalWorkspaceRegistrar interface {
	EnsureRegistered(workspaceKey, path string) error
}

// PlanTerminalAttach applies Interaction's capacity and session-generation
// policy before the delivery adapter upgrades an HTTP request to WebSocket.
func (s *TerminalTabService) PlanTerminalAttach(
	ctx context.Context,
	command TerminalAttachCommand,
) (TerminalAttachPlan, error) {
	if s == nil || s.runtime == nil || s.tabStore == nil {
		return TerminalAttachPlan{}, terminalError(ErrUnavailable, "terminal runtime is unavailable", nil)
	}
	key, err := terminalAttachKey(command)
	if err != nil {
		return TerminalAttachPlan{}, err
	}
	if !s.runtime.IsLive(key) &&
		s.runtime.SessionCountFor(key.WorkspaceKey) >= s.runtime.MaxSessions() {
		return TerminalAttachPlan{}, terminalError(ErrTerminalCapacity, "maximum terminal sessions reached", nil)
	}
	if s.runtime.IsLive(key) {
		return TerminalAttachPlan{}, nil
	}
	meta, err := s.tabStore.Get(ctx, key.WorkspaceKey, key.TerminalID)
	if err != nil {
		return TerminalAttachPlan{}, terminalError(ErrUnavailable, "failed to load terminal metadata", err)
	}
	return TerminalAttachPlan{
		StartAuthorityRequired: meta != nil && meta.Kind == terminalKindAgent,
	}, nil
}

// AttachTerminal owns launch authorization, AgentSession creation, terminal
// opening, PTY attachment, and post-spawn desired-state fencing. Delivery owns
// only WebSocket framing around the returned attachment.
func (s *TerminalTabService) AttachTerminal(
	ctx context.Context,
	command TerminalAttachCommand,
) (*TerminalAttachResult, error) {
	if s == nil || s.runtime == nil {
		return nil, terminalError(ErrUnavailable, "terminal runtime is unavailable", nil)
	}
	key, err := terminalAttachKey(command)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTerminalWorkspace(ctx, key.WorkspaceKey); err != nil {
		return nil, err
	}
	resolved, unlock, err := s.resolveAttachLaunch(ctx, key)
	if err != nil {
		return nil, err
	}
	if unlock != nil {
		defer unlock()
	}
	return s.attachResolvedTerminal(ctx, key, command, resolved)
}

func (s *TerminalTabService) resolveAttachLaunch(
	ctx context.Context,
	key TerminalKey,
) (*resolvedTerminalLaunch, func(), error) {
	resolved, err := s.resolveTerminalLaunch(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	agentID := strings.TrimSpace(resolved.AgentID)
	if agentID == "" {
		return resolved, nil, nil
	}
	unlock := LockAgentLifecycle(key.WorkspaceKey, agentID)
	resolved, err = s.resolveTerminalLaunch(ctx, key)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return resolved, unlock, nil
}

func (s *TerminalTabService) attachResolvedTerminal(
	ctx context.Context,
	key TerminalKey,
	command TerminalAttachCommand,
	resolved *resolvedTerminalLaunch,
) (*TerminalAttachResult, error) {
	agentID := strings.TrimSpace(resolved.AgentID)
	launch := resolved.Launch
	var lifecycle *terminalLaunchLifecycle
	var err error
	if agentID != "" && !s.runtime.IsLive(key) {
		launch, lifecycle, err = s.prepareTerminalLaunch(
			ctx, key, agentID, launch, command.StartAuthority,
		)
		if err != nil {
			return nil, err
		}
		defer lifecycle.Close()
	}
	attachment, reattached, err := s.runtime.Attach(
		key, command.Columns, command.Rows, launch,
	)
	if err != nil {
		if lifecycle != nil {
			lifecycle.fail(ctx, "terminal_spawn_failed")
		}
		return nil, err
	}
	if lifecycle != nil {
		if err := lifecycle.running(ctx); err != nil {
			_ = s.runtime.Kill(key)
			lifecycle.fail(ctx, "terminal_running_transition_failed")
			return nil, err
		}
	}
	if err := s.fenceAttachedAgentTerminal(ctx, key, agentID); err != nil {
		return nil, err
	}
	return &TerminalAttachResult{Attachment: attachment, Reattached: reattached}, nil
}

func (s *TerminalTabService) fenceAttachedAgentTerminal(
	ctx context.Context,
	key TerminalKey,
	agentID string,
) error {
	if agentID == "" {
		return nil
	}
	if _, err := s.resolveTerminalLaunch(ctx, key); err != nil {
		if killErr := s.runtime.Kill(key); killErr != nil {
			return fmt.Errorf("%w; additionally failed to fence spawned PTY: %v", err, killErr)
		}
		return err
	}
	return nil
}

func (s *TerminalTabService) DetachTerminal(
	_ context.Context,
	workspaceKey, terminalID, attachmentID string,
) {
	if s == nil || s.runtime == nil {
		return
	}
	s.runtime.Detach(
		TerminalKey{WorkspaceKey: workspaceKey, TerminalID: terminalID},
		attachmentID,
	)
}

func terminalAttachKey(command TerminalAttachCommand) (TerminalKey, error) {
	key := TerminalKey{
		WorkspaceKey: strings.TrimSpace(command.WorkspaceKey),
		TerminalID:   strings.TrimSpace(command.TerminalID),
	}
	if key.WorkspaceKey == "" || ValidateTerminalSessionName(key.TerminalID) != nil {
		return TerminalKey{}, terminalError(ErrInvalid, "valid workspace and terminal are required", nil)
	}
	return key, nil
}

func (s *TerminalTabService) ensureTerminalWorkspace(ctx context.Context, workspaceKey string) error {
	registrar, ok := s.runtime.(terminalWorkspaceRegistrar)
	if !ok {
		return nil
	}
	if s.agentTerminal.Placement == nil {
		return terminalError(ErrTerminalPlacement, "terminal workspace placement is unavailable", nil)
	}
	path := strings.TrimSpace(s.agentTerminal.Placement.WorkspacePath(ctx, workspaceKey))
	if path == "" {
		return terminalError(ErrTerminalPlacement, "terminal workspace is unavailable", nil)
	}
	if err := registrar.EnsureRegistered(workspaceKey, path); err != nil {
		return terminalError(ErrTerminalPlacement, "failed to register terminal workspace", err)
	}
	return nil
}

type terminalLaunchLifecycle struct {
	api         API
	resolver    SessionAuthorityResolver
	workspace   string
	sessionID   string
	agentID     string
	terminalID  string
	nodeID      string
	leaseID     string
	fence       int64
	rawToken    []byte
	launch      *LaunchSpec
	terminalSet bool
}

//nolint:cyclop,funlen // One owner-fenced transaction creates the session, terminal, credential envelope, and durable tab identity.
func (s *TerminalTabService) prepareTerminalLaunch(
	ctx context.Context,
	key TerminalKey,
	agentID string,
	persisted *LaunchSpec,
	operator *authority.OperatorAuthority,
) (*LaunchSpec, *terminalLaunchLifecycle, error) {
	deps := s.agentTerminal.Sessions
	if deps.API == nil || deps.Authorities == nil || operator == nil ||
		strings.TrimSpace(deps.NodeID) == "" || strings.TrimSpace(deps.APIURL) == "" {
		return nil, nil, terminalError(ErrUnavailable, "Interaction session lifecycle is unavailable", nil)
	}
	if persisted == nil || len(persisted.Argv) == 0 {
		return nil, nil, terminalError(ErrTerminalLaunchMissing, "agent terminal launch metadata missing", nil)
	}
	meta, err := s.tabStore.Get(ctx, key.WorkspaceKey, key.TerminalID)
	if err != nil {
		return nil, nil, terminalError(ErrUnavailable, "failed to load terminal lifecycle identity", err)
	}
	if meta == nil {
		return nil, nil, terminalError(ErrInvalidPersistedState, "terminal launch metadata disappeared", nil)
	}
	if hasTerminalLifecycleIdentity(meta) {
		if err := s.runtime.Kill(key); err != nil {
			return nil, nil, terminalError(ErrUnavailable, "failed to converge prior terminal lifecycle", err)
		}
	}
	sessionID, err := prefixedTerminalUUID("session-")
	if err != nil {
		return nil, nil, err
	}
	terminalID, err := prefixedTerminalUUID("terminal-")
	if err != nil {
		return nil, nil, err
	}
	leaseID, err := prefixedTerminalUUID("lease-")
	if err != nil {
		return nil, nil, err
	}
	start, err := deps.API.StartSession(ctx, *operator, StartSessionCommand{
		WorkspaceKey: key.WorkspaceKey, SessionID: sessionID, AgentID: agentID,
		NodeID: deps.NodeID, Kind: SessionKindInteractive, TaskID: meta.IssueID,
		TerminalID: terminalID, Phase: "launching", Attempt: 1,
		LeaseID: leaseID, LeaseTTL: interactiveTerminalLeaseTTL,
		Metadata: terminalLifecycleMetadata(key, meta),
	})
	if err != nil {
		return nil, nil, err
	}
	if start.Session == nil || start.Lease == nil || start.Token == nil {
		if start.Token != nil {
			start.Token.Close()
		}
		return nil, nil, terminalError(ErrInvalidPersistedState, "session start omitted authority material", nil)
	}
	lifecycle := &terminalLaunchLifecycle{
		api: deps.API, resolver: deps.Authorities,
		workspace: start.Session.WorkspaceKey, sessionID: start.Session.SessionID,
		agentID: start.Session.AgentID, terminalID: start.Session.TerminalID,
		nodeID: start.Lease.NodeID, leaseID: start.Lease.LeaseID,
		fence: start.Lease.FencingToken, rawToken: start.Token.Bytes(),
	}
	start.Token.Close()
	if len(lifecycle.rawToken) == 0 {
		lifecycle.Close()
		return nil, nil, terminalError(ErrInvalidPersistedState, "session start returned an empty credential", nil)
	}
	sessionAuth, err := lifecycle.resolve(ctx, ActionOpenTerminal)
	if err != nil {
		lifecycle.abort(ctx, "terminal_authority_failed")
		return nil, nil, err
	}
	defer sessionAuth.SessionOwner().CloseLeaseCredential()
	_, err = deps.API.OpenTerminal(ctx, sessionAuth, OpenTerminalCommand{
		WorkspaceKey: lifecycle.workspace, TerminalID: lifecycle.terminalID,
		SessionID: lifecycle.sessionID, AgentID: lifecycle.agentID,
		NodeID: lifecycle.nodeID, TaskID: meta.IssueID,
		Title: terminalLifecycleTitle(meta, agentID), Kind: terminalKindAgent,
		PTYProvider: "local-pty", StreamRef: "terminal:" + key.String(),
		Metadata: terminalLifecycleMetadata(key, meta),
	})
	if err != nil {
		lifecycle.abort(ctx, "terminal_open_failed")
		return nil, nil, err
	}
	lifecycle.terminalSet = true
	meta.InteractionSessionID = lifecycle.sessionID
	meta.InteractionTerminalID = lifecycle.terminalID
	meta.InteractionLeaseID = lifecycle.leaseID
	meta.InteractionLeaseFencingToken = lifecycle.fence
	if err := s.persistInteractionTabIdentity(ctx, key.WorkspaceKey, meta); err != nil {
		lifecycle.abort(ctx, "terminal_identity_persist_failed")
		return nil, nil, err
	}

	launch := cloneLaunchSpec(persisted)
	token := NewLeaseToken(lifecycle.rawToken)
	envelope, err := SessionEnvelope(sessionAuth, token)
	token.Close()
	if err != nil {
		lifecycle.abort(ctx, "terminal_envelope_failed")
		return nil, nil, err
	}
	envelope[EnvInteractionAPIURL] = strings.TrimRight(deps.APIURL, "/")
	if launch.Env == nil {
		launch.Env = make(map[string]string, len(envelope))
	}
	for name, value := range envelope {
		launch.Env[name] = value
	}
	lifecycle.launch = launch
	return launch, lifecycle, nil
}

func prefixedTerminalUUID(prefix string) (string, error) {
	value, err := NewUUID()
	if err != nil {
		return "", terminalError(ErrUnavailable, "failed to generate terminal lifecycle identity", err)
	}
	return prefix + value, nil
}

func hasTerminalLifecycleIdentity(meta *TabMetadata) bool {
	return meta != nil && (strings.TrimSpace(meta.InteractionSessionID) != "" ||
		strings.TrimSpace(meta.InteractionTerminalID) != "" ||
		strings.TrimSpace(meta.InteractionLeaseID) != "" ||
		meta.InteractionLeaseFencingToken != 0)
}

func (lifecycle *terminalLaunchLifecycle) resolve(
	ctx context.Context,
	action authority.Action,
) (authority.SessionAuthority, error) {
	if lifecycle == nil || lifecycle.resolver == nil || len(lifecycle.rawToken) == 0 {
		return authority.SessionAuthority{}, ErrUnavailable
	}
	token := NewLeaseToken(lifecycle.rawToken)
	auth, err := lifecycle.resolver.ResolveSessionAuthority(ctx, action, SessionAuthorityProof{
		WorkspaceKey: lifecycle.workspace, SessionID: lifecycle.sessionID,
		AgentID: lifecycle.agentID, TerminalID: lifecycle.terminalID,
		NodeID: lifecycle.nodeID, LeaseID: lifecycle.leaseID,
		FencingToken: lifecycle.fence, Token: token,
	})
	token.Close()
	return auth, err
}

func (lifecycle *terminalLaunchLifecycle) running(ctx context.Context) error {
	auth, err := lifecycle.resolve(ctx, ActionUpdateTerminal)
	if err != nil {
		return fmt.Errorf("derive terminal-running authority: %w", err)
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	attached := 1
	_, err = lifecycle.api.UpdateTerminal(ctx, auth, UpdateTerminalCommand{
		WorkspaceKey: lifecycle.workspace, TerminalID: lifecycle.terminalID,
		Status: TerminalRunning, AttachedClients: &attached,
	})
	return err
}

func (lifecycle *terminalLaunchLifecycle) fail(ctx context.Context, errorClass string) {
	if lifecycle == nil {
		return
	}
	auth, err := lifecycle.resolve(ctx, ActionFinishSession)
	if err != nil {
		slog.Warn("derive Interaction finish authority during terminal cleanup",
			"workspace", lifecycle.workspace, "session_id", lifecycle.sessionID, "err", err)
		return
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	if _, err := lifecycle.api.FinishSession(ctx, auth, FinishSessionCommand{
		WorkspaceKey: lifecycle.workspace, SessionID: lifecycle.sessionID,
		Status: SessionFailed, ErrorClass: strings.TrimSpace(errorClass),
	}); err != nil {
		slog.Warn("finish Interaction lifecycle during terminal cleanup",
			"workspace", lifecycle.workspace, "session_id", lifecycle.sessionID,
			"terminal_opened", lifecycle.terminalSet, "err", err)
	}
}

func (lifecycle *terminalLaunchLifecycle) abort(ctx context.Context, errorClass string) {
	lifecycle.fail(ctx, errorClass)
	lifecycle.Close()
}

func (lifecycle *terminalLaunchLifecycle) Close() {
	if lifecycle == nil {
		return
	}
	clear(lifecycle.rawToken)
	lifecycle.rawToken = nil
	if lifecycle.launch != nil && lifecycle.launch.Env != nil {
		for name := range lifecycle.launch.Env {
			if strings.HasPrefix(name, "LOOM_SESSION_") || name == EnvInteractionAPIURL {
				delete(lifecycle.launch.Env, name)
			}
		}
	}
}

func terminalLifecycleMetadata(key TerminalKey, meta *TabMetadata) map[string]string {
	values := map[string]string{"source": "web_terminal", "terminal_tab": key.TerminalID}
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

func terminalLifecycleTitle(meta *TabMetadata, agentID string) string {
	if meta != nil && strings.TrimSpace(meta.Label) != "" {
		return strings.TrimSpace(meta.Label)
	}
	return agentID
}
