package artifacts

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type fakeStore struct {
	create    func(context.Context, ExecutionOwner, CreateCommand) (*Artifact, error)
	upload    func(context.Context, ExecutionOwner, UploadCommand) (*Artifact, error)
	finalize  func(context.Context, ExecutionOwner, FinalizeCommand) (*Artifact, error)
	reference func(context.Context, ExecutionOwner, ReferenceCommand) (ReferenceResult, error)
	get       func(context.Context, ExecutionOwner, GetQuery) (*Artifact, error)
	list      func(context.Context, ExecutionOwner, ListFilter) ([]*Artifact, error)
}

func (f *fakeStore) Create(ctx context.Context, owner ExecutionOwner, command CreateCommand) (*Artifact, error) {
	return f.create(ctx, owner, command)
}

func (f *fakeStore) Upload(ctx context.Context, owner ExecutionOwner, command UploadCommand) (*Artifact, error) {
	return f.upload(ctx, owner, command)
}

func (f *fakeStore) Finalize(ctx context.Context, owner ExecutionOwner, command FinalizeCommand) (*Artifact, error) {
	return f.finalize(ctx, owner, command)
}

func (f *fakeStore) Reference(ctx context.Context, owner ExecutionOwner, command ReferenceCommand) (ReferenceResult, error) {
	return f.reference(ctx, owner, command)
}

func (f *fakeStore) Get(ctx context.Context, owner ExecutionOwner, query GetQuery) (*Artifact, error) {
	return f.get(ctx, owner, query)
}

func (f *fakeStore) List(ctx context.Context, owner ExecutionOwner, filter ListFilter) ([]*Artifact, error) {
	return f.list(ctx, owner, filter)
}

type serviceFixture struct {
	service   *Service
	issuer    *authority.Issuer
	owner     ExecutionOwner
	expiresAt time.Time
}

func newServiceFixture(t *testing.T, store Store, owner ExecutionOwner) serviceFixture {
	t.Helper()
	issuer := authority.NewIssuer()
	return newServiceFixtureWithIssuer(t, store, owner, issuer, time.Now().Add(time.Hour))
}

func newServiceFixtureWithIssuer(
	t *testing.T,
	store Store,
	owner ExecutionOwner,
	issuer *authority.Issuer,
	expiresAt time.Time,
) serviceFixture {
	t.Helper()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	service, err := New(store, admission)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return serviceFixture{service: service, issuer: issuer, owner: owner, expiresAt: expiresAt}
}

func (fixture serviceFixture) auth(t *testing.T, action authority.Action) authority.ExecutionAuthority {
	t.Helper()
	return issueTestAuthority(t, fixture.issuer, fixture.owner.WorkspaceKey, action, authorityOwner(fixture.owner), fixture.expiresAt)
}

func (fixture serviceFixture) contentAuthorities(t *testing.T) ContentAuthorities {
	t.Helper()
	return ContentAuthorities{
		Declare:   fixture.auth(t, ActionDeclare),
		Get:       fixture.auth(t, ActionGet),
		Upload:    fixture.auth(t, ActionUpload),
		Finalize:  fixture.auth(t, ActionFinalize),
		Reference: fixture.auth(t, ActionReference),
	}
}

