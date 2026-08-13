package serve

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	executionmodule "github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type artifactsInfraTransportStub struct {
	createOwner     infrafleetdb.ArtifactOwner
	createCommand   infrafleetdb.ArtifactCreateCommand
	createResult    *infrafleetdb.Artifact
	getOwner        infrafleetdb.ArtifactOwner
	getArtifactID   string
	getResult       *infrafleetdb.Artifact
	listResult      []*infrafleetdb.Artifact
	referenceResult infrafleetdb.ArtifactReferenceResult
	err             error
}

func (stub *artifactsInfraTransportStub) Create(_ context.Context, owner infrafleetdb.ArtifactOwner, command infrafleetdb.ArtifactCreateCommand) (*infrafleetdb.Artifact, error) {
	stub.createOwner = owner
	stub.createCommand = command
	return stub.createResult, stub.err
}

func (stub *artifactsInfraTransportStub) Upload(context.Context, infrafleetdb.ArtifactOwner, infrafleetdb.ArtifactUploadCommand) (*infrafleetdb.Artifact, error) {
	return nil, stub.err
}

func (stub *artifactsInfraTransportStub) Finalize(context.Context, infrafleetdb.ArtifactOwner, infrafleetdb.ArtifactFinalizeCommand) (*infrafleetdb.Artifact, error) {
	return nil, stub.err
}

func (stub *artifactsInfraTransportStub) Fail(context.Context, infrafleetdb.ArtifactOwner, infrafleetdb.ArtifactFailCommand) (*infrafleetdb.Artifact, error) {
	return nil, stub.err
}

func (stub *artifactsInfraTransportStub) Reference(context.Context, infrafleetdb.ArtifactOwner, infrafleetdb.ArtifactReferenceCommand) (infrafleetdb.ArtifactReferenceResult, error) {
	return stub.referenceResult, stub.err
}

func (stub *artifactsInfraTransportStub) Get(_ context.Context, owner infrafleetdb.ArtifactOwner, artifactID string) (*infrafleetdb.Artifact, error) {
	stub.getOwner = owner
	stub.getArtifactID = artifactID
	return stub.getResult, stub.err
}

func (stub *artifactsInfraTransportStub) List(context.Context, infrafleetdb.ArtifactOwner, infrafleetdb.ArtifactFilter) ([]*infrafleetdb.Artifact, error) {
	return stub.listResult, stub.err
}

func TestNewArtifactsCapabilityFailsClosedWithoutSharedTransport(t *testing.T) {
	executionCapability := &ExecutionCapability{issuer: authority.NewIssuer()}
	queries := memstore.New().ArtifactQueries()
	capability, err := executionCapability.NewArtifactsCapability(nil, queries)
	if capability != nil || !errors.Is(err, artifacts.ErrUnavailable) {
		t.Fatalf("execution.NewArtifactsCapability(nil, queries) = %#v, %v; want nil, ErrUnavailable", capability, err)
	}
	if capability, err := (*ExecutionCapability)(nil).NewArtifactsCapability(&artifactsInfraTransportStub{}, queries); capability != nil || err == nil {
		t.Fatalf("nil Execution capability composition = %#v, %v; want fail closed", capability, err)
	}
	var nilCapability *ArtifactsCapability
	if api := nilCapability.ArtifactsAPI(); api != nil {
		t.Fatalf("nil capability API = %#v, want nil", api)
	}
	if queries := nilCapability.ArtifactQueries(); queries != nil {
		t.Fatalf("nil capability queries = %#v, want nil", queries)
	}
}

