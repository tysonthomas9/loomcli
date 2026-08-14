package interaction

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const terminalConnectionTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type terminalConnectionAttachmentStub struct{}

func (terminalConnectionAttachmentStub) ConnID() string                       { return "conn-1" }
func (terminalConnectionAttachmentStub) Output() <-chan []byte                { return make(chan []byte) }
func (terminalConnectionAttachmentStub) WriteInput(value []byte) (int, error) { return len(value), nil }
func (terminalConnectionAttachmentStub) Replay() []TerminalReplayEvent {
	return []TerminalReplayEvent{{Output: []byte("replay")}}
}
func (terminalConnectionAttachmentStub) Resize(string, uint16, uint16) error { return nil }
func (terminalConnectionAttachmentStub) ExitReason() string                  { return "" }

type terminalConnectionAPIStub struct {
	startCommand StartSessionCommand
	openCommand  OpenTerminalCommand
	updates      []UpdateTerminalCommand
	finishes     []FinishSessionCommand
	startErr     error
	openErr      error
}

func (stub *terminalConnectionAPIStub) StartSession(_ context.Context, _ authority.OperatorAuthority, command StartSessionCommand) (SessionStart, error) {
	stub.startCommand = command
	if stub.startErr != nil {
		return SessionStart{}, stub.startErr
	}
	return SessionStart{
		Session: &AgentSession{WorkspaceKey: command.WorkspaceKey, SessionID: command.SessionID, AgentID: command.AgentID, NodeID: command.NodeID, TerminalID: command.TerminalID, Status: SessionRunning},
		Lease:   &SessionLease{WorkspaceKey: command.WorkspaceKey, SessionID: command.SessionID, AgentID: command.AgentID, NodeID: command.NodeID, LeaseID: command.LeaseID, FencingToken: 7, Status: "active", ExpiresAt: time.Now().Add(time.Hour)},
		Token:   NewLeaseToken([]byte(terminalConnectionTestToken)),
	}, nil
}
func (*terminalConnectionAPIStub) RecoverSessionStart(context.Context, authority.OperatorAuthority, RecoverSessionStartCommand) (SessionStart, error) {
	return SessionStart{}, ErrUnavailable
}
func (*terminalConnectionAPIStub) PatchSession(context.Context, authority.SessionAuthority, PatchSessionCommand) (*AgentSession, error) {
	return nil, nil
}
func (*terminalConnectionAPIStub) PublishTranscript(context.Context, authority.SessionAuthority, PublishTranscriptCommand) (*AgentSession, error) {
	return nil, nil
}
func (*terminalConnectionAPIStub) HeartbeatSession(context.Context, authority.SessionAuthority, HeartbeatSessionCommand) (*AgentSession, error) {
	return nil, nil
}
func (stub *terminalConnectionAPIStub) FinishSession(_ context.Context, _ authority.SessionAuthority, command FinishSessionCommand) (*AgentSession, error) {
	stub.finishes = append(stub.finishes, command)
	return &AgentSession{WorkspaceKey: command.WorkspaceKey, SessionID: command.SessionID, Status: command.Status}, nil
}
func (*terminalConnectionAPIStub) ForceInterrupt(context.Context, authority.SystemAuthority, ForceInterruptCommand) (ForceInterruptResult, error) {
	return ForceInterruptResult{}, nil
}
func (stub *terminalConnectionAPIStub) OpenTerminal(_ context.Context, _ authority.SessionAuthority, command OpenTerminalCommand) (*TerminalSession, error) {
	stub.openCommand = command
	if stub.openErr != nil {
		return nil, stub.openErr
	}
	return &TerminalSession{WorkspaceKey: command.WorkspaceKey, TerminalID: command.TerminalID, SessionID: command.SessionID, AgentID: command.AgentID, NodeID: command.NodeID, Status: TerminalStarting}, nil
}
func (stub *terminalConnectionAPIStub) UpdateTerminal(_ context.Context, _ authority.SessionAuthority, command UpdateTerminalCommand) (*TerminalSession, error) {
	stub.updates = append(stub.updates, command)
	return &TerminalSession{WorkspaceKey: command.WorkspaceKey, TerminalID: command.TerminalID, Status: command.Status}, nil
}
func (*terminalConnectionAPIStub) EnqueueInbox(context.Context, authority.OperatorAuthority, EnqueueInboxCommand) (*InboxMessage, error) {
	return nil, nil
}
func (*terminalConnectionAPIStub) ClaimInbox(context.Context, authority.SessionAuthority, ClaimInboxCommand) (*InboxMessage, error) {
	return nil, nil
}
func (*terminalConnectionAPIStub) CompleteInbox(context.Context, authority.SessionAuthority, CompleteInboxCommand) (*InboxMessage, error) {
	return nil, nil
}
func (*terminalConnectionAPIStub) ListActivity(context.Context, authority.OperatorAuthority, ActivityQuery) ([]Activity, error) {
	return nil, nil
}
func (*terminalConnectionAPIStub) ReconcileSessions(context.Context, authority.SystemAuthority, string, time.Time) (int, error) {
	return 0, nil
}

