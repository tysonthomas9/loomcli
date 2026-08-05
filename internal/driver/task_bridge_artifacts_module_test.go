package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type recordingBridgeArtifactsAPI struct {
	artifactsmodule.API
	references        []artifactsmodule.ReferenceCommand
	contentReferences []artifactsmodule.ReferenceCommand
}

func (api *recordingBridgeArtifactsAPI) Reference(
	ctx context.Context,
	auth authority.ExecutionAuthority,
	owner artifactsmodule.ExecutionOwner,
	command artifactsmodule.ReferenceCommand,
) (artifactsmodule.ReferenceResult, error) {
	api.references = append(api.references, command)
	return api.API.Reference(ctx, auth, owner, command)
}

func (api *recordingBridgeArtifactsAPI) CreateContent(
	ctx context.Context,
	auth artifactsmodule.ContentAuthorities,
	owner artifactsmodule.ExecutionOwner,
	command artifactsmodule.CreateCommand,
	content []byte,
	reference artifactsmodule.ReferenceCommand,
) (artifactsmodule.ContentResult, error) {
	api.contentReferences = append(api.contentReferences, reference)
	return api.API.CreateContent(ctx, auth, owner, command, content, reference)
}

func TestHostBridgeRejectsOversizedArtifactFilesBeforeReading(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oversized.out")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create oversized fixture: %v", err)
	}
	if err := file.Truncate(maxHostBridgeArtifactFileBytes + 1); err != nil {
		file.Close()
		t.Fatalf("truncate oversized fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close oversized fixture: %v", err)
	}

	executor := HostBridgeTaskExecutor{WorktreePath: dir}
	for _, test := range []struct {
		name string
		read func() ([]byte, error)
	}{
		{name: "runner output", read: func() ([]byte, error) {
			return executor.runnerFileOrInlineBytes(nil, filepath.Base(path), "logs")
		}},
		{name: "patch", read: func() ([]byte, error) {
			return executor.readPatch(context.Background(), bridgeTaskRunnerResult{PatchPath: filepath.Base(path)})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, err := test.read()
			if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "artifact limit") {
				t.Fatalf("read error = %v, want artifact limit domain.ErrInvalid", err)
			}
			if content != nil {
				t.Fatalf("oversized read returned %d bytes", len(content))
			}
		})
	}
}

func TestBridgeCreateContentArtifactUsesArtifactsOwnerLifecycle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	artifactsAPI := &recordingBridgeArtifactsAPI{API: testArtifactsAPI(st)}
	executor := HostBridgeTaskExecutor{Store: st, Artifacts: artifactsAPI, ArtifactAuthorities: taskWorkerTestAuthorities{}}
	req := hostBridgeTaskExecRequest()

	artifact, err := executor.createContentArtifact(
		ctx, req, "session-1", "logs-task-run-1", "logs", "runner logs", "text/plain", []byte("hello"),
	)
	if err != nil {
		t.Fatalf("createContentArtifact: %v", err)
	}
	if artifact.WorkspaceKey != req.WorkspaceKey || artifact.OwnerType != "task_run" || artifact.OwnerID != req.TaskRunID {
		t.Fatalf("artifact owner = %#v, want execution task run", artifact)
	}
	if artifact.DurableStatus != "finalized" || artifact.ContentHash == "" || artifact.FinalizedAt == nil {
		t.Fatalf("artifact lifecycle = %#v, want finalized content", artifact)
	}
	if len(artifactsAPI.contentReferences) != 1 || artifactsAPI.contentReferences[0] != (artifactsmodule.ReferenceCommand{
		ArtifactID: "logs-task-run-1", Kind: "task-output", TargetRef: "task-run://task-run-1/output",
	}) {
		t.Fatalf("CreateContent references = %#v", artifactsAPI.contentReferences)
	}

	persisted, err := st.Artifacts().Get(ctx, req.WorkspaceKey, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("get persisted artifact: %v", err)
	}
	if persisted.OwnerID != req.TaskRunID || persisted.SessionID != "session-1" {
		t.Fatalf("persisted ownership = %#v", persisted)
	}
}

func TestBridgeArtifactMutationFailsClosedWithoutCapability(t *testing.T) {
	st := memstore.New()
	executor := HostBridgeTaskExecutor{Store: st}
	req := hostBridgeTaskExecRequest()
	_, err := executor.createContentArtifact(
		context.Background(), req, "session-1", "logs-task-run-1", "logs", "runner logs", "text/plain", []byte("hello"),
	)
	if !errors.Is(err, artifactsmodule.ErrUnavailable) {
		t.Fatalf("createContentArtifact error = %v, want Artifacts unavailable", err)
	}
	if _, getErr := st.Artifacts().Get(context.Background(), req.WorkspaceKey, "logs-task-run-1"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("artifact persisted without capability, get error = %v", getErr)
	}
}

