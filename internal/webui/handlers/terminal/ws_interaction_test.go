package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

const terminalTestLeaseToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type terminalInteractionAPIStub struct {
	startCommand interaction.StartSessionCommand
	openCommand  interaction.OpenTerminalCommand
	updates      []interaction.UpdateTerminalCommand
	finishes     []interaction.FinishSessionCommand
	startErr     error
	openErr      error
}

func (stub *terminalInteractionAPIStub) StartSession(
	_ context.Context,
	_ authority.OperatorAuthority,
	command interaction.StartSessionCommand,
) (interaction.SessionStart, error) {
	stub.startCommand = command
	if stub.startErr != nil {
		return interaction.SessionStart{}, stub.startErr
	}
	return interaction.SessionStart{
		Session: &interaction.AgentSession{
			WorkspaceKey: command.WorkspaceKey,
			SessionID:    command.SessionID,
			AgentID:      command.AgentID,
			NodeID:       command.NodeID,
			TerminalID:   command.TerminalID,
			Status:       interaction.SessionRunning,
		},
		Lease: &interaction.SessionLease{
			WorkspaceKey: command.WorkspaceKey,
			SessionID:    command.SessionID,
			AgentID:      command.AgentID,
			NodeID:       command.NodeID,
			LeaseID:      command.LeaseID,
			FencingToken: 7,
			Status:       "active",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
		Token: interaction.NewLeaseToken([]byte(terminalTestLeaseToken)),
	}, nil
}

func (*terminalInteractionAPIStub) RecoverSessionStart(
	context.Context,
	authority.OperatorAuthority,
	interaction.RecoverSessionStartCommand,
) (interaction.SessionStart, error) {
	return interaction.SessionStart{}, interaction.ErrUnavailable
}

func (*terminalInteractionAPIStub) PatchSession(
	context.Context,
	authority.SessionAuthority,
	interaction.PatchSessionCommand,
) (*interaction.AgentSession, error) {
	return nil, nil
}

func (*terminalInteractionAPIStub) PublishTranscript(
	context.Context,
	authority.SessionAuthority,
	interaction.PublishTranscriptCommand,
) (*interaction.AgentSession, error) {
	return nil, nil
}

func (*terminalInteractionAPIStub) HeartbeatSession(
	context.Context,
	authority.SessionAuthority,
	interaction.HeartbeatSessionCommand,
) (*interaction.AgentSession, error) {
	return nil, nil
}

func (stub *terminalInteractionAPIStub) FinishSession(
	_ context.Context,
	_ authority.SessionAuthority,
	command interaction.FinishSessionCommand,
) (*interaction.AgentSession, error) {
	stub.finishes = append(stub.finishes, command)
	return &interaction.AgentSession{
		WorkspaceKey: command.WorkspaceKey,
		SessionID:    command.SessionID,
		Status:       command.Status,
	}, nil
}

func (*terminalInteractionAPIStub) ForceInterrupt(
	context.Context,
	authority.SystemAuthority,
	interaction.ForceInterruptCommand,
) (interaction.ForceInterruptResult, error) {
	return interaction.ForceInterruptResult{}, nil
}

func (stub *terminalInteractionAPIStub) OpenTerminal(
	_ context.Context,
	_ authority.SessionAuthority,
	command interaction.OpenTerminalCommand,
) (*interaction.TerminalSession, error) {
	stub.openCommand = command
	if stub.openErr != nil {
		return nil, stub.openErr
	}
	return &interaction.TerminalSession{
		WorkspaceKey: command.WorkspaceKey,
		TerminalID:   command.TerminalID,
		SessionID:    command.SessionID,
		AgentID:      command.AgentID,
		NodeID:       command.NodeID,
		Status:       interaction.TerminalStarting,
	}, nil
}

func (stub *terminalInteractionAPIStub) UpdateTerminal(
	_ context.Context,
	_ authority.SessionAuthority,
	command interaction.UpdateTerminalCommand,
) (*interaction.TerminalSession, error) {
	stub.updates = append(stub.updates, command)
	return &interaction.TerminalSession{
		WorkspaceKey: command.WorkspaceKey,
		TerminalID:   command.TerminalID,
		Status:       command.Status,
	}, nil
}

func (*terminalInteractionAPIStub) EnqueueInbox(
	context.Context,
	authority.OperatorAuthority,
	interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	return nil, nil
}

func (*terminalInteractionAPIStub) ClaimInbox(
	context.Context,
	authority.SessionAuthority,
	interaction.ClaimInboxCommand,
) (*interaction.InboxMessage, error) {
	return nil, nil
}

func (*terminalInteractionAPIStub) CompleteInbox(
	context.Context,
	authority.SessionAuthority,
	interaction.CompleteInboxCommand,
) (*interaction.InboxMessage, error) {
	return nil, nil
}

func (*terminalInteractionAPIStub) ListActivity(
	context.Context,
	authority.OperatorAuthority,
	interaction.ActivityQuery,
) ([]interaction.Activity, error) {
	return nil, nil
}

func (*terminalInteractionAPIStub) ReconcileSessions(
	context.Context,
	authority.SystemAuthority,
	string,
	time.Time,
) (int, error) {
	return 0, nil
}

type terminalSessionResolverStub struct {
	issuer  *authority.Issuer
	actions []authority.Action
	tokens  []string
}

func newTerminalSessionResolverStub() *terminalSessionResolverStub {
	return &terminalSessionResolverStub{issuer: authority.NewIssuer()}
}

func (stub *terminalSessionResolverStub) ResolveSessionAuthority(
	_ context.Context,
	action authority.Action,
	proof interaction.SessionAuthorityProof,
) (authority.SessionAuthority, error) {
	stub.actions = append(stub.actions, action)
	stub.tokens = append(stub.tokens, string(proof.Token.Bytes()))
	principal, err := stub.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "session:" + proof.SessionID,
		Class:     authority.ClassSession,
		Workspace: proof.WorkspaceKey,
		Actions:   []authority.Action{action},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		return authority.SessionAuthority{}, err
	}
	return stub.issuer.IssueSessionForOwner(
		principal,
		proof.WorkspaceKey,
		action,
		authority.SessionOwner{
			SessionID:    proof.SessionID,
			AgentID:      proof.AgentID,
			TerminalID:   proof.TerminalID,
			NodeID:       proof.NodeID,
			LeaseID:      proof.LeaseID,
			FencingToken: proof.FencingToken,
		},
	)
}

