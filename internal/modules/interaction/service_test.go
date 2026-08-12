package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	testWorkspace = "TEST"
	testAgent     = "agent-1"
	testSession   = "session-1"
	testTerminal  = "terminal-1"
)

type interactionHarness struct {
	service     *Service
	issuer      *authority.Issuer
	now         time.Time
	sessions    *fakeSessionStore
	leases      *fakeLeaseStore
	terminals   *fakeTerminalStore
	inbox       *fakeInboxStore
	activityLog *fakeActivitySource
	transcripts *fakeTranscriptArtifactStore
}

func newInteractionHarness(t *testing.T) *interactionHarness {
	t.Helper()
	harness := &interactionHarness{
		now:         time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		leases:      &fakeLeaseStore{values: map[string]*SessionLease{}},
		activityLog: &fakeActivitySource{},
		transcripts: &fakeTranscriptArtifactStore{},
	}
	harness.leases.now = func() time.Time { return harness.now }
	harness.sessions = &fakeSessionStore{
		values:     harnessSessionValues(),
		leases:     harness.leases.values,
		leaseStore: harness.leases,
	}
	harness.terminals = &fakeTerminalStore{
		values: harnessTerminalValues(),
		leases: harness.leases.values,
		now:    func() time.Time { return harness.now },
	}
	harness.sessions.terminals = harness.terminals.values
	harness.inbox = &fakeInboxStore{
		leases: harness.leases.values,
		now:    func() time.Time { return harness.now },
	}
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return harness.now })
	if err != nil {
		t.Fatalf("NewIssuerWithClock: %v", err)
	}
	harness.issuer = issuer
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	service, err := New(
		harness.sessions, harness.transcripts, harness.terminals, harness.inbox,
		harness.activityLog, admission, func() time.Time { return harness.now },
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	harness.service = service
	return harness
}

func harnessSessionValues() map[string]*AgentSession {
	return map[string]*AgentSession{}
}

func harnessTerminalValues() map[string]*TerminalSession {
	return map[string]*TerminalSession{}
}

func TestGetSessionReturnsTerminalOwnerSnapshotDefensively(t *testing.T) {
	harness := newInteractionHarness(t)
	finished := harness.now.Add(-time.Minute)
	metadata := map[string]string{"backend": "codex"}
	harness.sessions.values[testSession] = &AgentSession{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		Kind: SessionKindInteractive, Status: SessionCompleted,
		Metadata: metadata, FinishedAt: &finished,
		CreatedAt: harness.now.Add(-time.Hour), UpdatedAt: harness.now,
	}
	got, err := harness.service.GetSession(t.Context(), testWorkspace, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SessionCompleted || got.AgentID != testAgent || got.FinishedAt == nil {
		t.Fatalf("GetSession = %+v", got)
	}
	got.Metadata["backend"] = "mutated"
	*got.FinishedAt = time.Time{}
	if metadata["backend"] != "codex" || harness.sessions.values[testSession].FinishedAt.IsZero() {
		t.Fatal("GetSession leaked mutable persisted values")
	}
}

func TestGetSessionRejectsCrossWorkspaceAndMalformedRows(t *testing.T) {
	harness := newInteractionHarness(t)
	if _, err := harness.service.GetSession(t.Context(), "", testSession); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank workspace error = %v", err)
	}
	harness.sessions.values[testSession] = &AgentSession{
		WorkspaceKey: "OTHER", SessionID: testSession, AgentID: testAgent,
		Status: SessionRunning, CreatedAt: harness.now, UpdatedAt: harness.now,
	}
	if _, err := harness.service.GetSession(t.Context(), testWorkspace, testSession); !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("cross-workspace error = %v", err)
	}
}

func (harness *interactionHarness) operator(t *testing.T, action authority.Action) authority.OperatorAuthority {
	t.Helper()
	principal, err := harness.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "operator:test", Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: []authority.Action{action}, ExpiresAt: harness.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	value, err := harness.issuer.IssueOperator(principal, testWorkspace, action)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	return value
}

func (harness *interactionHarness) session(
	t *testing.T,
	action authority.Action,
	terminalID string,
	fence int64,
) authority.SessionAuthority {
	t.Helper()
	principal, err := harness.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "session:" + testSession, Class: authority.ClassSession, Workspace: testWorkspace,
		Actions: []authority.Action{action}, ExpiresAt: harness.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	value, err := harness.issuer.IssueSessionForOwner(principal, testWorkspace, action, authority.SessionOwner{
		SessionID: testSession, AgentID: testAgent, TerminalID: terminalID,
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: fence,
	})
	if err != nil {
		t.Fatalf("IssueSessionForOwner: %v", err)
	}
	return value
}

func (harness *interactionHarness) system(t *testing.T) authority.SystemAuthority {
	return harness.systemFor(t, "interaction-session-reconciler", ActionReconcileSessions)
}

func (harness *interactionHarness) systemFor(
	t *testing.T,
	subject string,
	action authority.Action,
) authority.SystemAuthority {
	t.Helper()
	principal, err := harness.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: subject, Class: authority.ClassSystem, Workspace: testWorkspace,
		Actions: []authority.Action{action}, ExpiresAt: harness.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	value, err := harness.issuer.IssueSystem(
		principal, testWorkspace, action, "test system operation",
	)
	if err != nil {
		t.Fatalf("IssueSystem: %v", err)
	}
	return value
}

func TestInboxEnqueueAcceptsRegisteredSystemAuthority(t *testing.T) {
	harness := newInteractionHarness(t)
	command := EnqueueInboxCommand{
		WorkspaceKey:  testWorkspace,
		MessageID:     "message-1",
		TargetAgentID: testAgent,
		Body:          "hello",
	}
	message, err := harness.service.EnqueueInboxAsSystem(
		t.Context(),
		harness.systemFor(t, string(InboxDeliveryComponentID), ActionEnqueueInbox),
		command,
	)
	if err != nil {
		t.Fatalf("EnqueueInboxAsSystem: %v", err)
	}
	if message.MessageID != command.MessageID || message.Status != InboxQueued {
		t.Fatalf("system enqueue = %+v", message)
	}
	if _, err := harness.service.EnqueueInboxAsSystem(
		t.Context(),
		harness.system(t),
		command,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong system authority error = %v, want admission denied", err)
	}
}

