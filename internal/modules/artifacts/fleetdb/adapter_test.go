package fleetdb

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
)

type transportStub struct {
	err           error
	createOwner   artifacts.ExecutionOwner
	createCommand artifacts.CreateCommand
}

func (stub *transportStub) Create(_ context.Context, owner artifacts.ExecutionOwner, command artifacts.CreateCommand) (*artifacts.Artifact, error) {
	stub.createOwner = owner
	stub.createCommand = command
	return &artifacts.Artifact{ArtifactID: "artifact-1"}, stub.err
}

func (stub *transportStub) Upload(context.Context, artifacts.ExecutionOwner, artifacts.UploadCommand) (*artifacts.Artifact, error) {
	return &artifacts.Artifact{ArtifactID: "artifact-1"}, stub.err
}

func (stub *transportStub) Finalize(context.Context, artifacts.ExecutionOwner, artifacts.FinalizeCommand) (*artifacts.Artifact, error) {
	return &artifacts.Artifact{ArtifactID: "artifact-1"}, stub.err
}

func (stub *transportStub) Reference(context.Context, artifacts.ExecutionOwner, artifacts.ReferenceCommand) (artifacts.ReferenceResult, error) {
	return artifacts.ReferenceResult{
		Artifact:  &artifacts.Artifact{ArtifactID: "artifact-1"},
		Reference: &artifacts.ArtifactReference{ArtifactID: "artifact-1"},
	}, stub.err
}

func (stub *transportStub) Get(context.Context, artifacts.ExecutionOwner, artifacts.GetQuery) (*artifacts.Artifact, error) {
	return &artifacts.Artifact{ArtifactID: "artifact-1"}, stub.err
}

func (stub *transportStub) List(context.Context, artifacts.ExecutionOwner, artifacts.ListFilter) ([]*artifacts.Artifact, error) {
	return []*artifacts.Artifact{{ArtifactID: "artifact-1"}}, stub.err
}

func TestNewRejectsNilTransport(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, artifacts.ErrUnavailable) {
		t.Fatalf("New(nil) error = %v, want ErrUnavailable", err)
	}
}

func TestAdapterDelegatesEveryPort(t *testing.T) {
	adapter, err := New(&transportStub{})
	if err != nil {
		t.Fatal(err)
	}
	owner := artifacts.ExecutionOwner{}
	if value, err := adapter.Create(t.Context(), owner, artifacts.CreateCommand{}); err != nil || value.ArtifactID != "artifact-1" {
		t.Fatalf("Create = %#v, %v", value, err)
	}
	if value, err := adapter.Upload(t.Context(), owner, artifacts.UploadCommand{}); err != nil || value.ArtifactID != "artifact-1" {
		t.Fatalf("Upload = %#v, %v", value, err)
	}
	if value, err := adapter.Finalize(t.Context(), owner, artifacts.FinalizeCommand{}); err != nil || value.ArtifactID != "artifact-1" {
		t.Fatalf("Finalize = %#v, %v", value, err)
	}
	if value, err := adapter.Reference(t.Context(), owner, artifacts.ReferenceCommand{}); err != nil || value.Artifact == nil || value.Artifact.ArtifactID != "artifact-1" {
		t.Fatalf("Reference = %#v, %v", value, err)
	}
	if value, err := adapter.Get(t.Context(), owner, artifacts.GetQuery{}); err != nil || value.ArtifactID != "artifact-1" {
		t.Fatalf("Get = %#v, %v", value, err)
	}
	if values, err := adapter.List(t.Context(), owner, artifacts.ListFilter{}); err != nil || len(values) != 1 || values[0].ArtifactID != "artifact-1" {
		t.Fatalf("List = %#v, %v", values, err)
	}
}

func TestAdapterCreatePreservesCompleteSemanticEnvelope(t *testing.T) {
	transport := &transportStub{}
	adapter, err := New(transport)
	if err != nil {
		t.Fatal(err)
	}
	owner := artifacts.ExecutionOwner{
		WorkspaceKey: "WS", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 42,
	}
	command := artifacts.CreateCommand{
		ArtifactID: "artifact-1", SessionID: "session-1", TaskID: "TASK-1", Type: "patch",
		URI: "artifact://artifact-1", Summary: "task patch", MIMEType: "text/x-diff", SizeBytes: 42,
		Checksum: "sha256:checksum", ContentHash: "sha256:content",
		Visibility: "private", RedactionStatus: "unredacted", Metadata: map[string]string{"runner": "local"},
	}
	if _, err := adapter.Create(t.Context(), owner, command); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := transport.createCommand
	if transport.createOwner != owner || got.ArtifactID != command.ArtifactID || got.SessionID != command.SessionID ||
		got.TaskID != command.TaskID || got.Type != command.Type || got.URI != command.URI || got.Summary != command.Summary ||
		got.MIMEType != command.MIMEType || got.SizeBytes != command.SizeBytes || got.Checksum != command.Checksum ||
		got.ContentHash != command.ContentHash || got.Visibility != command.Visibility ||
		got.RedactionStatus != command.RedactionStatus || !maps.Equal(got.Metadata, command.Metadata) {
		t.Fatalf("create envelope = owner %#v command %#v, want owner %#v command %#v", transport.createOwner, got, owner, command)
	}
}

func TestAdapterMapsTransportErrors(t *testing.T) {
	tests := []struct {
		transport error
		want      error
	}{
		{ErrTransportNotFound, artifacts.ErrNotFound},
		{ErrTransportInvalid, artifacts.ErrInvalid},
		{ErrTransportConflict, artifacts.ErrAlreadyExists},
		{ErrTransportNotOwner, artifacts.ErrNotOwner},
		{ErrTransportInvalidTransition, artifacts.ErrInvalidTransition},
		{ErrTransportUnavailable, artifacts.ErrUnavailable},
		{errors.New("network"), artifacts.ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.transport.Error(), func(t *testing.T) {
			adapter, err := New(&transportStub{err: test.transport})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Create(t.Context(), artifacts.ExecutionOwner{}, artifacts.CreateCommand{})
			if !errors.Is(err, test.want) || !errors.Is(err, test.transport) {
				t.Fatalf("Create error = %v, want %v and original transport error", err, test.want)
			}
		})
	}
}