func TestPrepareTerminalInteractionLaunchKeepsCredentialEphemeral(t *testing.T) {
	api := &terminalInteractionAPIStub{}
	resolver := newTerminalSessionResolverStub()
	persisted := &tabmeta.LaunchSpec{
		Argv: []string{"-lc", "loom agent lead"},
		Env:  map[string]string{"LOOM_BACKEND": "codex"},
		Cwd:  "/tmp/worktree",
	}
	params := &terminalWSParams{
		loomServerURL: "http://127.0.0.1:8683",
		interaction: InteractionDependencies{
			API: api, SessionAuthorities: resolver,
		},
		interactionNode: "node-terminal-test",
	}
	operator := authority.OperatorAuthority{}
	launch, lifecycle, err := prepareTerminalInteractionLaunch(
		t.Context(),
		params,
		webuterminal.SessionKey{Workspace: "WS", Name: "term_ui"},
		"agent-docs",
		persisted,
		&operator,
	)
	if err != nil {
		t.Fatalf("prepareTerminalInteractionLaunch: %v", err)
	}
	if launch == persisted || launch.Env[interaction.EnvSessionToken] != terminalTestLeaseToken ||
		launch.Env[interaction.EnvInteractionAPIURL] != "http://127.0.0.1:8683" {
		t.Fatalf("ephemeral launch = %+v", launch)
	}
	if _, exists := persisted.Env[interaction.EnvSessionToken]; exists {
		t.Fatal("one-use credential was added to persisted launch metadata")
	}
	persistedJSON, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if string(persistedJSON) == "" ||
		containsString(string(persistedJSON), terminalTestLeaseToken) {
		t.Fatalf("persisted metadata contains lease token: %s", persistedJSON)
	}
	if api.startCommand.Kind != interaction.SessionKindInteractive ||
		api.startCommand.TerminalID == "" ||
		api.openCommand.TerminalID != api.startCommand.TerminalID {
		t.Fatalf("start/open commands = %+v / %+v", api.startCommand, api.openCommand)
	}
	if err := lifecycle.running(t.Context(), nil); err != nil {
		t.Fatalf("running: %v", err)
	}
	if len(api.updates) != 1 ||
		api.updates[0].Status != interaction.TerminalRunning ||
		api.updates[0].AttachedClients == nil ||
		*api.updates[0].AttachedClients != 1 {
		t.Fatalf("terminal updates = %+v", api.updates)
	}
	lifecycle.Close()
	if lifecycle.rawToken != nil {
		t.Fatal("lifecycle retained raw token after Close")
	}
	if _, exists := launch.Env[interaction.EnvSessionToken]; exists {
		t.Fatal("ephemeral launch retained raw token after Close")
	}
	if launch.Env["LOOM_BACKEND"] != "codex" {
		t.Fatalf("Close removed non-session launch env: %+v", launch.Env)
	}
	for _, token := range resolver.tokens {
		if token != terminalTestLeaseToken {
			t.Fatalf("resolver token = %q", token)
		}
	}
}