type terminalConnectionResolverStub struct {
	issuer  *authority.Issuer
	actions []authority.Action
	tokens  []string
}

func newTerminalConnectionResolverStub() *terminalConnectionResolverStub {
	return &terminalConnectionResolverStub{issuer: authority.NewIssuer()}
}

func (stub *terminalConnectionResolverStub) ResolveSessionAuthority(_ context.Context, action authority.Action, proof SessionAuthorityProof) (authority.SessionAuthority, error) {
	stub.actions = append(stub.actions, action)
	stub.tokens = append(stub.tokens, string(proof.Token.Bytes()))
	principal, err := stub.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "session:" + proof.SessionID, Class: authority.ClassSession,
		Workspace: proof.WorkspaceKey, Actions: []authority.Action{action},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		return authority.SessionAuthority{}, err
	}
	return stub.issuer.IssueSessionForOwner(principal, proof.WorkspaceKey, action, authority.SessionOwner{
		SessionID: proof.SessionID, AgentID: proof.AgentID, TerminalID: proof.TerminalID,
		NodeID: proof.NodeID, LeaseID: proof.LeaseID, FencingToken: proof.FencingToken,
	})
}

func newTerminalConnectionService(t *testing.T) (*TerminalTabService, *terminalStoreFake, *terminalRuntimeFake, *terminalConnectionAPIStub, *terminalConnectionResolverStub) {
	t.Helper()
	service, store, runtime, _ := newAgentTerminalService(t, agents.DesiredRunning, agents.RoleKindInteractive)
	api := &terminalConnectionAPIStub{}
	resolver := newTerminalConnectionResolverStub()
	service.agentTerminal.Sessions = AgentTerminalSessionDependencies{
		API: api, Authorities: resolver, NodeID: "node-terminal-test", APIURL: "http://127.0.0.1:8683",
	}
	runtime.attachResult = terminalConnectionAttachmentStub{}
	return service, store, runtime, api, resolver
}

func TestPlanTerminalAttachCombinesCapacityAndSessionAuthorityPolicy(t *testing.T) {
	service, _, runtime, _, _ := newTerminalConnectionService(t)
	meta, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := TerminalAttachCommand{WorkspaceKey: "WS", TerminalID: meta.SessionName}
	plan, err := service.PlanTerminalAttach(t.Context(), command)
	if err != nil || !plan.StartAuthorityRequired {
		t.Fatalf("fresh agent plan = %#v, err = %v", plan, err)
	}

	key := TerminalKey{WorkspaceKey: "WS", TerminalID: meta.SessionName}
	runtime.live[key] = true
	plan, err = service.PlanTerminalAttach(t.Context(), command)
	if err != nil || plan.StartAuthorityRequired {
		t.Fatalf("live reconnect plan = %#v, err = %v", plan, err)
	}

	delete(runtime.live, key)
	for i := 0; i < runtime.MaxSessions(); i++ {
		runtime.live[TerminalKey{WorkspaceKey: "WS", TerminalID: fmt.Sprintf("full-%d", i)}] = true
	}
	if _, err := service.PlanTerminalAttach(t.Context(), command); !errors.Is(err, ErrTerminalCapacity) {
		t.Fatalf("capacity plan error = %v, want ErrTerminalCapacity", err)
	}
}