func TestStartSessionReturnsOneTimeTokenWithoutPersistingOrSerializingIt(t *testing.T) {
	harness := newInteractionHarness(t)
	metadata := map[string]string{"source": "ui"}
	result, err := harness.service.StartSession(t.Context(), harness.operator(t, ActionStartSession), StartSessionCommand{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent, NodeID: "node-1",
		Kind: SessionKindInteractive, TerminalID: testTerminal, LeaseID: "lease-1",
		LeaseTTL: time.Minute, Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	metadata["source"] = "mutated"
	if result.Session.Metadata["source"] != "ui" {
		t.Fatalf("persisted metadata was mutated through caller input: %+v", result.Session.Metadata)
	}
	const secret = "raw-session-token"
	if got := string(result.Token.Bytes()); got != secret {
		t.Fatalf("one-time token = %q", got)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(wire), secret) || strings.Contains(string(wire), `"Token"`) ||
		strings.Contains(result.Lease.TokenHash, secret) {
		t.Fatalf("session result serialized or persisted raw credential: %s", wire)
	}
	result.Token.Close()
	if got := result.Token.Bytes(); len(got) != 0 {
		t.Fatalf("closed token still has %d bytes", len(got))
	}
}

func TestRecoverSessionStartAcceptsOnlyOperatorOrRegisteredSystemAuthority(t *testing.T) {
	harness := newInteractionHarness(t)
	command := RecoverSessionStartCommand{
		Original: StartSessionCommand{
			WorkspaceKey: testWorkspace,
			SessionID:    testSession,
			AgentID:      testAgent,
			NodeID:       "node-1",
			Kind:         SessionKindInteractive,
			TerminalID:   testTerminal,
			Phase:        "starting",
			LeaseID:      "lease-original",
			LeaseTTL:     time.Minute,
			Metadata:     map[string]string{"intent": "stable"},
		},
		ExpectedLeaseID:           "lease-original",
		ExpectedLeaseFencingToken: 4,
		ReplacementLeaseID:        "lease-replacement",
		ReplacementLeaseTTL:       time.Minute,
	}
	result := func() SessionStart {
		return SessionStart{
			Session: &AgentSession{
				WorkspaceKey:             testWorkspace,
				SessionID:                testSession,
				AgentID:                  testAgent,
				NodeID:                   "node-1",
				Kind:                     SessionKindInteractive,
				TerminalID:               testTerminal,
				Status:                   SessionStarting,
				CurrentLeaseID:           "lease-replacement",
				CurrentLeaseFencingToken: 5,
				Phase:                    "starting",
				Metadata:                 map[string]string{"intent": "stable"},
			},
			Lease: &SessionLease{
				WorkspaceKey: testWorkspace,
				LeaseID:      "lease-replacement",
				SessionID:    testSession,
				AgentID:      testAgent,
				NodeID:       "node-1",
				FencingToken: 5,
				Status:       "active",
				ExpiresAt:    harness.now.Add(time.Minute),
			},
			Token: NewLeaseToken([]byte("raw-replacement-token")),
		}
	}
	harness.sessions.recoverStartResult = result()
	recovered, err := harness.service.RecoverSessionStart(
		t.Context(),
		harness.operator(t, ActionRecoverStart),
		command,
	)
	if err != nil {
		t.Fatalf("RecoverSessionStart: %v", err)
	}
	if recovered.Lease.LeaseID != command.ReplacementLeaseID ||
		len(harness.sessions.recoverStartCalls) != 1 {
		t.Fatalf("recovered=%+v calls=%+v", recovered, harness.sessions.recoverStartCalls)
	}
	recovered.Token.Close()

	if _, err := harness.service.RecoverSessionStart(
		t.Context(),
		harness.operator(t, ActionStartSession),
		command,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong operator authority error=%v", err)
	}
	if len(harness.sessions.recoverStartCalls) != 1 {
		t.Fatal("denied operator authority reached recovery store")
	}

	harness.sessions.recoverStartResult = result()
	recovered, err = harness.service.RecoverSessionStartAsSystem(
		t.Context(),
		harness.systemFor(t, string(SessionRecoveryComponentID), ActionRecoverStart),
		command,
	)
	if err != nil {
		t.Fatalf("RecoverSessionStartAsSystem: %v", err)
	}
	if len(harness.sessions.recoverStartCalls) != 2 {
		t.Fatalf("system recovery calls=%+v", harness.sessions.recoverStartCalls)
	}
	recovered.Token.Close()
}

func TestStartSessionDoesNotExposePartialStateWhenAtomicStartFails(t *testing.T) {
	harness := newInteractionHarness(t)
	harness.leases.createErr = errors.New("fleet unavailable")
	_, err := harness.service.StartSession(t.Context(), harness.operator(t, ActionStartSession), StartSessionCommand{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent, NodeID: "node-1",
		Kind: SessionKindInteractive, LeaseID: "lease-1", LeaseTTL: time.Minute,
	})
	if err == nil {
		t.Fatal("StartSession succeeded with a failed lease create")
	}
	if got := harness.sessions.values[testSession]; got != nil {
		t.Fatalf("failed atomic start persisted session = %+v", got)
	}
	if got := harness.leases.values[testSession]; got != nil {
		t.Fatalf("failed atomic start persisted lease = %+v", got)
	}
}

func TestStartSessionAcceptsMatchingHashProjectionAndRejectsInvalidProjection(t *testing.T) {
	t.Run("matching hash", func(t *testing.T) {
		harness := newInteractionHarness(t)
		harness.leases.projectHash = true
		result, err := harness.service.StartSession(
			t.Context(),
			harness.operator(t, ActionStartSession),
			StartSessionCommand{
				WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent, NodeID: "node-1",
				Kind: SessionKindInteractive, LeaseID: "lease-1", LeaseTTL: time.Minute,
			},
		)
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		sum := sha256.Sum256([]byte("raw-session-token"))
		if result.Lease.TokenHash != fmt.Sprintf("%x", sum[:]) {
			t.Fatalf("token hash = %q", result.Lease.TokenHash)
		}
	})

	t.Run("invalid returned lease is rejected and raw token is closed", func(t *testing.T) {
		harness := newInteractionHarness(t)
		harness.leases.invalidResult = true
		_, err := harness.service.StartSession(
			t.Context(),
			harness.operator(t, ActionStartSession),
			StartSessionCommand{
				WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent, NodeID: "node-1",
				Kind: SessionKindInteractive, LeaseID: "lease-1", LeaseTTL: time.Minute,
			},
		)
		if !errors.Is(err, ErrInvalidPersistedState) {
			t.Fatalf("StartSession error = %v, want invalid persisted state", err)
		}
		if got := harness.sessions.values[testSession]; got == nil || got.Status != SessionRunning {
			t.Fatalf("atomically committed session = %+v", got)
		}
		if got := harness.leases.values[testSession]; got == nil || got.Status != "active" {
			t.Fatalf("atomically committed lease = %+v", got)
		}
		if harness.leases.lastToken == nil || len(harness.leases.lastToken.Bytes()) != 0 {
			t.Fatal("invalid start projection retained the raw lease token")
		}
	})
}

func TestSessionAuthorityRejectsCrossTerminalAndStaleFence(t *testing.T) {
	harness := newInteractionHarness(t)
	harness.sessions.values[testSession] = &AgentSession{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", Kind: SessionKindInteractive, Status: SessionRunning,
	}
	harness.leases.values[testSession] = &SessionLease{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 4, Status: "active",
		ExpiresAt: harness.now.Add(time.Minute),
	}
	auth := harness.session(t, ActionOpenTerminal, testTerminal, 4)
	opened, err := harness.service.OpenTerminal(t.Context(), auth, OpenTerminalCommand{
		WorkspaceKey: testWorkspace, TerminalID: testTerminal, SessionID: testSession,
		AgentID: testAgent, NodeID: "node-1", PTYProvider: "local",
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	if opened.TerminalID != testTerminal {
		t.Fatalf("opened terminal = %+v", opened)
	}
	if _, err := harness.service.OpenTerminal(t.Context(), auth, OpenTerminalCommand{
		WorkspaceKey: testWorkspace, TerminalID: "terminal-2", SessionID: testSession,
		AgentID: testAgent, NodeID: "node-1", PTYProvider: "local",
	}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("cross-terminal error = %v, want ErrNotOwner", err)
	}

	harness.leases.values[testSession].FencingToken = 5
	heartbeatAuth := harness.session(t, ActionHeartbeatSession, "", 4)
	if _, err := harness.service.HeartbeatSession(t.Context(), heartbeatAuth, HeartbeatSessionCommand{
		WorkspaceKey: testWorkspace, SessionID: testSession, Phase: "stale", LeaseTTL: time.Minute,
	}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("stale-fence heartbeat error = %v, want ErrNotOwner", err)
	}
	if got := harness.sessions.values[testSession].Phase; got != "" {
		t.Fatalf("stale generation mutated session phase to %q", got)
	}
}

func TestHeartbeatAndFinishCommitSessionAndLeaseTogether(t *testing.T) {
	harness := newInteractionHarness(t)
	start, err := harness.service.StartSession(
		t.Context(),
		harness.operator(t, ActionStartSession),
		StartSessionCommand{
			WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent, NodeID: "node-1",
			Kind: SessionKindTask, LeaseID: "lease-1", LeaseTTL: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	start.Token.Close()
	heartbeat, err := harness.service.HeartbeatSession(
		t.Context(),
		harness.session(t, ActionHeartbeatSession, "", 1),
		HeartbeatSessionCommand{
			WorkspaceKey: testWorkspace, SessionID: testSession, Phase: "chatting", LeaseTTL: 2 * time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("HeartbeatSession: %v", err)
	}
	if heartbeat.Phase != "chatting" || !heartbeat.LastHeartbeat.Equal(harness.now) {
		t.Fatalf("heartbeat session = %+v", heartbeat)
	}
	if got := harness.leases.values[testSession]; !got.ExpiresAt.Equal(harness.now.Add(2 * time.Minute)) {
		t.Fatalf("heartbeat lease = %+v", got)
	}

	exitCode := 0
	finished, err := harness.service.FinishSession(
		t.Context(),
		harness.session(t, ActionFinishSession, "", 1),
		FinishSessionCommand{
			WorkspaceKey: testWorkspace, SessionID: testSession, Status: SessionCompleted,
			Summary: "done", ExitCode: &exitCode, TranscriptArtifactID: "artifact-1",
		},
	)
	if err != nil {
		t.Fatalf("FinishSession: %v", err)
	}
	if finished.Status != SessionCompleted || finished.FinishedAt == nil ||
		finished.TranscriptArtifactID != "artifact-1" {
		t.Fatalf("finished session = %+v", finished)
	}
	if got := harness.leases.values[testSession]; got.Status != "released" {
		t.Fatalf("finished lease = %+v", got)
	}
}

func TestPublishTranscriptUsesSessionAuthorityAndLinksExactGeneration(t *testing.T) {
	harness := newInteractionHarness(t)
	harness.sessions.values[testSession] = &AgentSession{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", Kind: SessionKindInteractive, TerminalID: testTerminal,
		TaskID: "task-1", Status: SessionRunning, CurrentLeaseID: "lease-1",
		CurrentLeaseFencingToken: 1,
	}
	harness.leases.values[testSession] = &SessionLease{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1,
		Status: "active", ExpiresAt: harness.now.Add(time.Minute),
	}
	content := []byte("{\"seq\":1,\"role\":\"user\",\"text\":\"hello\"}\n")
	updated, err := harness.service.PublishTranscript(
		t.Context(),
		harness.session(t, ActionPublishTranscript, testTerminal, 1),
		PublishTranscriptCommand{
			WorkspaceKey: testWorkspace, SessionID: testSession,
			Content: content, Metadata: map[string]string{"backend": "codex"},
		},
	)
	if err != nil {
		t.Fatalf("PublishTranscript: %v", err)
	}
	if updated.TranscriptArtifactID != "transcript-"+testSession {
		t.Fatalf("published session = %+v", updated)
	}
	if len(harness.transcripts.commands) != 1 {
		t.Fatalf("transcript commands = %+v", harness.transcripts.commands)
	}
	if len(harness.transcripts.authorities) != 1 ||
		harness.transcripts.authorities[0].Action() != ActionPublishTranscript ||
		harness.transcripts.authorities[0].SessionID() != testSession ||
		harness.transcripts.authorities[0].FencingToken() != 1 {
		t.Fatalf("transcript authority = %+v", harness.transcripts.authorities)
	}
	command := harness.transcripts.commands[0]
	if command.WorkspaceKey != testWorkspace || command.SessionID != testSession ||
		command.AgentID != testAgent || command.TaskID != "task-1" ||
		string(command.Content) != string(content) || command.Metadata["backend"] != "codex" {
		t.Fatalf("transcript command = %+v", command)
	}

	harness.leases.values[testSession].FencingToken = 2
	_, err = harness.service.PublishTranscript(
		t.Context(),
		harness.session(t, ActionPublishTranscript, testTerminal, 1),
		PublishTranscriptCommand{
			WorkspaceKey: testWorkspace, SessionID: testSession,
			Content: content,
		},
	)
	if !errors.Is(err, ErrNotOwner) {
		t.Fatalf("stale transcript publish error = %v, want ErrNotOwner", err)
	}
}

func TestInteractiveFinishRequiresAtomicTerminalResult(t *testing.T) {
	harness := newInteractionHarness(t)
	harness.sessions.values[testSession] = &AgentSession{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", Kind: SessionKindInteractive, TerminalID: testTerminal,
		Status: SessionRunning, CurrentLeaseID: "lease-1",
		CurrentLeaseFencingToken: 1,
	}
	harness.leases.values[testSession] = &SessionLease{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1,
		Status: "active", ExpiresAt: harness.now.Add(time.Minute),
	}
	harness.terminals.values[testTerminal] = &TerminalSession{
		WorkspaceKey: testWorkspace, TerminalID: testTerminal,
		SessionID: testSession, AgentID: testAgent, NodeID: "node-1",
		Status: TerminalRunning, AttachedClients: 2,
	}
	finished, err := harness.service.FinishSession(
		t.Context(),
		harness.session(t, ActionFinishSession, testTerminal, 1),
		FinishSessionCommand{
			WorkspaceKey: testWorkspace, SessionID: testSession,
			Status: SessionCompleted, Summary: "done",
		},
	)
	if err != nil {
		t.Fatalf("FinishSession: %v", err)
	}
	terminal := harness.terminals.values[testTerminal]
	if finished.Status != SessionCompleted ||
		terminal.Status != TerminalExited ||
		terminal.EndedAt == nil ||
		terminal.AttachedClients != 0 ||
		harness.leases.values[testSession].Status != "released" {
		t.Fatalf("atomic finish session=%+v terminal=%+v lease=%+v",
			finished, terminal, harness.leases.values[testSession])
	}
}

func TestForceInterruptRequiresRegisteredSystemAuthorityAndExactConvergedResult(t *testing.T) {
	harness := newInteractionHarness(t)
	finishedAt := harness.now
	harness.sessions.forceInterruptResult = ForceInterruptResult{
		Session: &AgentSession{
			WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
			NodeID: "node-1", TerminalID: testTerminal, Kind: SessionKindInteractive,
			CurrentLeaseID: "lease-1", CurrentLeaseFencingToken: 4,
			Status: SessionInterrupted, FinishedAt: &finishedAt,
		},
		Terminal: &TerminalSession{
			WorkspaceKey: testWorkspace, TerminalID: testTerminal, SessionID: testSession,
			AgentID: testAgent, NodeID: "node-1", StreamRef: "terminal:TEST/tab-1",
			Status: TerminalExited, AttachedClients: 0, EndedAt: &finishedAt,
			Metadata: map[string]string{"terminal_tab": "tab-1"},
		},
		Lease: &SessionLease{
			WorkspaceKey: testWorkspace, LeaseID: "lease-1", SessionID: testSession,
			AgentID: testAgent, NodeID: "node-1", FencingToken: 4, Status: "released",
		},
		Changed: true,
	}
	command := ForceInterruptCommand{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		TerminalID: testTerminal, ExpectedLeaseID: "lease-1",
		ExpectedLeaseFencingToken: 4, StreamRef: "terminal:TEST/tab-1",
		TerminalTab: "tab-1", Reason: "interactive agent restart",
	}
	missingGeneration := command
	missingGeneration.ExpectedLeaseID = ""
	missingGeneration.ExpectedLeaseFencingToken = 0
	_, err := harness.service.ForceInterrupt(
		t.Context(),
		harness.systemFor(t, string(TerminalLifecycleComponentID), ActionForceInterrupt),
		missingGeneration,
	)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing expected lease generation error = %v, want ErrInvalid", err)
	}
	if len(harness.sessions.forceInterruptCalls) != 0 {
		t.Fatalf("missing expected generation reached store: %+v", harness.sessions.forceInterruptCalls)
	}
	result, err := harness.service.ForceInterrupt(
		t.Context(),
		harness.systemFor(t, string(TerminalLifecycleComponentID), ActionForceInterrupt),
		command,
	)
	if err != nil {
		t.Fatalf("ForceInterrupt: %v", err)
	}
	if !result.Changed || result.Session.Status != SessionInterrupted ||
		result.Terminal.Status != TerminalExited || result.Lease.Status != "released" {
		t.Fatalf("force-interrupt result = %+v", result)
	}
	if len(harness.sessions.forceInterruptCalls) != 1 ||
		harness.sessions.forceInterruptCalls[0] != command {
		t.Fatalf("force-interrupt store calls = %+v", harness.sessions.forceInterruptCalls)
	}

	harness.sessions.forceInterruptResult.Lease.FencingToken = 5
	_, err = harness.service.ForceInterrupt(
		t.Context(),
		harness.systemFor(t, string(TerminalLifecycleComponentID), ActionForceInterrupt),
		command,
	)
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("mismatched result generation error = %v, want invalid persisted state", err)
	}
	harness.sessions.forceInterruptResult.Lease.FencingToken = 4
	harness.sessions.forceInterruptResult.Session.Status = SessionCompleted
	harness.sessions.forceInterruptResult.Terminal.Status = TerminalFailed
	harness.sessions.forceInterruptResult.Changed = false
	preserved, err := harness.service.ForceInterrupt(
		t.Context(),
		harness.systemFor(t, string(TerminalLifecycleComponentID), ActionForceInterrupt),
		command,
	)
	if err != nil {
		t.Fatalf("ForceInterrupt completed replay: %v", err)
	}
	if preserved.Changed || preserved.Session.Status != SessionCompleted ||
		preserved.Terminal.Status != TerminalFailed {
		t.Fatalf("completed force-interrupt replay = %+v", preserved)
	}

	_, err = harness.service.ForceInterrupt(
		t.Context(),
		harness.system(t),
		command,
	)
	if !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong system authority error = %v, want admission denied", err)
	}
	if len(harness.sessions.forceInterruptCalls) != 3 {
		t.Fatalf("denied authority reached store: %+v", harness.sessions.forceInterruptCalls)
	}
}

func TestInboxClaimantIsDerivedFromSessionAuthority(t *testing.T) {
	harness := newInteractionHarness(t)
	harness.sessions.values[testSession] = &AgentSession{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", Kind: SessionKindInteractive, Status: SessionRunning,
	}
	harness.leases.values[testSession] = &SessionLease{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1, Status: "active",
		ExpiresAt: harness.now.Add(time.Minute),
	}
	claimed, err := harness.service.ClaimInbox(
		t.Context(),
		harness.session(t, ActionClaimInbox, "", 1),
		ClaimInboxCommand{
			WorkspaceKey: testWorkspace, AgentID: testAgent, SessionID: testSession, LeaseTTL: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("ClaimInbox: %v", err)
	}
	if claimed.ClaimedBy != testSession {
		t.Fatalf("claimed_by = %q, want authority session", claimed.ClaimedBy)
	}
	if claimed.Attempt != 1 {
		t.Fatalf("claim attempt = %d, want 1", claimed.Attempt)
	}
	requeued, err := harness.service.CompleteInbox(
		t.Context(),
		harness.session(t, ActionCompleteInbox, "", 1),
		CompleteInboxCommand{
			WorkspaceKey: testWorkspace,
			SessionID:    testSession,
			MessageID:    claimed.MessageID,
			Attempt:      claimed.Attempt,
			Status:       InboxQueued,
			ErrorClass:   "runtime_busy",
		},
	)
	if err != nil {
		t.Fatalf("CompleteInbox(requeue): %v", err)
	}
	if requeued.Status != InboxQueued || requeued.Attempt != claimed.Attempt ||
		requeued.ClaimedBy != "" || requeued.ClaimExpiresAt != nil {
		t.Fatalf("requeued inbox = %+v", requeued)
	}
	if _, err := harness.service.CompleteInbox(
		t.Context(),
		harness.session(t, ActionCompleteInbox, "", 1),
		CompleteInboxCommand{
			WorkspaceKey: testWorkspace,
			SessionID:    testSession,
			MessageID:    claimed.MessageID,
			Status:       InboxDelivered,
		},
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero-attempt completion error = %v, want invalid", err)
	}
}

func TestInboxClaimRejectsInvalidPersistedDeliveryFence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*InboxMessage, time.Time)
	}{
		{
			name: "non-queued status",
			mutate: func(message *InboxMessage, _ time.Time) {
				message.Status = InboxDelivered
			},
		},
		{
			name: "non-positive attempt",
			mutate: func(message *InboxMessage, _ time.Time) {
				message.Attempt = 0
			},
		},
		{
			name: "missing claim expiry",
			mutate: func(message *InboxMessage, _ time.Time) {
				message.ClaimExpiresAt = nil
			},
		},
		{
			name: "expired claim",
			mutate: func(message *InboxMessage, now time.Time) {
				message.ClaimExpiresAt = &now
			},
		},
		{
			name: "foreign explicit session target",
			mutate: func(message *InboxMessage, _ time.Time) {
				message.SessionID = "session-other"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newInteractionHarness(t)
			harness.sessions.values[testSession] = &AgentSession{
				WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
				NodeID: "node-1", Kind: SessionKindInteractive, Status: SessionRunning,
			}
			harness.leases.values[testSession] = &SessionLease{
				WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
				NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1, Status: "active",
				ExpiresAt: harness.now.Add(time.Minute),
			}
			harness.inbox.claimMutator = test.mutate

			_, err := harness.service.ClaimInbox(
				t.Context(),
				harness.session(t, ActionClaimInbox, "", 1),
				ClaimInboxCommand{
					WorkspaceKey: testWorkspace,
					AgentID:      testAgent,
					SessionID:    testSession,
					LeaseTTL:     time.Minute,
				},
			)
			if !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("ClaimInbox error = %v, want invalid persisted state", err)
			}
		})
	}
}

func TestTerminalUpdateRejectsDestructivePartialOrInvalidState(t *testing.T) {
	harness := newInteractionHarness(t)
	harness.sessions.values[testSession] = &AgentSession{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", Kind: SessionKindInteractive, Status: SessionRunning,
	}
	harness.leases.values[testSession] = &SessionLease{
		WorkspaceKey: testWorkspace, SessionID: testSession, AgentID: testAgent,
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1, Status: "active",
		ExpiresAt: harness.now.Add(time.Minute),
	}
	opened, err := harness.service.OpenTerminal(
		t.Context(),
		harness.session(t, ActionOpenTerminal, testTerminal, 1),
		OpenTerminalCommand{
			WorkspaceKey: testWorkspace, TerminalID: testTerminal, SessionID: testSession,
			AgentID: testAgent, NodeID: "node-1", PTYProvider: "local", StreamRef: "stream-1",
		},
	)
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	if opened.StreamRef != "stream-1" {
		t.Fatalf("opened stream_ref = %q", opened.StreamRef)
	}

	negative := -1
	if _, err := harness.service.UpdateTerminal(
		t.Context(),
		harness.session(t, ActionUpdateTerminal, testTerminal, 1),
		UpdateTerminalCommand{
			WorkspaceKey: testWorkspace, TerminalID: testTerminal,
			Status: TerminalRunning, AttachedClients: &negative,
		},
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative attached clients error = %v", err)
	}

	updated, err := harness.service.UpdateTerminal(
		t.Context(),
		harness.session(t, ActionUpdateTerminal, testTerminal, 1),
		UpdateTerminalCommand{
			WorkspaceKey: testWorkspace, TerminalID: testTerminal, Status: TerminalRunning,
		},
	)
	if err != nil {
		t.Fatalf("UpdateTerminal: %v", err)
	}
	if updated.StreamRef != "stream-1" {
		t.Fatalf("omitted stream_ref cleared persisted value: %+v", updated)
	}
}

func TestActivityIsAReadProjectionOverDistinctAggregates(t *testing.T) {
	harness := newInteractionHarness(t)
	harness.activityLog.values = []Activity{
		{
			WorkspaceKey: testWorkspace, AgentID: testAgent, Kind: ActivitySession,
			SourceID: "session-1", StartedAt: harness.now,
		},
		{
			WorkspaceKey: testWorkspace, AgentID: testAgent, Kind: ActivityBatchRun,
			SourceID: "run-1", StartedAt: harness.now.Add(time.Minute),
		},
	}
	values, err := harness.service.ListActivity(
		t.Context(), harness.operator(t, ActionReadActivity),
		ActivityQuery{WorkspaceKey: testWorkspace, AgentID: testAgent},
	)
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if len(values) != 2 || values[0].Kind != ActivityBatchRun || values[0].SourceID != "run-1" ||
		values[1].Kind != ActivitySession || values[1].SourceID != "session-1" {
		t.Fatalf("activity projection = %+v", values)
	}
	if harness.activityLog.calls != 1 {
		t.Fatalf("combined activity source calls = %d, want 1", harness.activityLog.calls)
	}
}

func TestReconcileSessionsInterruptsOnlyRowsWithoutLiveLease(t *testing.T) {
	harness := newInteractionHarness(t)
	harness.sessions.values["missing-lease"] = &AgentSession{
		WorkspaceKey: testWorkspace, SessionID: "missing-lease", AgentID: testAgent,
		Status: SessionRunning,
	}
	harness.sessions.values["live"] = &AgentSession{
		WorkspaceKey: testWorkspace, SessionID: "live", AgentID: testAgent,
		Status: SessionRunning,
	}
	harness.leases.values["live"] = &SessionLease{
		WorkspaceKey: testWorkspace, SessionID: "live", AgentID: testAgent,
		Status: "active", ExpiresAt: harness.now.Add(time.Minute),
	}
	count, err := harness.service.ReconcileSessions(
		t.Context(), harness.system(t), testWorkspace, harness.now,
	)
	if err != nil {
		t.Fatalf("ReconcileSessions: %v", err)
	}
	if count != 1 || harness.sessions.values["missing-lease"].Status != SessionInterrupted ||
		harness.sessions.values["live"].Status != SessionRunning {
		t.Fatalf("reconciled=%d missing=%+v live=%+v", count,
			harness.sessions.values["missing-lease"], harness.sessions.values["live"])
	}
}

type fakeSessionStore struct {
	values               map[string]*AgentSession
	leases               map[string]*SessionLease
	terminals            map[string]*TerminalSession
	leaseStore           *fakeLeaseStore
	forceInterruptResult ForceInterruptResult
	forceInterruptErr    error
	forceInterruptCalls  []ForceInterruptCommand
	recoverStartResult   SessionStart
	recoverStartErr      error
	recoverStartCalls    []RecoverSessionStartCommand
}

type fakeTranscriptArtifactStore struct {
	commands    []TranscriptArtifactCreate
	authorities []authority.SessionAuthority
	err         error
}

func (store *fakeTranscriptArtifactStore) CreateContent(
	_ context.Context,
	auth authority.SessionAuthority,
	command TranscriptArtifactCreate,
) (string, error) {
	store.authorities = append(store.authorities, auth)
	store.commands = append(store.commands, command)
	if store.err != nil {
		return "", store.err
	}
	return command.ArtifactID, nil
}

func (store *fakeSessionStore) Start(_ context.Context, command StartSessionCommand) (SessionStart, error) {
	if _, exists := store.values[command.SessionID]; exists {
		return SessionStart{}, ErrConflict
	}
	if store.leaseStore == nil {
		return SessionStart{}, ErrUnavailable
	}
	value := &AgentSession{
		WorkspaceKey: command.WorkspaceKey, SessionID: command.SessionID, AgentID: command.AgentID,
		NodeID: command.NodeID, Kind: command.Kind, TaskID: command.TaskID, TerminalID: command.TerminalID,
		Status: SessionRunning, CurrentLeaseID: command.LeaseID,
		Metadata: cloneMetadata(command.Metadata),
	}
	lease, token, err := store.leaseStore.start(command)
	if err != nil {
		return SessionStart{}, err
	}
	value.CurrentLeaseFencingToken = lease.FencingToken
	store.values[command.SessionID] = cloneSession(value)
	return SessionStart{Session: cloneSession(value), Lease: lease, Token: token}, nil
}

func (store *fakeSessionStore) RecoverStart(
	_ context.Context,
	command RecoverSessionStartCommand,
) (SessionStart, error) {
	store.recoverStartCalls = append(store.recoverStartCalls, command)
	return store.recoverStartResult, store.recoverStartErr
}

func (store *fakeSessionStore) Get(_ context.Context, _, sessionID string) (*AgentSession, error) {
	value := store.values[sessionID]
	if value == nil {
		return nil, ErrNotFound
	}
	return cloneSession(value), nil
}

func (store *fakeSessionStore) PatchOwned(
	_ context.Context,
	workspace string,
	owner authority.SessionOwner,
	patch SessionPatch,
) (*AgentSession, *SessionLease, error) {
	value := store.values[owner.SessionID]
	lease := store.leases[owner.SessionID]
	if value == nil || value.Status.Terminal() ||
		!fakeLeaseOwnerMatches(lease, workspace, owner, patch.At) {
		return nil, nil, ErrNotOwner
	}
	if patch.Phase != nil {
		value.Phase = *patch.Phase
	}
	if value.Metadata == nil && len(patch.MetadataUpserts) > 0 {
		value.Metadata = make(map[string]string, len(patch.MetadataUpserts))
	}
	for key, item := range patch.MetadataUpserts {
		value.Metadata[key] = item
	}
	for _, key := range patch.MetadataRemovals {
		delete(value.Metadata, key)
	}
	if patch.TranscriptArtifactID != nil {
		value.TranscriptArtifactID = *patch.TranscriptArtifactID
	}
	value.UpdatedAt = patch.At
	return cloneSession(value), cloneLease(lease), nil
}

func (store *fakeSessionStore) HeartbeatOwned(
	_ context.Context,
	workspace string,
	owner authority.SessionOwner,
	heartbeat SessionHeartbeat,
) (*AgentSession, *SessionLease, error) {
	value := store.values[owner.SessionID]
	lease := store.leases[owner.SessionID]
	if value == nil || value.Status.Terminal() ||
		!fakeLeaseOwnerMatches(lease, workspace, owner, heartbeat.At) {
		return nil, nil, ErrNotOwner
	}
	value.Phase = heartbeat.Phase
	value.LastHeartbeat = heartbeat.At
	lease.ExpiresAt = heartbeat.At.Add(heartbeat.LeaseTTL)
	lease.LastHeartbeat = heartbeat.At
	return cloneSession(value), cloneLease(lease), nil
}

func (store *fakeSessionStore) FinishOwned(
	_ context.Context,
	workspace string,
	owner authority.SessionOwner,
	finish SessionFinish,
) (SessionFinishResult, error) {
	value := store.values[owner.SessionID]
	lease := store.leases[owner.SessionID]
	if value == nil || value.Status.Terminal() ||
		!fakeLeaseOwnerMatches(lease, workspace, owner, finish.FinishedAt) {
		return SessionFinishResult{}, ErrNotOwner
	}
	value.Status = finish.Status
	value.Summary = finish.Summary
	value.ErrorClass = finish.ErrorClass
	value.TranscriptArtifactID = finish.TranscriptArtifactID
	value.FinishedAt = &finish.FinishedAt
	if finish.ExitCode != nil {
		exitCode := *finish.ExitCode
		value.ExitCode = &exitCode
	}
	lease.Status = "released"
	var terminal *TerminalSession
	if value.Kind == SessionKindInteractive {
		terminal = store.terminals[value.TerminalID]
		if terminal == nil || terminal.SessionID != value.SessionID ||
			terminal.AgentID != value.AgentID || terminal.NodeID != value.NodeID {
			return SessionFinishResult{}, ErrNotOwner
		}
		if finish.Status == SessionFailed || finish.Status == SessionInterrupted {
			terminal.Status = TerminalFailed
		} else {
			terminal.Status = TerminalExited
		}
		terminal.AttachedClients = 0
		terminal.EndedAt = &finish.FinishedAt
		terminal.UpdatedAt = finish.FinishedAt
	}
	return SessionFinishResult{
		Session: cloneSession(value), Terminal: cloneTerminal(terminal), Lease: cloneLease(lease),
	}, nil
}

func (store *fakeSessionStore) ForceInterrupt(
	_ context.Context,
	command ForceInterruptCommand,
) (ForceInterruptResult, error) {
	store.forceInterruptCalls = append(store.forceInterruptCalls, command)
	if store.forceInterruptErr != nil {
		return ForceInterruptResult{}, store.forceInterruptErr
	}
	return ForceInterruptResult{
		Session:  cloneSession(store.forceInterruptResult.Session),
		Terminal: cloneTerminal(store.forceInterruptResult.Terminal),
		Lease:    cloneLease(store.forceInterruptResult.Lease),
		Changed:  store.forceInterruptResult.Changed,
	}, nil
}

func (store *fakeSessionStore) InterruptIfLeaseMissing(
	_ context.Context,
	workspace string,
	sessionID string,
	now time.Time,
) (*AgentSession, bool, error) {
	value := store.values[sessionID]
	if value == nil || value.WorkspaceKey != workspace {
		return nil, false, ErrNotFound
	}
	if value.Status.Terminal() {
		return cloneSession(value), false, nil
	}
	if lease := store.leases[sessionID]; lease != nil &&
		lease.Status == "active" && lease.ExpiresAt.After(now) {
		return cloneSession(value), false, nil
	}
	value.Status = SessionInterrupted
	value.ErrorClass = "session_lease_missing"
	value.FinishedAt = &now
	return cloneSession(value), true, nil
}

func (store *fakeSessionStore) ListRecoverable(_ context.Context, _ string, _ time.Time) ([]*AgentSession, error) {
	out := make([]*AgentSession, 0, len(store.values))
	for _, value := range store.values {
		out = append(out, cloneSession(value))
	}
	return out, nil
}

type fakeLeaseStore struct {
	values        map[string]*SessionLease
	createErr     error
	now           func() time.Time
	projectHash   bool
	invalidResult bool
	lastToken     *LeaseToken
}

func (store *fakeLeaseStore) start(command StartSessionCommand) (*SessionLease, *LeaseToken, error) {
	if store.createErr != nil {
		return nil, nil, store.createErr
	}
	value := &SessionLease{
		WorkspaceKey: command.WorkspaceKey, LeaseID: command.LeaseID, SessionID: command.SessionID,
		AgentID: command.AgentID, NodeID: command.NodeID, FencingToken: 1, Status: "active",
		ExpiresAt: store.now().Add(command.LeaseTTL),
	}
	if store.projectHash {
		sum := sha256.Sum256([]byte("raw-session-token"))
		value.TokenHash = fmt.Sprintf("%x", sum[:])
	}
	store.values[command.SessionID] = cloneLease(value)
	if store.invalidResult {
		value.AgentID = "wrong-agent"
	}
	store.lastToken = NewLeaseToken([]byte("raw-session-token"))
	return cloneLease(value), store.lastToken, nil
}

func fakeLeaseOwnerMatches(
	value *SessionLease,
	workspace string,
	owner authority.SessionOwner,
	now time.Time,
) bool {
	if value == nil || value.WorkspaceKey != workspace || value.AgentID != owner.AgentID ||
		value.NodeID != owner.NodeID || value.LeaseID != owner.LeaseID ||
		value.FencingToken != owner.FencingToken || value.Status != "active" || !now.Before(value.ExpiresAt) {
		return false
	}
	return true
}

type fakeTerminalStore struct {
	values map[string]*TerminalSession
	leases map[string]*SessionLease
	now    func() time.Time
}

func (store *fakeTerminalStore) Create(
	_ context.Context,
	owner authority.SessionOwner,
	command OpenTerminalCommand,
) (*TerminalSession, error) {
	if !fakeLeaseOwnerMatches(store.leases[owner.SessionID], command.WorkspaceKey, owner, store.now()) {
		return nil, ErrNotOwner
	}
	value := &TerminalSession{
		WorkspaceKey: command.WorkspaceKey, TerminalID: command.TerminalID,
		SessionID: command.SessionID, AgentID: command.AgentID, NodeID: command.NodeID,
		Status: TerminalRunning, PTYProvider: command.PTYProvider, StreamRef: command.StreamRef,
	}
	store.values[command.TerminalID] = cloneTerminal(value)
	return cloneTerminal(value), nil
}

func (store *fakeTerminalStore) Get(_ context.Context, _, terminalID string) (*TerminalSession, error) {
	value := store.values[terminalID]
	if value == nil {
		return nil, ErrNotFound
	}
	return cloneTerminal(value), nil
}

func (store *fakeTerminalStore) Update(
	_ context.Context,
	owner authority.SessionOwner,
	workspace string,
	terminalID string,
	update TerminalUpdate,
) (*TerminalSession, error) {
	if !fakeLeaseOwnerMatches(store.leases[owner.SessionID], workspace, owner, store.now()) {
		return nil, ErrNotOwner
	}
	value := store.values[terminalID]
	if value == nil {
		return nil, ErrNotFound
	}
	if update.Status != nil {
		value.Status = *update.Status
	}
	if update.StreamRef != nil {
		value.StreamRef = *update.StreamRef
	}
	if update.TranscriptArtifactID != nil {
		value.TranscriptArtifactID = *update.TranscriptArtifactID
	}
	if update.AttachedClients != nil {
		value.AttachedClients = *update.AttachedClients
	}
	if update.LastSeenAt != nil {
		value.LastSeenAt = *update.LastSeenAt
	}
	if update.EndedAt != nil {
		ended := *update.EndedAt
		value.EndedAt = &ended
	}
	return cloneTerminal(value), nil
}

type fakeInboxStore struct {
	leases       map[string]*SessionLease
	now          func() time.Time
	claimMutator func(*InboxMessage, time.Time)
}

func (*fakeInboxStore) Enqueue(_ context.Context, command EnqueueInboxCommand) (*InboxMessage, error) {
	return &InboxMessage{
		WorkspaceKey: command.WorkspaceKey, MessageID: command.MessageID,
		TargetAgentID: command.TargetAgentID, SessionID: command.SessionID,
		Body: command.Body, Status: InboxQueued,
	}, nil
}

func (store *fakeInboxStore) ClaimNext(
	_ context.Context,
	owner authority.SessionOwner,
	command ClaimInboxCommand,
) (*InboxMessage, error) {
	if !fakeLeaseOwnerMatches(store.leases[owner.SessionID], command.WorkspaceKey, owner, store.now()) {
		return nil, ErrNotOwner
	}
	expiresAt := store.now().Add(command.LeaseTTL)
	value := &InboxMessage{
		WorkspaceKey: command.WorkspaceKey, MessageID: "message-1",
		TargetAgentID: command.AgentID, Status: InboxQueued,
		Attempt: 1, ClaimedBy: owner.SessionID, ClaimExpiresAt: &expiresAt,
	}
	if store.claimMutator != nil {
		store.claimMutator(value, store.now())
	}
	return value, nil
}

func (store *fakeInboxStore) Complete(
	_ context.Context,
	owner authority.SessionOwner,
	command CompleteInboxCommand,
) (*InboxMessage, error) {
	if !fakeLeaseOwnerMatches(store.leases[owner.SessionID], command.WorkspaceKey, owner, store.now()) {
		return nil, ErrNotOwner
	}
	return &InboxMessage{
		WorkspaceKey: command.WorkspaceKey, MessageID: command.MessageID,
		TargetAgentID: owner.AgentID, Attempt: command.Attempt, Status: command.Status,
	}, nil
}

type fakeActivitySource struct {
	values []Activity
	calls  int
}

func (source *fakeActivitySource) ListActivity(
	_ context.Context,
	_, _ string,
	_ int,
) ([]Activity, error) {
	source.calls++
	return append([]Activity(nil), source.values...), nil
}
