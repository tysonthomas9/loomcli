package artifacts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type fakeSessionStore struct {
	artifact  *Artifact
	content   []byte
	creates   int
	uploads   int
	finalizes int
}

func (store *fakeSessionStore) CreateSession(_ context.Context, owner SessionOwner, command CreateCommand) (*Artifact, error) {
	store.creates++
	if store.artifact != nil {
		return nil, ErrAlreadyExists
	}
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	store.artifact = &Artifact{
		WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID, AgentID: owner.AgentID,
		SessionID: owner.SessionID, TaskID: command.TaskID, OwnerType: OwnerSession, OwnerID: owner.SessionID,
		Type: command.Type, URI: command.URI, Summary: command.Summary, MIMEType: command.MIMEType,
		SizeBytes: command.SizeBytes, Checksum: command.Checksum, ContentHash: command.ContentHash,
		Visibility: command.Visibility, RedactionStatus: command.RedactionStatus,
		DurableStatus: StatusDeclared, Metadata: cloneMetadata(command.Metadata), CreatedAt: now, UpdatedAt: now,
	}
	return cloneArtifact(store.artifact), nil
}

func (store *fakeSessionStore) UploadSession(_ context.Context, _ SessionOwner, command UploadCommand) (*Artifact, error) {
	store.uploads++
	store.content = append([]byte(nil), command.Content...)
	store.artifact.SizeBytes = int64(len(command.Content))
	store.artifact.ContentHash = artifactContentHash(command.Content)
	store.artifact.MIMEType = command.MIMEType
	store.artifact.DurableStatus = StatusUploading
	return cloneArtifact(store.artifact), nil
}

func (store *fakeSessionStore) FinalizeSession(_ context.Context, _ SessionOwner, command FinalizeCommand) (*Artifact, error) {
	store.finalizes++
	now := time.Date(2026, 8, 4, 10, 1, 0, 0, time.UTC)
	store.artifact.DurableStatus = StatusFinalized
	store.artifact.FinalizedAt = &now
	if command.ContentHash != nil {
		store.artifact.ContentHash = *command.ContentHash
	}
	return cloneArtifact(store.artifact), nil
}

func (store *fakeSessionStore) GetSession(_ context.Context, _ SessionOwner, _ GetQuery) (*Artifact, error) {
	if store.artifact == nil {
		return nil, ErrNotFound
	}
	return cloneArtifact(store.artifact), nil
}

func TestSessionServiceOwnsRetrySafeContentLifecycle(t *testing.T) {
	store := &fakeSessionStore{}
	service, issuer, owner := newSessionServiceFixture(t, store)
	auth := sessionContentAuthorities(t, issuer, owner)
	command := SessionContentCommand{
		ArtifactID: "transcript-session-1", TaskID: "TASK-1", Type: "transcript",
		Summary: "interactive session transcript", MIMEType: "application/x-ndjson",
		Metadata: map[string]string{"backend": "codex"}, Content: []byte("{\"text\":\"hello\"}\n"),
	}

	artifact, err := service.CreateContent(t.Context(), auth, owner, command)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ArtifactID != command.ArtifactID || artifact.OwnerType != OwnerSession ||
		artifact.OwnerID != owner.SessionID || artifact.AgentID != owner.AgentID ||
		artifact.SessionID != owner.SessionID || artifact.DurableStatus != StatusFinalized ||
		artifact.FinalizedAt == nil || artifact.ContentHash != artifactContentHash(command.Content) {
		t.Fatalf("finalized artifact = %#v", artifact)
	}
	if store.creates != 1 || store.uploads != 1 || store.finalizes != 1 {
		t.Fatalf("lifecycle calls = create %d upload %d finalize %d", store.creates, store.uploads, store.finalizes)
	}

	replayed, err := service.CreateContent(t.Context(), auth, owner, command)
	if err != nil || replayed.ArtifactID != command.ArtifactID {
		t.Fatalf("exact replay = %#v, %v", replayed, err)
	}
	if store.creates != 2 || store.uploads != 1 || store.finalizes != 1 {
		t.Fatalf("exact replay rewrote content: create %d upload %d finalize %d", store.creates, store.uploads, store.finalizes)
	}

	command.Content = []byte("{\"text\":\"different\"}\n")
	if _, err := service.CreateContent(t.Context(), auth, owner, command); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("divergent replay error = %v, want ErrAlreadyExists", err)
	}
	if store.uploads != 1 || store.finalizes != 1 {
		t.Fatalf("divergent replay mutated content: upload %d finalize %d", store.uploads, store.finalizes)
	}
}

func TestSessionServiceRejectsForeignAuthorityAndGeneration(t *testing.T) {
	service, _, owner := newSessionServiceFixture(t, &fakeSessionStore{})
	foreign := authority.NewIssuer()
	auth := sessionContentAuthorities(t, foreign, owner)
	command := SessionContentCommand{ArtifactID: "transcript-session-1", Type: "transcript", Content: []byte("x")}
	if _, err := service.CreateContent(t.Context(), auth, owner, command); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("foreign authority error = %v, want admission denied", err)
	}

	service, issuer, owner := newSessionServiceFixture(t, &fakeSessionStore{})
	auth = sessionContentAuthorities(t, issuer, owner)
	owner.FencingToken++
	if _, err := service.CreateContent(t.Context(), auth, owner, command); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("wrong generation error = %v, want ErrNotOwner", err)
	}
}

func TestSessionServiceRejectsCrossOwnerPersistedArtifact(t *testing.T) {
	store := &fakeSessionStore{}
	service, issuer, owner := newSessionServiceFixture(t, store)
	store.artifact = &Artifact{
		WorkspaceKey: owner.WorkspaceKey, ArtifactID: "transcript-session-1", AgentID: "other-agent",
		SessionID: owner.SessionID, OwnerType: OwnerSession, OwnerID: owner.SessionID,
		Type: "transcript", DurableStatus: StatusFinalized, ContentHash: artifactContentHash([]byte("x")),
		FinalizedAt: ptrTime(time.Now()),
	}
	_, err := service.CreateContent(t.Context(), sessionContentAuthorities(t, issuer, owner), owner, SessionContentCommand{
		ArtifactID: "transcript-session-1", Type: "transcript", Content: []byte("x"),
	})
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("cross-owner result error = %v, want ErrInvalidPersistedState", err)
	}
}

func newSessionServiceFixture(t *testing.T, store SessionStore) (*SessionService, *authority.Issuer, SessionOwner) {
	t.Helper()
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewSession(store, admission)
	if err != nil {
		t.Fatal(err)
	}
	owner := SessionOwner{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
	}
	return service, issuer, owner
}

func sessionContentAuthorities(t *testing.T, issuer *authority.Issuer, owner SessionOwner) SessionContentAuthorities {
	t.Helper()
	actions := []authority.Action{ActionDeclare, ActionGet, ActionUpload, ActionFinalize}
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "session:" + owner.SessionID, Class: authority.ClassSession, Workspace: owner.WorkspaceKey,
		Actions: actions, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	platformOwner := authority.SessionOwner{
		SessionID: owner.SessionID, AgentID: owner.AgentID, NodeID: owner.NodeID,
		LeaseID: owner.LeaseID, FencingToken: owner.FencingToken,
	}
	issue := func(action authority.Action) authority.SessionAuthority {
		value, issueErr := issuer.IssueSessionForOwner(principal, owner.WorkspaceKey, action, platformOwner)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		return value
	}
	return SessionContentAuthorities{
		Declare: issue(ActionDeclare), Get: issue(ActionGet), Upload: issue(ActionUpload), Finalize: issue(ActionFinalize),
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
