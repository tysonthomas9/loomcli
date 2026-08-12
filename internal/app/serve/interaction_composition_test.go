package serve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	artifacttranscript "github.com/tysonthomas9/loomcli/internal/modules/artifacts/transcript"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type interactionPersistenceStub struct{}

func (*interactionPersistenceStub) Start(context.Context, interaction.StartSessionCommand) (interaction.SessionStart, error) {
	return interaction.SessionStart{}, nil
}

func (*interactionPersistenceStub) RecoverStart(
	context.Context,
	interaction.RecoverSessionStartCommand,
) (interaction.SessionStart, error) {
	return interaction.SessionStart{}, nil
}

func (*interactionPersistenceStub) Get(context.Context, string, string) (*interaction.AgentSession, error) {
	return nil, nil
}

func (*interactionPersistenceStub) PatchOwned(
	context.Context,
	string,
	authority.SessionOwner,
	interaction.SessionPatch,
) (*interaction.AgentSession, *interaction.SessionLease, error) {
	return nil, nil, nil
}

func (*interactionPersistenceStub) HeartbeatOwned(
	context.Context,
	string,
	authority.SessionOwner,
	interaction.SessionHeartbeat,
) (*interaction.AgentSession, *interaction.SessionLease, error) {
	return nil, nil, nil
}

func (*interactionPersistenceStub) FinishOwned(
	context.Context,
	string,
	authority.SessionOwner,
	interaction.SessionFinish,
) (interaction.SessionFinishResult, error) {
	return interaction.SessionFinishResult{}, nil
}

func (*interactionPersistenceStub) ForceInterrupt(
	context.Context,
	interaction.ForceInterruptCommand,
) (interaction.ForceInterruptResult, error) {
	return interaction.ForceInterruptResult{}, nil
}

func (*interactionPersistenceStub) InterruptIfLeaseMissing(
	context.Context,
	string,
	string,
	time.Time,
) (*interaction.AgentSession, bool, error) {
	return nil, false, nil
}

func (*interactionPersistenceStub) ListRecoverable(
	context.Context,
	string,
	time.Time,
) ([]*interaction.AgentSession, error) {
	return nil, nil
}

func (*interactionPersistenceStub) CreateTerminal(
	context.Context,
	authority.SessionOwner,
	interaction.OpenTerminalCommand,
) (*interaction.TerminalSession, error) {
	return nil, nil
}

type interactionTerminalStoreStub struct{ *interactionPersistenceStub }

func (*interactionTerminalStoreStub) Create(
	context.Context,
	authority.SessionOwner,
	interaction.OpenTerminalCommand,
) (*interaction.TerminalSession, error) {
	return nil, nil
}

func (*interactionTerminalStoreStub) Get(
	context.Context,
	string,
	string,
) (*interaction.TerminalSession, error) {
	return nil, nil
}

func (*interactionTerminalStoreStub) Update(
	context.Context,
	authority.SessionOwner,
	string,
	string,
	interaction.TerminalUpdate,
) (*interaction.TerminalSession, error) {
	return nil, nil
}

type interactionInboxStoreStub struct{}

func (*interactionInboxStoreStub) Enqueue(
	context.Context,
	interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	return nil, nil
}

func (*interactionInboxStoreStub) ClaimNext(
	context.Context,
	authority.SessionOwner,
	interaction.ClaimInboxCommand,
) (*interaction.InboxMessage, error) {
	return nil, nil
}

func (*interactionInboxStoreStub) Complete(
	context.Context,
	authority.SessionOwner,
	interaction.CompleteInboxCommand,
) (*interaction.InboxMessage, error) {
	return nil, nil
}

type interactionActivityStub struct{}

func (*interactionActivityStub) ListActivity(
	context.Context,
	string,
	string,
	int,
) ([]interaction.Activity, error) {
	return nil, nil
}

type interactionTranscriptStoreStub struct{}

func (*interactionTranscriptStoreStub) CreateContent(
	context.Context,
	authority.SessionAuthority,
	interaction.TranscriptArtifactCreate,
) (string, error) {
	return "transcript-session-1", nil
}