func TestServiceLifecycleCarriesExactExecutionOwnerAndDefensiveValues(t *testing.T) {
	owner := validOwner()
	createdAt := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	metadata := map[string]string{"runner": "local"}
	content := []byte("patch")
	store := &fakeStore{}
	store.create = func(_ context.Context, got ExecutionOwner, command CreateCommand) (*Artifact, error) {
		if got != owner {
			t.Fatalf("create owner = %#v, want %#v", got, owner)
		}
		if command.SessionID != "session-1" || command.TaskID != "TASK-1" ||
			command.URI != "artifact://artifact-1" || command.SizeBytes != 5 ||
			command.Checksum != "sha256:checksum" || command.ContentHash != "sha256:content" {
			t.Fatalf("create command lost semantic fields: %#v", command)
		}
		command.Metadata["runner"] = "mutated"
		return &Artifact{
			WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID, SessionID: command.SessionID, TaskID: command.TaskID,
			OwnerType: OwnerTaskRun, OwnerID: owner.TaskRunID, Type: command.Type, URI: command.URI,
			Summary: command.Summary, MIMEType: command.MIMEType, SizeBytes: command.SizeBytes,
			Checksum: command.Checksum, ContentHash: command.ContentHash,
			Visibility: command.Visibility, RedactionStatus: command.RedactionStatus,
			DurableStatus: StatusDeclared, Metadata: map[string]string{"runner": "local"}, CreatedAt: createdAt, UpdatedAt: createdAt,
		}, nil
	}
	store.upload = func(_ context.Context, got ExecutionOwner, command UploadCommand) (*Artifact, error) {
		if got != owner || !slices.Equal(command.Content, content) {
			t.Fatalf("upload owner/content = %#v/%q", got, command.Content)
		}
		command.Content[0] = 'X'
		return &Artifact{
			WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID, OwnerType: OwnerTaskRun, OwnerID: owner.TaskRunID,
			Type: "patch", MIMEType: command.MIMEType, SizeBytes: int64(len(content)), DurableStatus: StatusUploading,
			ContentHash: artifactContentHash(content), CreatedAt: createdAt, UpdatedAt: createdAt,
		}, nil
	}
	store.finalize = func(_ context.Context, got ExecutionOwner, command FinalizeCommand) (*Artifact, error) {
		if got != owner || command.ContentHash == nil || *command.ContentHash != artifactContentHash(content) {
			t.Fatalf("finalize owner/command = %#v/%#v", got, command)
		}
		value := finalizedArtifact(owner, command.ArtifactID, "patch", createdAt)
		value.ContentHash = *command.ContentHash
		return value, nil
	}
	store.reference = func(_ context.Context, got ExecutionOwner, command ReferenceCommand) (ReferenceResult, error) {
		if got != owner || command.Kind != "task-output" || command.TargetRef != "task-run://task-run-1/output" {
			t.Fatalf("reference owner/command = %#v/%#v", got, command)
		}
		return validReferenceResult(owner, command, "patch", createdAt), nil
	}
	store.get = func(_ context.Context, got ExecutionOwner, query GetQuery) (*Artifact, error) {
		if got != owner || query.ArtifactID != "artifact-1" {
			t.Fatalf("get owner/query = %#v/%#v", got, query)
		}
		return finalizedArtifact(owner, query.ArtifactID, "patch", createdAt), nil
	}
	store.list = func(_ context.Context, got ExecutionOwner, filter ListFilter) ([]*Artifact, error) {
		if got != owner || filter.Type != "patch" || filter.DurableStatus != StatusFinalized {
			t.Fatalf("list owner/filter = %#v/%#v", got, filter)
		}
		return []*Artifact{finalizedArtifact(owner, "artifact-1", "patch", createdAt)}, nil
	}

	fixture := newServiceFixture(t, store, owner)
	created, err := fixture.service.Create(t.Context(), fixture.auth(t, ActionDeclare), owner, CreateCommand{
		ArtifactID: "artifact-1", SessionID: "session-1", TaskID: "TASK-1", Type: "patch",
		URI: "artifact://artifact-1", Summary: "task patch", MIMEType: "text/x-diff", SizeBytes: 5,
		Checksum: "sha256:checksum", ContentHash: "sha256:content",
		Visibility: "private", RedactionStatus: "unredacted", Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if metadata["runner"] != "local" || created.Metadata["runner"] != "local" {
		t.Fatalf("create metadata was not defensively copied: input=%v result=%v", metadata, created.Metadata)
	}

	uploaded, err := fixture.service.Upload(t.Context(), fixture.auth(t, ActionUpload), owner, UploadCommand{
		ArtifactID: "artifact-1", Content: content,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if string(content) != "patch" || uploaded.ContentHash != artifactContentHash(content) {
		t.Fatalf("upload copies/content hash = %q/%q", content, uploaded.ContentHash)
	}

	hash := uploaded.ContentHash
	finalized, err := fixture.service.Finalize(t.Context(), fixture.auth(t, ActionFinalize), owner, FinalizeCommand{
		ArtifactID: "artifact-1", ContentHash: &hash,
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if finalized.DurableStatus != StatusFinalized || finalized.FinalizedAt == nil {
		t.Fatalf("finalized = %#v", finalized)
	}

	referenced, err := fixture.service.Reference(t.Context(), fixture.auth(t, ActionReference), owner, ReferenceCommand{
		ArtifactID: "artifact-1", Kind: "task-output", TargetRef: "task-run://task-run-1/output",
	})
	if err != nil {
		t.Fatalf("Reference: %v", err)
	}
	if referenced.Reference == nil || referenced.Reference.ArtifactID != "artifact-1" {
		t.Fatalf("reference = %#v", referenced)
	}

	got, err := fixture.service.Get(t.Context(), fixture.auth(t, ActionGet), owner, GetQuery{ArtifactID: "artifact-1"})
	if err != nil || got.ArtifactID != "artifact-1" {
		t.Fatalf("Get = %#v, %v", got, err)
	}
	values, err := fixture.service.List(t.Context(), fixture.auth(t, ActionList), owner, ListFilter{
		Type: "patch", DurableStatus: StatusFinalized,
	})
	if err != nil || len(values) != 1 || values[0].ArtifactID != "artifact-1" {
		t.Fatalf("List = %#v, %v", values, err)
	}
}

func TestServiceRejectsMalformedExecutionOwnerBeforePort(t *testing.T) {
	called := false
	store := &fakeStore{create: func(context.Context, ExecutionOwner, CreateCommand) (*Artifact, error) {
		called = true
		return nil, nil
	}}
	fixture := newServiceFixture(t, store, validOwner())
	tests := []struct {
		name string
		edit func(*ExecutionOwner)
	}{
		{name: "workspace", edit: func(v *ExecutionOwner) { v.WorkspaceKey = "" }},
		{name: "task run", edit: func(v *ExecutionOwner) { v.TaskRunID = " task-run-1" }},
		{name: "node", edit: func(v *ExecutionOwner) { v.NodeID = "" }},
		{name: "lease", edit: func(v *ExecutionOwner) { v.LeaseID = "" }},
		{name: "token", edit: func(v *ExecutionOwner) { v.LeaseToken = " " }},
		{name: "fence", edit: func(v *ExecutionOwner) { v.FencingToken = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owner := validOwner()
			test.edit(&owner)
			_, err := fixture.service.Create(t.Context(), fixture.auth(t, ActionDeclare), owner, CreateCommand{ArtifactID: "artifact-1", Type: "patch"})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Create error = %v, want ErrInvalid", err)
			}
		})
	}
	if called {
		t.Fatal("durable port called for malformed execution owner")
	}
}

func TestServiceAuthorityIsIssuerActionWorkspaceAndOwnerBound(t *testing.T) {
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		workspace      string
		action         authority.Action
		authorityOwner authority.ExecutionOwner
		zero           bool
		foreignIssuer  bool
		expired        bool
		want           error
	}{
		{name: "zero", zero: true, want: authority.ErrAdmissionDenied},
		{name: "foreign issuer", foreignIssuer: true, want: authority.ErrAdmissionDenied},
		{name: "expired", expired: true, want: authority.ErrAdmissionDenied},
		{name: "wrong action", action: ActionGet, want: authority.ErrAdmissionDenied},
		{name: "wrong workspace", workspace: "OTHER", want: authority.ErrAdmissionDenied},
		{name: "wrong resource kind", authorityOwner: testAuthorityOwner(authority.ExecutionResourceDriverRun, "task-run-1", "node-1", "lease-1", 42), want: ErrNotOwner},
		{name: "wrong task run id", authorityOwner: testAuthorityOwner(authority.ExecutionResourceTaskRun, "task-run-2", "node-1", "lease-1", 42), want: ErrNotOwner},
		{name: "wrong node", authorityOwner: testAuthorityOwner(authority.ExecutionResourceTaskRun, "task-run-1", "node-2", "lease-1", 42), want: ErrNotOwner},
		{name: "wrong lease", authorityOwner: testAuthorityOwner(authority.ExecutionResourceTaskRun, "task-run-1", "node-1", "lease-2", 42), want: ErrNotOwner},
		{name: "wrong fence", authorityOwner: testAuthorityOwner(authority.ExecutionResourceTaskRun, "task-run-1", "node-1", "lease-1", 43), want: ErrNotOwner},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := base
			issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			called := false
			store := &fakeStore{create: func(context.Context, ExecutionOwner, CreateCommand) (*Artifact, error) {
				called = true
				return nil, nil
			}}
			fixture := newServiceFixtureWithIssuer(t, store, validOwner(), issuer, base.Add(time.Minute))
			var auth authority.ExecutionAuthority
			if !test.zero {
				authIssuer := issuer
				if test.foreignIssuer {
					authIssuer, err = authority.NewIssuerWithClock(func() time.Time { return now })
					if err != nil {
						t.Fatal(err)
					}
				}
				workspace := test.workspace
				if workspace == "" {
					workspace = validOwner().WorkspaceKey
				}
				action := test.action
				if action == "" {
					action = ActionDeclare
				}
				authOwner := test.authorityOwner
				if authOwner.ResourceKind == "" {
					authOwner = authorityOwner(validOwner())
				}
				auth = issueTestAuthority(t, authIssuer, workspace, action, authOwner, base.Add(time.Minute))
			}
			if test.expired {
				now = base.Add(2 * time.Minute)
			}
			_, err = fixture.service.Create(t.Context(), auth, validOwner(), CreateCommand{ArtifactID: "artifact-1", Type: "patch"})
			if !errors.Is(err, test.want) {
				t.Fatalf("Create error = %v, want %v", err, test.want)
			}
			if called {
				t.Fatal("unauthorized call reached durable port")
			}
		})
	}
}

func TestServiceRejectsPersistedOwnerEscape(t *testing.T) {
	owner := validOwner()
	store := &fakeStore{get: func(context.Context, ExecutionOwner, GetQuery) (*Artifact, error) {
		return &Artifact{
			WorkspaceKey: owner.WorkspaceKey, ArtifactID: "artifact-1", OwnerType: OwnerTaskRun,
			OwnerID: "foreign-task-run", Type: "patch", DurableStatus: StatusDeclared,
		}, nil
	}}
	fixture := newServiceFixture(t, store, owner)
	_, err := fixture.service.Get(t.Context(), fixture.auth(t, ActionGet), owner, GetQuery{ArtifactID: "artifact-1"})
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("Get error = %v, want ErrInvalidPersistedState", err)
	}
}

func TestCreateRejectsPersistedSemanticFieldLoss(t *testing.T) {
	owner := validOwner()
	command := CreateCommand{
		ArtifactID: "artifact-1", SessionID: "session-1", TaskID: "TASK-1", Type: "patch",
		URI: "artifact://artifact-1", SizeBytes: 42, Checksum: "sha256:checksum", ContentHash: "sha256:content",
	}
	tests := []struct {
		name string
		drop func(*Artifact)
	}{
		{name: "session id", drop: func(value *Artifact) { value.SessionID = "" }},
		{name: "task id", drop: func(value *Artifact) { value.TaskID = "" }},
		{name: "uri", drop: func(value *Artifact) { value.URI = "" }},
		{name: "size", drop: func(value *Artifact) { value.SizeBytes = 0 }},
		{name: "checksum", drop: func(value *Artifact) { value.Checksum = "" }},
		{name: "content hash", drop: func(value *Artifact) { value.ContentHash = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{create: func(_ context.Context, _ ExecutionOwner, got CreateCommand) (*Artifact, error) {
				value := &Artifact{
					WorkspaceKey: owner.WorkspaceKey, ArtifactID: got.ArtifactID, SessionID: got.SessionID, TaskID: got.TaskID,
					OwnerType: OwnerTaskRun, OwnerID: owner.TaskRunID, Type: got.Type, URI: got.URI,
					SizeBytes: got.SizeBytes, Checksum: got.Checksum, ContentHash: got.ContentHash, DurableStatus: StatusDeclared,
				}
				test.drop(value)
				return value, nil
			}}
			fixture := newServiceFixture(t, store, owner)
			if _, err := fixture.service.Create(t.Context(), fixture.auth(t, ActionDeclare), owner, command); !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("Create error = %v, want ErrInvalidPersistedState", err)
			}
		})
	}
}