func TestPrepareTerminalInteractionLaunchConvergesPersistedPriorGeneration(t *testing.T) {
	api := &terminalInteractionAPIStub{}
	tabs := newTabMetaStoreForWSTest(t)
	key := webuterminal.SessionKey{Workspace: "WS", Name: "term_ui"}
	if err := tabs.Set(t.Context(), &tabmeta.TabMetadata{
		Workspace: key.Workspace, SessionName: key.Name, Kind: "agent",
		AgentID: "agent-docs", InteractionSessionID: "session-old",
		InteractionTerminalID: "terminal-old",
		InteractionLeaseID:    "lease-old", InteractionLeaseFencingToken: 3,
	}); err != nil {
		t.Fatalf("Set prior metadata: %v", err)
	}
	manager := webuterminal.NewMultiPTYManager("cat", 1)
	t.Cleanup(func() { _ = manager.Close() })
	var converged []webuterminal.SessionKey
	manager.SetBeforeKill(func(
		_ context.Context,
		got webuterminal.SessionKey,
		reason string,
	) error {
		if reason != webuterminal.ExitReasonKilled {
			t.Fatalf("prior generation reason = %q", reason)
		}
		converged = append(converged, got)
		return nil
	})
	params := &terminalWSParams{
		manager: manager, tabMetaStore: tabs,
		loomServerURL: "http://loom", interactionNode: "node",
		interaction: InteractionDependencies{
			API: api, SessionAuthorities: newTerminalSessionResolverStub(),
			TerminalIdentities: webuterminal.NewTerminalService(
				nil, tabs, nil, nil, nil, time.Now(),
			),
		},
	}
	_, lifecycle, err := prepareTerminalInteractionLaunch(
		t.Context(),
		params,
		key,
		"agent-docs",
		&tabmeta.LaunchSpec{Argv: []string{"-lc", "true"}},
		&authority.OperatorAuthority{},
	)
	if err != nil {
		t.Fatalf("prepareTerminalInteractionLaunch: %v", err)
	}
	defer lifecycle.Close()
	if len(converged) != 1 || converged[0] != key {
		t.Fatalf("prior-generation convergence = %+v", converged)
	}
	meta, err := tabs.Get(t.Context(), key.Workspace, key.Name)
	if err != nil {
		t.Fatalf("Get successor metadata: %v", err)
	}
	if meta.InteractionSessionID == "" ||
		meta.InteractionTerminalID == "" ||
		meta.InteractionLeaseID == "" ||
		meta.InteractionLeaseFencingToken <= 0 ||
		meta.InteractionSessionID == "session-old" ||
		meta.InteractionTerminalID == "terminal-old" ||
		meta.InteractionLeaseID == "lease-old" {
		t.Fatalf("successor metadata = %+v", meta)
	}
}

func TestTerminalLaunchFailureUsesOnlyAtomicFinish(t *testing.T) {
	api := &terminalInteractionAPIStub{}
	params := &terminalWSParams{
		loomServerURL: "http://loom", interactionNode: "node",
		interaction: InteractionDependencies{
			API: api, SessionAuthorities: newTerminalSessionResolverStub(),
		},
	}
	_, lifecycle, err := prepareTerminalInteractionLaunch(
		t.Context(),
		params,
		webuterminal.SessionKey{Workspace: "WS", Name: "term_ui"},
		"agent-docs",
		&tabmeta.LaunchSpec{Argv: []string{"-lc", "true"}},
		&authority.OperatorAuthority{},
	)
	if err != nil {
		t.Fatalf("prepareTerminalInteractionLaunch: %v", err)
	}
	defer lifecycle.Close()
	lifecycle.fail(t.Context(), "spawn_failed")
	if len(api.updates) != 0 {
		t.Fatalf("launch failure used retired terminal pre-update: %+v", api.updates)
	}
	if len(api.finishes) != 1 ||
		api.finishes[0].Status != interaction.SessionFailed ||
		api.finishes[0].ErrorClass != "spawn_failed" {
		t.Fatalf("atomic finish commands = %+v", api.finishes)
	}
}

func TestPrepareTerminalInteractionLaunchFailsClosedAndConvergesStartedSession(t *testing.T) {
	t.Run("missing capability", func(t *testing.T) {
		_, _, err := prepareTerminalInteractionLaunch(
			t.Context(),
			&terminalWSParams{interactionNode: "node", loomServerURL: "http://loom"},
			webuterminal.SessionKey{Workspace: "WS", Name: "term_ui"},
			"agent-docs",
			&tabmeta.LaunchSpec{Argv: []string{"-lc", "true"}},
			&authority.OperatorAuthority{},
		)
		if !errors.Is(err, interaction.ErrUnavailable) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("terminal open failure", func(t *testing.T) {
		api := &terminalInteractionAPIStub{openErr: errors.New("open failed")}
		params := &terminalWSParams{
			loomServerURL:   "http://loom",
			interactionNode: "node",
			interaction: InteractionDependencies{
				API: api, SessionAuthorities: newTerminalSessionResolverStub(),
			},
		}
		_, _, err := prepareTerminalInteractionLaunch(
			t.Context(),
			params,
			webuterminal.SessionKey{Workspace: "WS", Name: "term_ui"},
			"agent-docs",
			&tabmeta.LaunchSpec{Argv: []string{"-lc", "true"}},
			&authority.OperatorAuthority{},
		)
		if err == nil {
			t.Fatal("expected terminal open failure")
		}
		if len(api.finishes) != 1 ||
			api.finishes[0].Status != interaction.SessionFailed ||
			api.finishes[0].ErrorClass != "terminal_open_failed" {
			t.Fatalf("session cleanup = %+v", api.finishes)
		}
	})
}

func containsString(value, part string) bool {
	return len(part) > 0 && len(value) >= len(part) &&
		(value == part || stringContains(value, part))
}

func stringContains(value, part string) bool {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return true
		}
	}
	return false
}