func TestAttachTerminalOwnsFencedSessionAndKeepsCredentialEphemeral(t *testing.T) {
	service, store, runtime, api, resolver := newTerminalConnectionService(t)
	meta, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{WorkspaceKey: "WS", AgentID: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	persistedLaunch := cloneLaunchSpec(meta.Launch)
	result, err := service.AttachTerminal(t.Context(), TerminalAttachCommand{
		WorkspaceKey: "WS", TerminalID: meta.SessionName, Columns: 120, Rows: 36,
		StartAuthority: &authority.OperatorAuthority{},
	})
	if err != nil {
		t.Fatalf("AttachTerminal: %v", err)
	}
	if result.Attachment == nil || result.Reattached {
		t.Fatalf("attach result = %#v", result)
	}
	if api.startCommand.Kind != SessionKindInteractive || api.startCommand.AgentID != "reviewer" ||
		api.openCommand.TerminalID == "" || api.openCommand.TerminalID != api.startCommand.TerminalID {
		t.Fatalf("start/open = %#v / %#v", api.startCommand, api.openCommand)
	}
	if len(api.updates) != 1 || api.updates[0].Status != TerminalRunning ||
		api.updates[0].AttachedClients == nil || *api.updates[0].AttachedClients != 1 {
		t.Fatalf("terminal updates = %#v", api.updates)
	}
	if runtime.attachLaunch == nil || runtime.attachLaunch.Env[EnvSessionToken] != terminalConnectionTestToken ||
		runtime.attachLaunch.Env[EnvInteractionAPIURL] != "http://127.0.0.1:8683" {
		t.Fatalf("private PTY launch = %#v", runtime.attachLaunch)
	}
	stored, err := store.Get(t.Context(), "WS", meta.SessionName)
	if err != nil {
		t.Fatal(err)
	}
	if stored.InteractionSessionID == "" || stored.InteractionTerminalID == "" ||
		stored.InteractionLeaseID == "" || stored.InteractionLeaseFencingToken != 7 {
		t.Fatalf("canonical terminal identity = %#v", stored)
	}
	if stored.Launch.Env[EnvSessionToken] != "" || persistedLaunch.Env[EnvSessionToken] != "" {
		t.Fatal("one-use session credential reached persisted launch metadata")
	}
	for _, token := range resolver.tokens {
		if token != terminalConnectionTestToken {
			t.Fatalf("resolver token = %q", token)
		}
	}
}

func TestAttachTerminalReconnectDoesNotMintAnotherSession(t *testing.T) {
	service, _, runtime, api, _ := newTerminalConnectionService(t)
	meta, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{WorkspaceKey: "WS", AgentID: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	key := TerminalKey{WorkspaceKey: "WS", TerminalID: meta.SessionName}
	runtime.live[key] = true
	runtime.reattach = true
	plan, err := service.PlanTerminalAttach(t.Context(), TerminalAttachCommand{WorkspaceKey: "WS", TerminalID: meta.SessionName})
	if err != nil || plan.StartAuthorityRequired {
		t.Fatalf("reconnect plan = %#v, err = %v", plan, err)
	}
	result, err := service.AttachTerminal(t.Context(), TerminalAttachCommand{WorkspaceKey: "WS", TerminalID: meta.SessionName, Columns: 80, Rows: 24})
	if err != nil || result == nil || !result.Reattached {
		t.Fatalf("reconnect = %#v, err = %v", result, err)
	}
	if api.startCommand.SessionID != "" {
		t.Fatalf("reconnect started another session: %#v", api.startCommand)
	}
}

func TestAttachTerminalOpenFailureAtomicallyFinishesStartedSession(t *testing.T) {
	service, _, runtime, api, _ := newTerminalConnectionService(t)
	api.openErr = errors.New("open failed")
	meta, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{WorkspaceKey: "WS", AgentID: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AttachTerminal(t.Context(), TerminalAttachCommand{
		WorkspaceKey: "WS", TerminalID: meta.SessionName,
		StartAuthority: &authority.OperatorAuthority{}, Columns: 80, Rows: 24,
	})
	if err == nil {
		t.Fatal("expected terminal open failure")
	}
	if runtime.attachLaunch != nil {
		t.Fatal("PTY spawned after terminal-open failure")
	}
	if len(api.finishes) != 1 || api.finishes[0].Status != SessionFailed || api.finishes[0].ErrorClass != "terminal_open_failed" {
		t.Fatalf("session cleanup = %#v", api.finishes)
	}
}

func TestAttachTerminalConvergesPersistedPriorGeneration(t *testing.T) {
	service, store, runtime, _, _ := newTerminalConnectionService(t)
	meta, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{WorkspaceKey: "WS", AgentID: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	meta.InteractionSessionID = "session-old"
	meta.InteractionTerminalID = "terminal-old"
	meta.InteractionLeaseID = "lease-old"
	meta.InteractionLeaseFencingToken = 3
	if err := store.Set(t.Context(), meta); err != nil {
		t.Fatal(err)
	}
	_, err = service.AttachTerminal(t.Context(), TerminalAttachCommand{
		WorkspaceKey: "WS", TerminalID: meta.SessionName,
		StartAuthority: &authority.OperatorAuthority{}, Columns: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.killed) != 1 || runtime.killed[0].TerminalID != meta.SessionName {
		t.Fatalf("prior generation kills = %#v", runtime.killed)
	}
	stored, _ := store.Get(t.Context(), "WS", meta.SessionName)
	if stored.InteractionSessionID == "session-old" || stored.InteractionTerminalID == "terminal-old" || stored.InteractionLeaseID == "lease-old" {
		t.Fatalf("prior generation survived: %#v", stored)
	}
}