func TestUploadRejectsDivergentContentProjection(t *testing.T) {
	owner := validOwner()
	content := []byte("hello")
	tests := []struct {
		name   string
		mutate func(*Artifact)
	}{
		{name: "status", mutate: func(value *Artifact) { value.DurableStatus = StatusDeclared }},
		{name: "size", mutate: func(value *Artifact) { value.SizeBytes-- }},
		{name: "digest", mutate: func(value *Artifact) { value.ContentHash = "sha256:different" }},
		{name: "mime type", mutate: func(value *Artifact) { value.MIMEType = "application/octet-stream" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{upload: func(_ context.Context, _ ExecutionOwner, command UploadCommand) (*Artifact, error) {
				value := &Artifact{
					WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID, OwnerType: OwnerTaskRun,
					OwnerID: owner.TaskRunID, Type: "patch", MIMEType: command.MIMEType,
					SizeBytes: int64(len(command.Content)), ContentHash: artifactContentHash(command.Content), DurableStatus: StatusUploading,
				}
				test.mutate(value)
				return value, nil
			}}
			fixture := newServiceFixture(t, store, owner)
			_, err := fixture.service.Upload(t.Context(), fixture.auth(t, ActionUpload), owner, UploadCommand{
				ArtifactID: "artifact-1", Content: content, MIMEType: "text/plain",
			})
			if !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("Upload error = %v, want ErrInvalidPersistedState", err)
			}
		})
	}
}