func TestInteractionTranscriptArtifactStoreRejectsDivergentRetry(t *testing.T) {
	issuer := authority.NewIssuer()
	persistence := &sessionArtifactTransportStub{}
	adapter, err := newInteractionTranscriptArtifactStore(persistence, issuer)
	if err != nil {
		t.Fatal(err)
	}
	auth := interactionTranscriptTestAuthority(t, issuer)
	command := interaction.TranscriptArtifactCreate{
		WorkspaceKey: "WS",
		ArtifactID:   "transcript-session-1",
		AgentID:      "agent-1",
		SessionID:    "session-1",
		Content:      canonicalInteractionTranscript(t, "first"),
	}
	if artifactID, err := adapter.CreateContent(t.Context(), auth, command); err != nil || artifactID != command.ArtifactID {
		t.Fatalf("first transcript publish = %q, %v", artifactID, err)
	}
	if artifactID, err := adapter.CreateContent(t.Context(), auth, command); err != nil || artifactID != command.ArtifactID {
		t.Fatalf("exact transcript replay = %q, %v", artifactID, err)
	}
	command.Content = canonicalInteractionTranscript(t, "different")
	if _, err := adapter.CreateContent(t.Context(), auth, command); !errors.Is(err, interaction.ErrConflict) {
		t.Fatalf("divergent transcript replay error = %v, want conflict", err)
	}
}