func TestArtifactsCapabilityPreservesOwnerAndCompleteCreateEnvelope(t *testing.T) {
	owner := artifacts.ExecutionOwner{
		WorkspaceKey: "workspace-1", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret-token", FencingToken: 42,
	}
	command := artifacts.CreateCommand{
		ArtifactID: "artifact-1", SessionID: "session-1", TaskID: "task-1", Type: "patch",
		URI: "artifact://artifact-1", Summary: "task patch", MIMEType: "text/x-diff", SizeBytes: 42,
		Checksum: "sha256:checksum", ContentHash: "sha256:content",
		Visibility: "private", RedactionStatus: "unredacted", Metadata: map[string]string{"runner": "local"},
	}
	stub := &artifactsInfraTransportStub{createResult: &infrafleetdb.Artifact{
		WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID,
		SessionID: command.SessionID, TaskID: command.TaskID, OwnerType: "task_run", OwnerID: owner.TaskRunID,
		Type: command.Type, URI: command.URI, Summary: command.Summary, MIMEType: command.MIMEType,
		SizeBytes: command.SizeBytes, Checksum: command.Checksum, ContentHash: command.ContentHash,
		Visibility: command.Visibility, RedactionStatus: command.RedactionStatus,
		DurableStatus: "declared", Metadata: map[string]string{"runner": "local"},
	}}
	st := memstore.New()
	executionCapability, err := NewExecutionCapability(executionTestDependencies(t, st))
	if err != nil {
		t.Fatalf("NewExecutionCapability: %v", err)
	}
	capability, err := executionCapability.NewArtifactsCapability(stub, st.ArtifactQueries())
	if err != nil {
		t.Fatalf("NewArtifactsCapability: %v", err)
	}
	auth, err := executionCapability.TaskRunAuthorityResolver().ResolveTaskRunAuthority(t.Context(), owner.WorkspaceKey, artifacts.ActionDeclare, executionmodule.Owner{
		ResourceKind: executionmodule.ResourceTaskRun, ResourceID: owner.TaskRunID,
		NodeID: owner.NodeID, LeaseID: owner.LeaseID, LeaseToken: owner.LeaseToken, FencingToken: owner.FencingToken,
	})
	if err != nil {
		t.Fatalf("resolve Artifact declare authority: %v", err)
	}
	result, err := capability.ArtifactsAPI().Create(t.Context(), auth, owner, command)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantOwner := infrafleetdb.ArtifactOwner{
		WorkspaceKey: owner.WorkspaceKey, TaskRunID: owner.TaskRunID, NodeID: owner.NodeID,
		LeaseID: owner.LeaseID, LeaseToken: owner.LeaseToken, FencingToken: owner.FencingToken,
	}
	wantCommand := infrafleetdb.ArtifactCreateCommand{
		ArtifactID: command.ArtifactID, SessionID: command.SessionID, TaskID: command.TaskID,
		Type: command.Type, URI: command.URI, Summary: command.Summary, MIMEType: command.MIMEType,
		SizeBytes: command.SizeBytes, Checksum: command.Checksum, ContentHash: command.ContentHash,
		Visibility: command.Visibility, RedactionStatus: command.RedactionStatus, Metadata: command.Metadata,
	}
	if stub.createOwner != wantOwner || stub.createCommand.ArtifactID != wantCommand.ArtifactID ||
		stub.createCommand.SessionID != wantCommand.SessionID || stub.createCommand.TaskID != wantCommand.TaskID ||
		stub.createCommand.Type != wantCommand.Type || stub.createCommand.URI != wantCommand.URI ||
		stub.createCommand.Summary != wantCommand.Summary || stub.createCommand.MIMEType != wantCommand.MIMEType ||
		stub.createCommand.SizeBytes != wantCommand.SizeBytes || stub.createCommand.Checksum != wantCommand.Checksum ||
		stub.createCommand.ContentHash != wantCommand.ContentHash || stub.createCommand.Visibility != wantCommand.Visibility ||
		stub.createCommand.RedactionStatus != wantCommand.RedactionStatus || !maps.Equal(stub.createCommand.Metadata, wantCommand.Metadata) {
		t.Fatalf("infra create = owner %#v command %#v, want owner %#v command %#v", stub.createOwner, stub.createCommand, wantOwner, wantCommand)
	}
	if result.SessionID != command.SessionID || result.TaskID != command.TaskID || result.URI != command.URI ||
		result.SizeBytes != command.SizeBytes || result.Checksum != command.Checksum || result.ContentHash != command.ContentHash {
		t.Fatalf("module result = %#v, want six create fields preserved", result)
	}
}

func TestExecutionTaskRunResolverWhitelistsEveryArtifactsOperation(t *testing.T) {
	executionCapability, err := NewExecutionCapability(executionTestDependencies(t, memstore.New()))
	if err != nil {
		t.Fatal(err)
	}
	owner := executionmodule.Owner{
		ResourceKind: executionmodule.ResourceTaskRun, ResourceID: "task-run-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 7,
	}
	for _, action := range []authority.Action{
		artifacts.ActionDeclare, artifacts.ActionGet, artifacts.ActionUpload,
		artifacts.ActionFinalize, artifacts.ActionFail,
		artifacts.ActionReference, artifacts.ActionList,
	} {
		auth, err := executionCapability.TaskRunAuthorityResolver().ResolveTaskRunAuthority(t.Context(), "WS", action, owner)
		if err != nil {
			t.Fatalf("resolve %s: %v", action, err)
		}
		if auth.Action() != action || auth.ResourceKind() != authority.ExecutionResourceTaskRun || auth.ResourceID() != owner.ResourceID {
			t.Fatalf("resolved %s as action=%s kind=%s id=%s", action, auth.Action(), auth.ResourceKind(), auth.ResourceID())
		}
	}
}