func TestFinalizeRejectsDroppedSuppliedFields(t *testing.T) {
	owner := validOwner()
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	uri, summary, mimeType := "artifact://artifact-1", "task patch", "text/x-diff"
	sizeBytes := int64(42)
	checksum, contentHash := "sha256:checksum", "sha256:content"
	visibility, redactionStatus := "private", "unredacted"
	metadata := map[string]string{"runner": "local"}
	command := FinalizeCommand{
		ArtifactID: "artifact-1", URI: &uri, Summary: &summary, MIMEType: &mimeType, SizeBytes: &sizeBytes,
		Checksum: &checksum, ContentHash: &contentHash, Visibility: &visibility,
		RedactionStatus: &redactionStatus, Metadata: &metadata,
	}
	tests := []struct {
		name string
		drop func(*Artifact)
	}{
		{name: "uri", drop: func(value *Artifact) { value.URI = "" }},
		{name: "summary", drop: func(value *Artifact) { value.Summary = "" }},
		{name: "mime type", drop: func(value *Artifact) { value.MIMEType = "" }},
		{name: "size", drop: func(value *Artifact) { value.SizeBytes = 0 }},
		{name: "checksum", drop: func(value *Artifact) { value.Checksum = "" }},
		{name: "content hash", drop: func(value *Artifact) { value.ContentHash = "" }},
		{name: "visibility", drop: func(value *Artifact) { value.Visibility = "" }},
		{name: "redaction status", drop: func(value *Artifact) { value.RedactionStatus = "" }},
		{name: "metadata", drop: func(value *Artifact) { value.Metadata = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{finalize: func(_ context.Context, _ ExecutionOwner, got FinalizeCommand) (*Artifact, error) {
				value := &Artifact{
					WorkspaceKey: owner.WorkspaceKey, ArtifactID: got.ArtifactID, OwnerType: OwnerTaskRun, OwnerID: owner.TaskRunID,
					Type: "patch", URI: *got.URI, Summary: *got.Summary, MIMEType: *got.MIMEType, SizeBytes: *got.SizeBytes,
					Checksum: *got.Checksum, ContentHash: *got.ContentHash, Visibility: *got.Visibility,
					RedactionStatus: *got.RedactionStatus, Metadata: cloneMetadata(*got.Metadata),
					DurableStatus: StatusFinalized, FinalizedAt: &now,
				}
				test.drop(value)
				return value, nil
			}}
			fixture := newServiceFixture(t, store, owner)
			if _, err := fixture.service.Finalize(t.Context(), fixture.auth(t, ActionFinalize), owner, command); !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("Finalize error = %v, want ErrInvalidPersistedState", err)
			}
		})
	}
}