func TestBridgeCreateContentArtifactRejectsUnfencedOwnerBeforePersistence(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	artifactsAPI := &recordingBridgeArtifactsAPI{API: testArtifactsAPI(st)}
	executor := HostBridgeTaskExecutor{Store: st, Artifacts: artifactsAPI, ArtifactAuthorities: taskWorkerTestAuthorities{}}
	req := hostBridgeTaskExecRequest()
	req.FencingToken = 0

	_, err := executor.createContentArtifact(
		ctx, req, "session-1", "logs-task-run-1", "logs", "runner logs", "text/plain", []byte("hello"),
	)
	if !errors.Is(err, authority.ErrInvalidScope) {
		t.Fatalf("createContentArtifact error = %v, want authority.ErrInvalidScope", err)
	}
	if _, getErr := st.Artifacts().Get(ctx, req.WorkspaceKey, "logs-task-run-1"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("unfenced call persisted artifact, get error = %v", getErr)
	}
}

func TestBridgeRegisterRunnerArtifactFinalizesThroughArtifactsModule(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	artifactsAPI := &recordingBridgeArtifactsAPI{API: testArtifactsAPI(st)}
	executor := HostBridgeTaskExecutor{Store: st, Artifacts: artifactsAPI, ArtifactAuthorities: taskWorkerTestAuthorities{}}
	req := hostBridgeTaskExecRequest()

	artifact, err := executor.registerRunnerArtifact(ctx, req, bridgeArtifact{
		Type: "report", Summary: "runner report", MIMEType: "application/json",
		ContentHash: "sha256:report", Metadata: map[string]string{"source": "runner"},
	}, "report-task-run-1", "artifact://report-task-run-1")
	if err != nil {
		t.Fatalf("registerRunnerArtifact: %v", err)
	}
	if artifact.DurableStatus != "finalized" || artifact.OwnerID != req.TaskRunID || artifact.URI != "artifact://report-task-run-1" {
		t.Fatalf("artifact = %#v", artifact)
	}
	if artifact.Metadata["driver_run_id"] != req.DriverRunID || artifact.Metadata["source"] != "runner" {
		t.Fatalf("artifact metadata = %#v", artifact.Metadata)
	}
	if len(artifactsAPI.references) != 1 || artifactsAPI.references[0] != (artifactsmodule.ReferenceCommand{
		ArtifactID: "report-task-run-1", Kind: "task-output", TargetRef: "task-run://task-run-1/output",
	}) {
		t.Fatalf("runner references = %#v", artifactsAPI.references)
	}
}

func TestBridgeCreatePatchArtifactUsesArtifactsOwnerLifecycle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	executor := HostBridgeTaskExecutor{Store: st, Artifacts: testArtifactsAPI(st), ArtifactAuthorities: taskWorkerTestAuthorities{}}
	req := hostBridgeTaskExecRequest()

	artifact, baseRef, err := executor.createPatchArtifact(ctx, req, bridgeTaskRunnerResult{
		PatchBaseRef: "refs/heads/main",
		PatchSummary: "generated patch",
	}, []byte("diff --git a/a b/a\n"))
	if err != nil {
		t.Fatalf("createPatchArtifact: %v", err)
	}
	if baseRef != "refs/heads/main" {
		t.Fatalf("base ref = %q, want refs/heads/main", baseRef)
	}
	if artifact.ArtifactID != "patch-"+req.TaskRunID || artifact.Type != "patch" {
		t.Fatalf("artifact identity = %#v", artifact)
	}
	if artifact.OwnerType != "task_run" || artifact.OwnerID != req.TaskRunID || artifact.DurableStatus != "finalized" {
		t.Fatalf("artifact lifecycle = %#v", artifact)
	}
	if artifact.ContentHash == "" || artifact.FinalizedAt == nil || artifact.Metadata["patch_base_ref"] != baseRef {
		t.Fatalf("artifact content/metadata = %#v", artifact)
	}
}

func TestBridgeCreatePatchArtifactRejectsUnfencedOwnerBeforePersistence(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	executor := HostBridgeTaskExecutor{Store: st, Artifacts: testArtifactsAPI(st), ArtifactAuthorities: taskWorkerTestAuthorities{}}
	req := hostBridgeTaskExecRequest()
	req.FencingToken = 0

	_, _, err := executor.createPatchArtifact(ctx, req, bridgeTaskRunnerResult{}, []byte("patch"))
	if !errors.Is(err, authority.ErrInvalidScope) {
		t.Fatalf("createPatchArtifact error = %v, want authority.ErrInvalidScope", err)
	}
	if _, getErr := st.Artifacts().Get(ctx, req.WorkspaceKey, "patch-"+req.TaskRunID); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("unfenced call persisted artifact, get error = %v", getErr)
	}
}