func TestArtifactsFleetDBTransportMapsCompleteSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	finalizedAt := now.Add(-time.Minute)
	owner := artifacts.ExecutionOwner{
		WorkspaceKey: "workspace-1", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret-token", FencingToken: 7,
	}
	stub := &artifactsInfraTransportStub{getResult: &infrafleetdb.Artifact{
		WorkspaceKey: owner.WorkspaceKey, ArtifactID: "artifact-1", SessionID: "session-1", TaskID: "task-1",
		OwnerType: "task_run", OwnerID: owner.TaskRunID, Type: "patch", URI: "artifact://artifact-1",
		Summary: "patch", MIMEType: "text/x-diff", SizeBytes: 9, Checksum: "sha256:a", ContentHash: "sha256:b",
		Visibility: "private", RedactionStatus: "unredacted", DurableStatus: "finalized",
		Metadata: map[string]string{"key": "value"}, FinalizedAt: &finalizedAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}}
	bridge := &artifactsFleetDBTransport{transport: stub}
	result, err := bridge.Get(t.Context(), owner, artifacts.GetQuery{ArtifactID: "artifact-1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if result.WorkspaceKey != stub.getResult.WorkspaceKey || result.ArtifactID != stub.getResult.ArtifactID ||
		result.SessionID != stub.getResult.SessionID || result.TaskID != stub.getResult.TaskID ||
		string(result.OwnerType) != stub.getResult.OwnerType || result.OwnerID != stub.getResult.OwnerID ||
		result.Type != stub.getResult.Type || result.URI != stub.getResult.URI || result.Summary != stub.getResult.Summary ||
		result.MIMEType != stub.getResult.MIMEType || result.SizeBytes != stub.getResult.SizeBytes ||
		result.Checksum != stub.getResult.Checksum || result.ContentHash != stub.getResult.ContentHash ||
		result.Visibility != stub.getResult.Visibility || result.RedactionStatus != stub.getResult.RedactionStatus ||
		string(result.DurableStatus) != stub.getResult.DurableStatus || !maps.Equal(result.Metadata, stub.getResult.Metadata) ||
		result.FinalizedAt == nil || !result.FinalizedAt.Equal(finalizedAt) ||
		!result.CreatedAt.Equal(stub.getResult.CreatedAt) || !result.UpdatedAt.Equal(stub.getResult.UpdatedAt) {
		t.Fatalf("mapped snapshot = %#v, want %#v", result, stub.getResult)
	}
	if stub.getArtifactID != "artifact-1" || stub.getOwner.LeaseToken != owner.LeaseToken {
		t.Fatalf("get inputs = owner %#v artifact %q", stub.getOwner, stub.getArtifactID)
	}
	stub.getResult.Metadata["key"] = "mutated"
	*stub.getResult.FinalizedAt = now.Add(time.Hour)
	if result.Metadata["key"] != "value" || !result.FinalizedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("mapped snapshot retained infra storage: %#v", result)
	}
}

func TestArtifactsFleetDBTransportMapsDurableReference(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	owner := artifacts.ExecutionOwner{
		WorkspaceKey: "workspace-1", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret-token", FencingToken: 7,
	}
	stub := &artifactsInfraTransportStub{referenceResult: infrafleetdb.ArtifactReferenceResult{
		Artifact: &infrafleetdb.Artifact{
			WorkspaceKey: owner.WorkspaceKey, ArtifactID: "artifact-1", OwnerType: "task_run", OwnerID: owner.TaskRunID,
			Type: "logs", DurableStatus: "finalized", Revision: 4, FinalizedAt: &now, CreatedAt: now, UpdatedAt: now,
		},
		Reference: &infrafleetdb.ArtifactReference{
			WorkspaceKey: owner.WorkspaceKey, ReferenceID: "reference-1", ArtifactID: "artifact-1",
			OwnerType: "task_run", OwnerID: owner.TaskRunID, Kind: "task-output",
			TargetRef: "task-run://task-run-1/output", CreatedAt: now,
		},
		Replayed: true,
	}}
	bridge := &artifactsFleetDBTransport{transport: stub}
	result, err := bridge.Reference(t.Context(), owner, artifacts.ReferenceCommand{
		ArtifactID: "artifact-1", Kind: "task-output", TargetRef: "task-run://task-run-1/output",
	})
	if err != nil {
		t.Fatalf("Reference: %v", err)
	}
	if result.Artifact == nil || result.Artifact.ArtifactID != "artifact-1" || result.Reference == nil ||
		result.Reference.ReferenceID != "reference-1" || result.Reference.Kind != "task-output" ||
		result.Reference.TargetRef != "task-run://task-run-1/output" || !result.Reference.CreatedAt.Equal(now) {
		t.Fatalf("mapped reference = %#v", result)
	}
}

func TestArtifactsFleetDBTransportErrorVocabulary(t *testing.T) {
	tests := []struct {
		input error
		want  error
	}{
		{infrafleetdb.ErrArtifactsNotFound, artifacts.ErrNotFound},
		{infrafleetdb.ErrArtifactsInvalid, artifacts.ErrInvalid},
		{infrafleetdb.ErrArtifactsConflict, artifacts.ErrAlreadyExists},
		{infrafleetdb.ErrArtifactsNotOwner, artifacts.ErrNotOwner},
		{infrafleetdb.ErrArtifactsInvalidTransition, artifacts.ErrInvalidTransition},
		{infrafleetdb.ErrArtifactsUnavailable, artifacts.ErrUnavailable},
	}
	for _, test := range tests {
		translated := translateArtifactsFleetDBError(test.input)
		if !errors.Is(translated, test.input) || !errors.Is(translated, test.want) {
			t.Fatalf("translate %v = %v, want original and %v", test.input, translated, test.want)
		}
	}
	unknown := errors.New("unknown")
	if translated := translateArtifactsFleetDBError(unknown); translated != unknown {
		t.Fatalf("unknown translation = %v, want identity", translated)
	}
}