func TestReferenceRejectsDivergentImmutableResult(t *testing.T) {
	owner := validOwner()
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	command := ReferenceCommand{ArtifactID: "artifact-1", Kind: "task-output", TargetRef: "task-run://task-run-1/output"}
	tests := []struct {
		name   string
		mutate func(*ReferenceResult)
	}{
		{name: "missing reference", mutate: func(value *ReferenceResult) { value.Reference = nil }},
		{name: "non-finalized artifact", mutate: func(value *ReferenceResult) { value.Artifact.DurableStatus = StatusUploading }},
		{name: "wrong artifact", mutate: func(value *ReferenceResult) { value.Reference.ArtifactID = "artifact-2" }},
		{name: "wrong owner", mutate: func(value *ReferenceResult) { value.Reference.OwnerID = "task-run-2" }},
		{name: "wrong kind", mutate: func(value *ReferenceResult) { value.Reference.Kind = "different" }},
		{name: "wrong target", mutate: func(value *ReferenceResult) { value.Reference.TargetRef = "task-run://other/output" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{reference: func(context.Context, ExecutionOwner, ReferenceCommand) (ReferenceResult, error) {
				value := validReferenceResult(owner, command, "patch", now)
				test.mutate(&value)
				return value, nil
			}}
			fixture := newServiceFixture(t, store, owner)
			_, err := fixture.service.Reference(t.Context(), fixture.auth(t, ActionReference), owner, command)
			if !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("Reference error = %v, want ErrInvalidPersistedState", err)
			}
		})
	}
}

