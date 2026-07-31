package fleetdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type authorityTransportStub struct {
	value       *SessionAuthorityValidationWire
	err         error
	captured    SessionAuthorityProofWire
	capturedRaw []byte
}

type mutationTransportStub struct {
	MutationTransport
	starts  int
	command interaction.StartSessionCommand
	result  interaction.SessionStart
	err     error
}

type startRecoveryTransportStub struct {
	MutationTransport
	startResults   []interaction.SessionStart
	startErrors    []error
	getResults     []*interaction.AgentSession
	getErrors      []error
	recoverResults []interaction.SessionStart
	recoverErrors  []error
	recoverCalls   []interaction.RecoverSessionStartCommand
	startCalls     int
	getCalls       int
}

func (stub *startRecoveryTransportStub) StartSession(
	context.Context,
	interaction.StartSessionCommand,
) (interaction.SessionStart, error) {
	index := stub.startCalls
	stub.startCalls++
	var result interaction.SessionStart
	var err error
	if index < len(stub.startResults) {
		result = stub.startResults[index]
	}
	if index < len(stub.startErrors) {
		err = stub.startErrors[index]
	}
	return result, err
}

func (stub *startRecoveryTransportStub) GetSession(
	context.Context,
	string,
	string,
) (*interaction.AgentSession, error) {
	index := stub.getCalls
	stub.getCalls++
	var result *interaction.AgentSession
	var err error
	if index < len(stub.getResults) {
		result = stub.getResults[index]
	}
	if index < len(stub.getErrors) {
		err = stub.getErrors[index]
	}
	return result, err
}

func (stub *startRecoveryTransportStub) RecoverSessionStart(
	_ context.Context,
	command interaction.RecoverSessionStartCommand,
) (interaction.SessionStart, error) {
	index := len(stub.recoverCalls)
	stub.recoverCalls = append(stub.recoverCalls, command)
	var result interaction.SessionStart
	var err error
	if index < len(stub.recoverResults) {
		result = stub.recoverResults[index]
	}
	if index < len(stub.recoverErrors) {
		err = stub.recoverErrors[index]
	}
	return result, err
}

func (stub *mutationTransportStub) StartSession(
	_ context.Context,
	command interaction.StartSessionCommand,
) (interaction.SessionStart, error) {
	stub.starts++
	stub.command = command
	return stub.result, stub.err
}

func (stub *authorityTransportStub) ValidateSessionAuthority(
	_ context.Context,
	proof SessionAuthorityProofWire,
) (*SessionAuthorityValidationWire, error) {
	stub.captured = proof
	stub.capturedRaw = proof.LeaseToken
	return stub.value, stub.err
}