func TestInteractionTranscriptArtifactStorePreservesLeaseCredentialForSessionAttachment(t *testing.T) {
	issuer := authority.NewIssuer()
	adapter, err := newInteractionTranscriptArtifactStore(&sessionArtifactTransportStub{}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "session:session-1", Class: authority.ClassSession, Workspace: "WS",
		Actions: []authority.Action{interaction.ActionPublishTranscript}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	const credential = "one-use-session-credential"
	auth, err := issuer.IssueSessionForOwnerWithCredential(
		principal,
		"WS",
		interaction.ActionPublishTranscript,
		authority.SessionOwner{
			SessionID: "session-1", AgentID: "agent-1", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1,
		},
		[]byte(credential),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.CreateContent(t.Context(), auth, interaction.TranscriptArtifactCreate{
		WorkspaceKey: "WS", ArtifactID: "transcript-session-1", AgentID: "agent-1", SessionID: "session-1",
		Content: canonicalInteractionTranscript(t, "hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := auth.SessionOwner().ConsumeLeaseCredential()
	defer clear(got)
	if string(got) != credential {
		t.Fatalf("lease credential after Artifact lifecycle = %q, want preserved one-use credential", got)
	}
}

func TestInteractionTranscriptArtifactStoreRejectsForeignPublishAuthority(t *testing.T) {
	issuer := authority.NewIssuer()
	adapter, err := newInteractionTranscriptArtifactStore(&sessionArtifactTransportStub{}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	foreignAuthority := interactionTranscriptTestAuthority(t, authority.NewIssuer())
	_, err = adapter.CreateContent(t.Context(), foreignAuthority, interaction.TranscriptArtifactCreate{
		WorkspaceKey: "WS", ArtifactID: "transcript-session-1", AgentID: "agent-1", SessionID: "session-1",
		Content: canonicalInteractionTranscript(t, "hello"),
	})
	if !errors.Is(err, authority.ErrAdmissionDenied) || !errors.Is(err, interaction.ErrNotOwner) {
		t.Fatalf("foreign publish authority error = %v, want admission denied and not owner", err)
	}
}

type sessionArtifactTransportStub struct {
	artifact *infrafleetdb.Artifact
}

func (stub *sessionArtifactTransportStub) CreateSession(
	_ context.Context,
	owner infrafleetdb.SessionArtifactOwner,
	command infrafleetdb.SessionArtifactCreateCommand,
) (*infrafleetdb.Artifact, error) {
	if stub.artifact != nil {
		return nil, infrafleetdb.ErrArtifactsConflict
	}
	now := time.Now()
	stub.artifact = &infrafleetdb.Artifact{
		WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID, AgentID: owner.AgentID,
		SessionID: owner.SessionID, TaskID: command.TaskID, OwnerType: "session", OwnerID: owner.SessionID,
		Type: command.Type, Summary: command.Summary, MIMEType: command.MIMEType,
		SizeBytes: command.SizeBytes, ContentHash: command.ContentHash, Visibility: command.Visibility,
		RedactionStatus: command.RedactionStatus, DurableStatus: "declared", Metadata: command.Metadata,
		CreatedAt: now, UpdatedAt: now,
	}
	return cloneSessionArtifact(stub.artifact), nil
}

func (stub *sessionArtifactTransportStub) UploadSession(
	_ context.Context,
	_ infrafleetdb.SessionArtifactOwner,
	command infrafleetdb.ArtifactUploadCommand,
) (*infrafleetdb.Artifact, error) {
	sum := sha256.Sum256(command.Content)
	stub.artifact.ContentHash = "sha256:" + hex.EncodeToString(sum[:])
	stub.artifact.SizeBytes = int64(len(command.Content))
	stub.artifact.MIMEType = command.MIMEType
	stub.artifact.DurableStatus = "uploading"
	return cloneSessionArtifact(stub.artifact), nil
}

func (stub *sessionArtifactTransportStub) FinalizeSession(
	_ context.Context,
	_ infrafleetdb.SessionArtifactOwner,
	_ infrafleetdb.ArtifactFinalizeCommand,
) (*infrafleetdb.Artifact, error) {
	now := time.Now()
	stub.artifact.DurableStatus = "finalized"
	stub.artifact.FinalizedAt = &now
	return cloneSessionArtifact(stub.artifact), nil
}

func (stub *sessionArtifactTransportStub) FailSession(
	_ context.Context,
	_ infrafleetdb.SessionArtifactOwner,
	command infrafleetdb.ArtifactFailCommand,
) (*infrafleetdb.Artifact, error) {
	stub.artifact.DurableStatus = "failed"
	stub.artifact.FinalizedAt = nil
	stub.artifact.Metadata = cloneInteractionMap(command.Metadata)
	return cloneSessionArtifact(stub.artifact), nil
}

func (stub *sessionArtifactTransportStub) GetSession(
	context.Context,
	infrafleetdb.SessionArtifactOwner,
	string,
) (*infrafleetdb.Artifact, error) {
	if stub.artifact == nil {
		return nil, infrafleetdb.ErrArtifactsNotFound
	}
	return cloneSessionArtifact(stub.artifact), nil
}

func cloneSessionArtifact(value *infrafleetdb.Artifact) *infrafleetdb.Artifact {
	if value == nil {
		return nil
	}
	result := *value
	result.Metadata = cloneInteractionMap(value.Metadata)
	if value.FinalizedAt != nil {
		finalized := *value.FinalizedAt
		result.FinalizedAt = &finalized
	}
	return &result
}

func interactionTranscriptTestAuthority(t *testing.T, issuer *authority.Issuer) authority.SessionAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "session:session-1", Class: authority.ClassSession, Workspace: "WS",
		Actions: []authority.Action{interaction.ActionPublishTranscript}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueSessionForOwner(principal, "WS", interaction.ActionPublishTranscript, authority.SessionOwner{
		SessionID: "session-1", AgentID: "agent-1", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func canonicalInteractionTranscript(t *testing.T, text string) []byte {
	t.Helper()
	value, err := json.Marshal(artifacttranscript.Event{
		Seq: 1, Timestamp: time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC),
		Role: artifacttranscript.RoleAssistant, Type: artifacttranscript.EventText, Text: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return append(value, '\n')
}

type interactionForceCommandsStub struct {
	auth    authority.SystemAuthority
	command interaction.ForceInterruptCommand
	calls   int
}

func (stub *interactionForceCommandsStub) ForceInterrupt(
	_ context.Context,
	auth authority.SystemAuthority,
	command interaction.ForceInterruptCommand,
) (interaction.ForceInterruptResult, error) {
	stub.calls++
	stub.auth = auth
	stub.command = command
	return interaction.ForceInterruptResult{Changed: true}, nil
}

func TestInteractionForceInterrupterDerivesOneExactRuntimeAuthority(t *testing.T) {
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	commands := &interactionForceCommandsStub{}
	interrupter := newInteractionForceInterrupter(
		commands,
		newInteractionRuntimeAuthorityProvider(issuer, func() time.Time { return now }),
	)
	command := interaction.ForceInterruptCommand{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		TerminalID: "terminal-1", ExpectedLeaseID: "lease-1",
		ExpectedLeaseFencingToken: 7, StreamRef: "terminal:WS/tab-1",
		TerminalTab: "tab-1", Reason: "interactive agent stop",
	}
	result, err := interrupter.ForceInterrupt(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || commands.calls != 1 || commands.command != command {
		t.Fatalf("force interrupt result=%+v calls=%d command=%+v", result, commands.calls, commands.command)
	}
	if commands.auth.Subject() != string(interaction.TerminalLifecycleComponentID) ||
		commands.auth.Workspace() != command.WorkspaceKey ||
		commands.auth.Action() != interaction.ActionForceInterrupt {
		t.Fatalf("force interrupt authority = %+v", commands.auth)
	}
}

func TestNewInteractionCapabilityPublishesAPIResolverAndRecoveryRegistration(t *testing.T) {
	persistence := &interactionPersistenceStub{}
	activity := &interactionActivityStub{}
	validator := &interactionAuthorityValidatorStub{}
	issuer := authority.NewIssuer()
	capability, err := NewInteractionCapabilityWithIssuer(
		InteractionConfig{WorkspaceKey: "WS"},
		InteractionDependencies{
			Sessions:         persistence,
			Transcripts:      &interactionTranscriptStoreStub{},
			Terminals:        &interactionTerminalStoreStub{persistence},
			Inbox:            &interactionInboxStoreStub{},
			Activity:         activity,
			SessionAuthority: validator,
		},
		issuer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if capability.InteractionAPI() == nil || capability.SessionAuthorityResolver() == nil ||
		capability.InboxEnqueuer() == nil || capability.ForceInterrupter() == nil {
		t.Fatal("composed Interaction omitted API, session resolver, inbox enqueuer, or force interrupter")
	}
	registrations := capability.RuntimeRegistrations()
	if len(registrations) != 1 ||
		registrations[0].Component.ID() != interaction.SessionRecoveryComponentID ||
		!registrations[0].Policy.Immediate {
		t.Fatalf("runtime registrations = %+v", registrations)
	}
	registrations[0].Policy.Immediate = false
	if !capability.RuntimeRegistrations()[0].Policy.Immediate {
		t.Fatal("runtime registrations accessor did not return an isolated slice")
	}

	standalone, err := NewInteractionCapability(
		InteractionConfig{WorkspaceKey: "WS"},
		InteractionDependencies{
			Sessions:         persistence,
			Transcripts:      &interactionTranscriptStoreStub{},
			Terminals:        &interactionTerminalStoreStub{persistence},
			Inbox:            &interactionInboxStoreStub{},
			Activity:         activity,
			SessionAuthority: validator,
		},
	)
	if err != nil || standalone.InteractionAPI() == nil {
		t.Fatalf("standalone Interaction = %+v, error = %v", standalone, err)
	}
}

func TestNewInteractionCapabilityFailsClosedWithoutCompoundPort(t *testing.T) {
	_, err := NewInteractionCapabilityWithIssuer(
		InteractionConfig{WorkspaceKey: "WS"},
		InteractionDependencies{},
		authority.NewIssuer(),
	)
	if !errors.Is(err, interaction.ErrUnavailable) {
		t.Fatalf("error = %v, want Interaction unavailable", err)
	}
}

func TestNewInteractionCapabilityWithFleetDBPublishesCompleteProductionSurface(t *testing.T) {
	client, err := infrafleetdb.New(infrafleetdb.Config{BaseURL: "http://unused.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := NewInteractionCapabilityWithFleetDB(
		InteractionConfig{WorkspaceKey: "WS"},
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	if capability.InteractionAPI() == nil ||
		capability.SessionAuthorityResolver() == nil ||
		capability.OperatorAuthorityResolver() == nil ||
		capability.ForceInterrupter() == nil {
		t.Fatal("production Interaction composition omitted a published surface")
	}
	registrations := capability.RuntimeRegistrations()
	if len(registrations) != 1 ||
		registrations[0].Component.ID() != interaction.SessionRecoveryComponentID {
		t.Fatalf("runtime registrations = %+v", registrations)
	}
}

func TestNewInteractionCapabilityWithFleetDBFailsClosedWithoutExternalResolver(t *testing.T) {
	client, err := infrafleetdb.New(infrafleetdb.Config{BaseURL: "http://unused.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewInteractionCapabilityWithFleetDB(
		InteractionConfig{WorkspaceKey: "WS", ExternalAuth: true},
		client,
	)
	if err == nil {
		t.Fatal("external-auth Interaction composition accepted no operator resolver")
	}
}