func TestCreateContentReusesMatchingFinalizedArtifactAndCommitsReference(t *testing.T) {
	owner := validOwner()
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	content := []byte("content")
	uploadCalls, finalizeCalls, referenceCalls := 0, 0, 0
	command := CreateCommand{ArtifactID: "transcript-task-run-1", SessionID: "session-1", Type: "transcript", MIMEType: "application/x-ndjson"}
	reference := ReferenceCommand{ArtifactID: command.ArtifactID, Kind: "task-output", TargetRef: "task-run://task-run-1/output"}
	store := &fakeStore{
		create: func(context.Context, ExecutionOwner, CreateCommand) (*Artifact, error) { return nil, ErrAlreadyExists },
		get: func(context.Context, ExecutionOwner, GetQuery) (*Artifact, error) {
			artifact := finalizedArtifact(owner, command.ArtifactID, command.Type, now)
			artifact.SessionID = command.SessionID
			artifact.MIMEType = command.MIMEType
			artifact.ContentHash = artifactContentHash(content)
			return artifact, nil
		},
		reference: func(context.Context, ExecutionOwner, ReferenceCommand) (ReferenceResult, error) {
			referenceCalls++
			return validReferenceResult(owner, reference, command.Type, now), nil
		},
		upload: func(context.Context, ExecutionOwner, UploadCommand) (*Artifact, error) {
			uploadCalls++
			return nil, nil
		},
		finalize: func(context.Context, ExecutionOwner, FinalizeCommand) (*Artifact, error) {
			finalizeCalls++
			return nil, nil
		},
	}
	fixture := newServiceFixture(t, store, owner)
	result, err := fixture.service.CreateContent(t.Context(), fixture.contentAuthorities(t), owner, command, content, reference)
	if err != nil {
		t.Fatalf("CreateContent: %v", err)
	}
	if result.Artifact == nil || result.Artifact.DurableStatus != StatusFinalized || result.Reference == nil ||
		uploadCalls != 0 || finalizeCalls != 0 || referenceCalls != 1 {
		t.Fatalf("reuse result/calls = %#v upload=%d finalize=%d reference=%d", result, uploadCalls, finalizeCalls, referenceCalls)
	}
}