func TestAuthorityAdapterValidatesExactGenerationAndClearsTransportTokenCopy(t *testing.T) {
	expiresAt := time.Date(2026, 7, 30, 12, 1, 0, 0, time.UTC)
	transport := &authorityTransportStub{value: &SessionAuthorityValidationWire{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		TerminalID: "terminal-1", NodeID: "node-1", LeaseID: "lease-1",
		FencingToken: 7, ExpiresAt: expiresAt,
	}}
	adapter, err := NewAuthorityAdapter(transport)
	if err != nil {
		t.Fatal(err)
	}
	token := interaction.NewLeaseToken([]byte("raw-session-token"))
	value, err := adapter.ValidateSessionAuthority(t.Context(), interaction.SessionAuthorityProof{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		TerminalID: "terminal-1", NodeID: "node-1", LeaseID: "lease-1",
		FencingToken: 7, Token: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if value == nil || value.TerminalID != "terminal-1" ||
		value.FencingToken != 7 || !value.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("validation = %+v", value)
	}
	if string(token.Bytes()) != "raw-session-token" {
		t.Fatal("adapter closed the caller-owned token before authority derivation")
	}
	for index, value := range transport.capturedRaw {
		if value != 0 {
			t.Fatalf("transport token byte %d was retained after validation", index)
		}
	}
	token.Close()
}

func TestAuthorityAdapterFailsClosedForTransportAndProjectionErrors(t *testing.T) {
	proof := interaction.SessionAuthorityProof{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
		Token: interaction.NewLeaseToken([]byte("token")),
	}
	t.Cleanup(proof.Token.Close)

	for name, testCase := range map[string]struct {
		transport *authorityTransportStub
		want      error
	}{
		"not owner": {
			transport: &authorityTransportStub{err: ErrTransportNotOwner},
			want:      interaction.ErrNotOwner,
		},
		"unavailable": {
			transport: &authorityTransportStub{err: errors.New("network down")},
			want:      interaction.ErrUnavailable,
		},
		"cross session": {
			transport: &authorityTransportStub{value: &SessionAuthorityValidationWire{
				WorkspaceKey: "WS", SessionID: "session-2", AgentID: "agent-1",
				NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
				ExpiresAt: time.Now().Add(time.Minute),
			}},
			want: interaction.ErrInvalidPersistedState,
		},
	} {
		t.Run(name, func(t *testing.T) {
			adapter, err := NewAuthorityAdapter(testCase.transport)
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.ValidateSessionAuthority(t.Context(), proof)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestAuthorityAdapterRejectsMissingTokenWithoutCallingTransport(t *testing.T) {
	transport := &authorityTransportStub{}
	adapter, err := NewAuthorityAdapter(transport)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.ValidateSessionAuthority(t.Context(), interaction.SessionAuthorityProof{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
	})
	if !errors.Is(err, interaction.ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
	if transport.captured.WorkspaceKey != "" {
		t.Fatalf("transport was called: %+v", transport.captured)
	}
}

func TestCompleteAdapterRefusesMissingCompoundMutationTransport(t *testing.T) {
	_, err := New(&authorityTransportStub{}, nil)
	if !errors.Is(err, interaction.ErrUnavailable) {
		t.Fatalf("error = %v, want Interaction unavailable", err)
	}
}

func TestCompleteAdapterDelegatesSessionStartAsOneAtomicTransportCommand(t *testing.T) {
	mutations := &mutationTransportStub{result: interaction.SessionStart{
		Session: &interaction.AgentSession{WorkspaceKey: "WS", SessionID: "session-1"},
		Lease:   &interaction.SessionLease{WorkspaceKey: "WS", LeaseID: "lease-1"},
		Token:   interaction.NewLeaseToken([]byte("raw-token")),
	}}
	adapter, err := New(&authorityTransportStub{}, mutations)
	if err != nil {
		t.Fatal(err)
	}
	command := interaction.StartSessionCommand{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseTTL: time.Minute,
	}
	result, err := adapter.Start(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if mutations.starts != 1 || mutations.command.SessionID != command.SessionID ||
		result.Session == nil || result.Lease == nil || result.Token == nil {
		t.Fatalf("starts=%d command=%+v result=%+v", mutations.starts, mutations.command, result)
	}
	result.Token.Close()
}

func TestStartRecoversOnlyExactDurableStartingDefinitionAfterAmbiguousResponse(t *testing.T) {
	command := startRecoveryCommandForTest()
	initial := startRecoverySessionForTest(command, command.LeaseID, 7)
	replacement := startRecoveryResultForTest(command, "replacement-1", 8)
	transport := &startRecoveryTransportStub{
		startErrors:    []error{ErrTransportUnavailable},
		getResults:     []*interaction.AgentSession{initial},
		recoverResults: []interaction.SessionStart{replacement},
	}
	adapter := startRecoveryAdapterForTest(t, transport, "replacement-1")
	result, err := adapter.Start(t.Context(), command)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(transport.recoverCalls) != 1 ||
		transport.recoverCalls[0].ExpectedLeaseID != command.LeaseID ||
		transport.recoverCalls[0].ExpectedLeaseFencingToken != 7 ||
		transport.recoverCalls[0].ReplacementLeaseID != "replacement-1" ||
		result.Lease == nil || result.Lease.LeaseID != "replacement-1" ||
		result.Token == nil {
		t.Fatalf("recover calls=%+v result=%+v", transport.recoverCalls, result)
	}
	result.Token.Close()
}

func TestStartRetriesOriginalDefinitionWhenAmbiguousInspectionIsAbsent(t *testing.T) {
	command := startRecoveryCommandForTest()
	retried := startRecoveryResultForTest(command, command.LeaseID, 7)
	transport := &startRecoveryTransportStub{
		startResults: []interaction.SessionStart{{}, retried},
		startErrors:  []error{ErrTransportUnavailable, nil},
		getErrors:    []error{ErrTransportNotFound},
	}
	adapter := startRecoveryAdapterForTest(t, transport)
	result, err := adapter.Start(t.Context(), command)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if transport.startCalls != 2 || len(transport.recoverCalls) != 0 ||
		result.Lease == nil || result.Lease.LeaseID != command.LeaseID {
		t.Fatalf(
			"starts=%d recover=%d result=%+v",
			transport.startCalls,
			len(transport.recoverCalls),
			result,
		)
	}
	result.Token.Close()
}

func TestStartNeverRecoversDefinitiveStartErrorOrMismatchedDefinition(t *testing.T) {
	command := startRecoveryCommandForTest()
	for name, testCase := range map[string]struct {
		startErr error
		session  *interaction.AgentSession
		want     error
		getCalls int
	}{
		"definitive conflict": {
			startErr: ErrTransportConflict,
			want:     interaction.ErrConflict,
		},
		"malformed successful response": {
			startErr: ErrTransportInvalidPersistedState,
			want:     interaction.ErrInvalidPersistedState,
		},
		"cancelled request": {
			startErr: errors.Join(ErrTransportUnavailable, context.Canceled),
			want:     interaction.ErrUnavailable,
		},
		"mismatched durable definition": {
			startErr: ErrTransportUnavailable,
			session: func() *interaction.AgentSession {
				value := startRecoverySessionForTest(command, command.LeaseID, 7)
				value.TaskID = "different-task"
				return value
			}(),
			want:     interaction.ErrInvalidPersistedState,
			getCalls: 1,
		},
		"original generation already replaced": {
			startErr: ErrTransportUnavailable,
			session: startRecoverySessionForTest(
				command,
				"concurrent-replacement",
				8,
			),
			want:     interaction.ErrConflict,
			getCalls: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			transport := &startRecoveryTransportStub{
				startErrors: []error{testCase.startErr},
				getResults:  []*interaction.AgentSession{testCase.session},
			}
			adapter := startRecoveryAdapterForTest(t, transport, "replacement-1")
			_, err := adapter.Start(t.Context(), command)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error=%v, want %v", err, testCase.want)
			}
			if transport.getCalls != testCase.getCalls ||
				len(transport.recoverCalls) != 0 {
				t.Fatalf(
					"get calls=%d recover calls=%d",
					transport.getCalls,
					len(transport.recoverCalls),
				)
			}
		})
	}
}

func TestStartRotatesAgainWhenRecoveryResponseIsLost(t *testing.T) {
	command := startRecoveryCommandForTest()
	initial := startRecoverySessionForTest(command, command.LeaseID, 7)
	afterLostResponse := startRecoverySessionForTest(command, "replacement-lost", 8)
	final := startRecoveryResultForTest(command, "replacement-final", 9)
	transport := &startRecoveryTransportStub{
		startErrors: []error{ErrTransportUnavailable},
		getResults: []*interaction.AgentSession{
			initial,
			afterLostResponse,
		},
		recoverResults: []interaction.SessionStart{{}, final},
		recoverErrors:  []error{ErrTransportUnavailable, nil},
	}
	adapter := startRecoveryAdapterForTest(
		t,
		transport,
		"replacement-lost",
		"replacement-final",
	)
	result, err := adapter.Start(t.Context(), command)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(transport.recoverCalls) != 2 ||
		transport.recoverCalls[1].ExpectedLeaseID != "replacement-lost" ||
		transport.recoverCalls[1].ExpectedLeaseFencingToken != 8 ||
		result.Lease == nil || result.Lease.LeaseID != "replacement-final" {
		t.Fatalf("recover calls=%+v result=%+v", transport.recoverCalls, result)
	}
	result.Token.Close()
}

func TestStartDoesNotRotateAConcurrentRecoveryGeneration(t *testing.T) {
	command := startRecoveryCommandForTest()
	initial := startRecoverySessionForTest(command, command.LeaseID, 7)
	concurrent := startRecoverySessionForTest(command, "other-recovery", 8)
	transport := &startRecoveryTransportStub{
		startErrors:   []error{ErrTransportUnavailable},
		getResults:    []*interaction.AgentSession{initial, concurrent},
		recoverErrors: []error{ErrTransportUnavailable},
	}
	adapter := startRecoveryAdapterForTest(t, transport, "our-replacement")
	_, err := adapter.Start(t.Context(), command)
	if !errors.Is(err, interaction.ErrConflict) {
		t.Fatalf("error=%v, want ErrConflict", err)
	}
	if len(transport.recoverCalls) != 1 {
		t.Fatalf("recover calls=%+v", transport.recoverCalls)
	}
}

func TestStartBoundsLostRecoveryResponseRotations(t *testing.T) {
	command := startRecoveryCommandForTest()
	transport := &startRecoveryTransportStub{
		startErrors: []error{ErrTransportUnavailable},
		getResults: []*interaction.AgentSession{
			startRecoverySessionForTest(command, command.LeaseID, 7),
			startRecoverySessionForTest(command, "replacement-1", 8),
			startRecoverySessionForTest(command, "replacement-2", 9),
			startRecoverySessionForTest(command, "replacement-3", 10),
		},
		recoverErrors: []error{
			ErrTransportUnavailable,
			ErrTransportUnavailable,
			ErrTransportUnavailable,
		},
	}
	adapter := startRecoveryAdapterForTest(
		t,
		transport,
		"replacement-1",
		"replacement-2",
		"replacement-3",
	)
	_, err := adapter.Start(t.Context(), command)
	if !errors.Is(err, interaction.ErrUnavailable) {
		t.Fatalf("error=%v, want ErrUnavailable", err)
	}
	if len(transport.recoverCalls) != interactionStartRecoveryLimit ||
		transport.getCalls != interactionStartRecoveryLimit+1 {
		t.Fatalf(
			"recover calls=%d get calls=%d, want %d/%d",
			len(transport.recoverCalls),
			transport.getCalls,
			interactionStartRecoveryLimit,
			interactionStartRecoveryLimit+1,
		)
	}
}

func startRecoveryCommandForTest() interaction.StartSessionCommand {
	return interaction.StartSessionCommand{
		WorkspaceKey: "WS",
		SessionID:    "session-lost-start",
		AgentID:      "agent-1",
		NodeID:       "node-1",
		Kind:         interaction.SessionKindInteractive,
		TaskID:       "TASK-1",
		TerminalID:   "terminal-1",
		Phase:        "starting",
		Attempt:      1,
		LeaseID:      "original-lease",
		LeaseTTL:     time.Minute,
		Metadata:     map[string]string{"intent": "stable"},
	}
}

func startRecoverySessionForTest(
	command interaction.StartSessionCommand,
	leaseID string,
	fence int64,
) *interaction.AgentSession {
	return &interaction.AgentSession{
		WorkspaceKey:             command.WorkspaceKey,
		SessionID:                command.SessionID,
		AgentID:                  command.AgentID,
		NodeID:                   command.NodeID,
		Kind:                     command.Kind,
		TaskID:                   command.TaskID,
		TerminalID:               command.TerminalID,
		ParentSessionID:          command.ParentSessionID,
		Status:                   interaction.SessionStarting,
		CurrentLeaseID:           leaseID,
		CurrentLeaseFencingToken: fence,
		Phase:                    command.Phase,
		Attempt:                  command.Attempt,
		Metadata:                 map[string]string{"intent": "stable"},
	}
}

func startRecoveryResultForTest(
	command interaction.StartSessionCommand,
	leaseID string,
	fence int64,
) interaction.SessionStart {
	return interaction.SessionStart{
		Session: startRecoverySessionForTest(command, leaseID, fence),
		Lease: &interaction.SessionLease{
			WorkspaceKey: command.WorkspaceKey,
			LeaseID:      leaseID,
			SessionID:    command.SessionID,
			AgentID:      command.AgentID,
			NodeID:       command.NodeID,
			FencingToken: fence,
			Status:       "active",
			ExpiresAt:    time.Now().Add(time.Minute),
		},
		Token: interaction.NewLeaseToken([]byte("raw-replacement-token")),
	}
}

func startRecoveryAdapterForTest(
	t *testing.T,
	transport MutationTransport,
	leaseIDs ...string,
) *Adapter {
	t.Helper()
	adapter, err := New(&authorityTransportStub{}, transport)
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	adapter.newLeaseID = func() (string, error) {
		if index >= len(leaseIDs) {
			return "unexpected-extra-replacement", nil
		}
		value := leaseIDs[index]
		index++
		return value, nil
	}
	return adapter
}