func TestCreateContentRejectsIncompleteAuthorityBundleBeforePort(t *testing.T) {
	owner := validOwner()
	tests := []struct {
		name string
		zero func(*ContentAuthorities)
	}{
		{name: "declare", zero: func(value *ContentAuthorities) { value.Declare = authority.ExecutionAuthority{} }},
		{name: "get", zero: func(value *ContentAuthorities) { value.Get = authority.ExecutionAuthority{} }},
		{name: "upload", zero: func(value *ContentAuthorities) { value.Upload = authority.ExecutionAuthority{} }},
		{name: "finalize", zero: func(value *ContentAuthorities) { value.Finalize = authority.ExecutionAuthority{} }},
		{name: "reference", zero: func(value *ContentAuthorities) { value.Reference = authority.ExecutionAuthority{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			store := &fakeStore{create: func(context.Context, ExecutionOwner, CreateCommand) (*Artifact, error) {
				called = true
				return nil, nil
			}}
			fixture := newServiceFixture(t, store, owner)
			auth := fixture.contentAuthorities(t)
			test.zero(&auth)
			_, err := fixture.service.CreateContent(t.Context(), auth, owner, CreateCommand{
				ArtifactID: "artifact-1", Type: "logs",
			}, []byte("logs"), ReferenceCommand{
				ArtifactID: "artifact-1", Kind: "task-output", TargetRef: "task-run://task-run-1/output",
			})
			if !errors.Is(err, authority.ErrAdmissionDenied) {
				t.Fatalf("CreateContent error = %v, want ErrAdmissionDenied", err)
			}
			if called {
				t.Fatal("incomplete authority bundle reached durable port")
			}
		})
	}
}

func TestCreateContentUploadsFinalizesAndReferencesDeclaredArtifact(t *testing.T) {
	owner := validOwner()
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	command := CreateCommand{ArtifactID: "logs-task-run-1", Type: "logs", MIMEType: "text/plain"}
	reference := ReferenceCommand{ArtifactID: command.ArtifactID, Kind: "task-output", TargetRef: "task-run://task-run-1/output"}
	store := &fakeStore{}
	store.create = func(context.Context, ExecutionOwner, CreateCommand) (*Artifact, error) {
		return &Artifact{
			WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID, OwnerType: OwnerTaskRun,
			OwnerID: owner.TaskRunID, Type: command.Type, MIMEType: command.MIMEType, DurableStatus: StatusDeclared,
		}, nil
	}
	store.upload = func(_ context.Context, _ ExecutionOwner, upload UploadCommand) (*Artifact, error) {
		if string(upload.Content) != "logs" {
			t.Fatalf("upload content = %q", upload.Content)
		}
		return &Artifact{
			WorkspaceKey: owner.WorkspaceKey, ArtifactID: upload.ArtifactID, OwnerType: OwnerTaskRun,
			OwnerID: owner.TaskRunID, Type: command.Type, MIMEType: upload.MIMEType, SizeBytes: int64(len(upload.Content)),
			DurableStatus: StatusUploading, ContentHash: artifactContentHash(upload.Content),
		}, nil
	}
	store.finalize = func(_ context.Context, _ ExecutionOwner, finalize FinalizeCommand) (*Artifact, error) {
		if finalize.ContentHash == nil || *finalize.ContentHash != artifactContentHash([]byte("logs")) {
			t.Fatalf("finalize hash = %#v", finalize.ContentHash)
		}
		value := finalizedArtifact(owner, finalize.ArtifactID, command.Type, now)
		value.ContentHash = *finalize.ContentHash
		return value, nil
	}
	store.reference = func(_ context.Context, _ ExecutionOwner, got ReferenceCommand) (ReferenceResult, error) {
		if got != reference {
			t.Fatalf("reference = %#v, want %#v", got, reference)
		}
		value := validReferenceResult(owner, got, command.Type, now)
		value.Artifact.ContentHash = artifactContentHash([]byte("logs"))
		return value, nil
	}
	fixture := newServiceFixture(t, store, owner)
	result, err := fixture.service.CreateContent(t.Context(), fixture.contentAuthorities(t), owner, command, []byte("logs"), reference)
	if err != nil {
		t.Fatalf("CreateContent: %v", err)
	}
	if result.Artifact == nil || result.Artifact.ContentHash != artifactContentHash([]byte("logs")) ||
		result.Artifact.DurableStatus != StatusFinalized || result.Reference == nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestCreateContentRejectsDifferentContentForFinalizedArtifact(t *testing.T) {
	owner := validOwner()
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	command := CreateCommand{ArtifactID: "patch-task-run-1", Type: "patch", MIMEType: "text/x-diff"}
	referenceCalls := 0
	store := &fakeStore{
		create: func(context.Context, ExecutionOwner, CreateCommand) (*Artifact, error) { return nil, ErrAlreadyExists },
		get: func(context.Context, ExecutionOwner, GetQuery) (*Artifact, error) {
			artifact := finalizedArtifact(owner, command.ArtifactID, command.Type, now)
			artifact.MIMEType = command.MIMEType
			artifact.ContentHash = artifactContentHash([]byte("original"))
			return artifact, nil
		},
		reference: func(context.Context, ExecutionOwner, ReferenceCommand) (ReferenceResult, error) {
			referenceCalls++
			return ReferenceResult{}, nil
		},
	}
	fixture := newServiceFixture(t, store, owner)
	result, err := fixture.service.CreateContent(t.Context(), fixture.contentAuthorities(t), owner, command, []byte("different"), ReferenceCommand{
		ArtifactID: command.ArtifactID, Kind: "task-output", TargetRef: "task-run://task-run-1/output",
	})
	if !errors.Is(err, ErrAlreadyExists) || result.Artifact != nil || referenceCalls != 0 {
		t.Fatalf("CreateContent = %#v, %v, reference calls=%d", result, err, referenceCalls)
	}
}

func TestNewFailsClosedWithoutDurablePortOrAdmission(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil, admission); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New(nil, admission) = %v, want ErrUnavailable", err)
	}
	if _, err := New(&fakeStore{}, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New(store, nil) = %v, want ErrUnavailable", err)
	}
}

func issueTestAuthority(
	t *testing.T,
	issuer *authority.Issuer,
	workspace string,
	action authority.Action,
	owner authority.ExecutionOwner,
	expiresAt time.Time,
) authority.ExecutionAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "task-run:" + owner.ResourceID, Class: authority.ClassExecution,
		Workspace: workspace, Actions: []authority.Action{action}, ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	value, err := issuer.IssueExecutionForOwner(principal, workspace, action, owner)
	if err != nil {
		t.Fatalf("IssueExecutionForOwner: %v", err)
	}
	return value
}

func authorityOwner(owner ExecutionOwner) authority.ExecutionOwner {
	return testAuthorityOwner(
		authority.ExecutionResourceTaskRun,
		owner.TaskRunID,
		owner.NodeID,
		owner.LeaseID,
		owner.FencingToken,
	)
}

func testAuthorityOwner(kind authority.ExecutionResourceKind, resourceID, nodeID, leaseID string, fence int64) authority.ExecutionOwner {
	return authority.ExecutionOwner{
		ResourceKind: kind, ResourceID: resourceID, NodeID: nodeID, LeaseID: leaseID, FencingToken: fence,
	}
}

func validOwner() ExecutionOwner {
	return ExecutionOwner{
		WorkspaceKey: "WS", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 42,
	}
}

func finalizedArtifact(owner ExecutionOwner, artifactID, artifactType string, now time.Time) *Artifact {
	return &Artifact{
		WorkspaceKey: owner.WorkspaceKey, ArtifactID: artifactID, SessionID: "session-1",
		OwnerType: OwnerTaskRun, OwnerID: owner.TaskRunID, Type: artifactType,
		ContentHash: "sha256:" + artifactType, DurableStatus: StatusFinalized,
		Metadata: map[string]string{"kind": artifactType}, FinalizedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
}

func validReferenceResult(owner ExecutionOwner, command ReferenceCommand, artifactType string, now time.Time) ReferenceResult {
	return ReferenceResult{
		Artifact: finalizedArtifact(owner, command.ArtifactID, artifactType, now),
		Reference: &ArtifactReference{
			WorkspaceKey: owner.WorkspaceKey, ReferenceID: "reference-1", ArtifactID: command.ArtifactID,
			OwnerType: OwnerTaskRun, OwnerID: owner.TaskRunID, Kind: command.Kind, TargetRef: command.TargetRef, CreatedAt: now,
		},
	}
}
