package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type recordingBridgeArtifactsAPI struct {
	artifactsmodule.API
	references        []artifactsmodule.ReferenceCommand
	contentReferences []artifactsmodule.ReferenceCommand
}

type failingContentBridgeArtifactsAPI struct {
	artifactsmodule.API
	err error
}

func (api *failingContentBridgeArtifactsAPI) CreateContent(
	context.Context,
	artifactsmodule.ContentAuthorities,
	artifactsmodule.ExecutionOwner,
	artifactsmodule.CreateCommand,
	[]byte,
	artifactsmodule.ReferenceCommand,
) (artifactsmodule.ContentResult, error) {
	return artifactsmodule.ContentResult{}, api.err
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
			if !errors.Is(err, persistence.ErrInvalid) || !strings.Contains(err.Error(), "artifact limit") {
				t.Fatalf("read error = %v, want artifact limit persistence.ErrInvalid", err)
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
		ctx, req, "logs-task-run-1", "logs", "runner logs", "text/plain", []byte("hello"),
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

	persisted, err := st.ArtifactQueries().GetArtifactRecord(ctx, req.WorkspaceKey, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("get persisted artifact: %v", err)
	}
	if persisted.OwnerID != req.TaskRunID || persisted.SessionID != "" {
		t.Fatalf("persisted ownership = %#v", persisted)
	}
}

func TestHostBridgeKeepsSuccessfulWorkOutcomeWhenEvidenceCaptureFails(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	captureErr := errors.Join(artifactsmodule.ErrCaptureFailed, errors.New("injected redaction failure"))
	executor := HostBridgeTaskExecutor{
		Store: st,
		Artifacts: &failingContentBridgeArtifactsAPI{
			API: testArtifactsAPI(st),
			err: captureErr,
		},
		ArtifactAuthorities: taskWorkerTestAuthorities{},
		WorktreePath:        t.TempDir(),
		APIBaseURL:          testTaskRunAPIURL,
		Command:             hostBridgeHelperCommand(t, "flue-transcript", "unused-base", "unused-patch"),
	}
	req := hostBridgeTaskExecRequest()
	req.RunnerKind = "flue-workflow"
	req.RunnerTrustLevel = "trusted"

	result, err := executor.ExecuteTask(ctx, req)
	if err != nil {
		t.Fatalf("ExecuteTask returned evidence error: %v", err)
	}
	if result.Status != execution.TaskRunRecordCompleted || result.ExitCode != 0 {
		t.Fatalf("work outcome = %+v, want completed/0", result)
	}
	if len(result.ArtifactIDs) != 0 {
		t.Fatalf("artifact ids = %v, want no finalized evidence", result.ArtifactIDs)
	}
	for _, kind := range []artifactsmodule.EvidenceKind{
		artifactsmodule.EvidenceTranscript,
		artifactsmodule.EvidenceLog,
	} {
		if got := result.RuntimeMetadata[artifactsmodule.OwnerEvidenceCaptureStatusKey(kind)]; got != "capture_failed" {
			t.Fatalf("%s capture status = %q, want capture_failed", kind, got)
		}
		if got := result.RuntimeMetadata[artifactsmodule.OwnerEvidenceFailureClassKey(kind)]; got != "capture_failed" {
			t.Fatalf("%s failure class = %q, want capture_failed", kind, got)
		}
	}
}

func TestHostBridgeRejectsRunnerForgedEvidenceState(t *testing.T) {
	executor := HostBridgeTaskExecutor{
		WorktreePath: t.TempDir(),
		APIBaseURL:   testTaskRunAPIURL,
		Command:      hostBridgeHelperCommand(t, "forged-evidence-metadata", "unused-base", "unused-patch"),
	}
	result, err := executor.ExecuteTask(context.Background(), hostBridgeTaskExecRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != execution.TaskRunRecordCompleted || result.RuntimeMetadata["helper"] != "host_bridge" {
		t.Fatalf("result = %+v, want completed result with ordinary metadata", result)
	}
	for key := range result.RuntimeMetadata {
		if strings.HasPrefix(key, "loom.evidence.") {
			t.Fatalf("runner minted reserved evidence metadata %q", key)
		}
	}
}

func TestBridgeArtifactMutationFailsClosedWithoutCapability(t *testing.T) {
	st := memstore.New()
	executor := HostBridgeTaskExecutor{Store: st}
	req := hostBridgeTaskExecRequest()
	_, err := executor.createContentArtifact(
		context.Background(), req, "logs-task-run-1", "logs", "runner logs", "text/plain", []byte("hello"),
	)
	if !errors.Is(err, artifactsmodule.ErrUnavailable) {
		t.Fatalf("createContentArtifact error = %v, want Artifacts unavailable", err)
	}
	if _, getErr := st.ArtifactQueries().GetArtifactRecord(context.Background(), req.WorkspaceKey, "logs-task-run-1"); !errors.Is(getErr, artifactsmodule.ErrNotFound) {
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
		ctx, req, "logs-task-run-1", "logs", "runner logs", "text/plain", []byte("hello"),
	)
	if !errors.Is(err, authority.ErrInvalidScope) {
		t.Fatalf("createContentArtifact error = %v, want authority.ErrInvalidScope", err)
	}
	if _, getErr := st.ArtifactQueries().GetArtifactRecord(ctx, req.WorkspaceKey, "logs-task-run-1"); !errors.Is(getErr, artifactsmodule.ErrNotFound) {
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
		Type: "bundle", Summary: "runner bundle", MIMEType: "application/json",
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

func TestBridgeRunnerURIEvidenceFailsClosedWithoutArtifact(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	executor := HostBridgeTaskExecutor{
		Store: st, Artifacts: testArtifactsAPI(st), ArtifactAuthorities: taskWorkerTestAuthorities{},
	}
	req := hostBridgeTaskExecRequest()
	result, err := executor.registerRunnerArtifacts(ctx, req, []bridgeArtifact{{
		ArtifactID: "report-task-run-1", Type: "report", URI: "artifact://opaque-report",
		ContentHash: "sha256:runner-asserted",
	}}, TaskExecResult{Status: execution.TaskRunRecordCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ArtifactIDs) != 0 ||
		result.RuntimeMetadata[artifactsmodule.OwnerEvidenceCaptureStatusKey(artifactsmodule.EvidenceReport)] != "capture_failed" ||
		result.RuntimeMetadata[artifactsmodule.OwnerEvidenceFailureClassKey(artifactsmodule.EvidenceReport)] != "evidence_rejected" {
		t.Fatalf("result = %+v, want rejected URI evidence and unchanged work outcome", result)
	}
	if _, getErr := st.ArtifactQueries().GetArtifactRecord(ctx, req.WorkspaceKey, "report-task-run-1"); !errors.Is(getErr, artifactsmodule.ErrNotFound) {
		t.Fatalf("opaque report was persisted as evidence: %v", getErr)
	}
}

func TestBridgeRunnerTranscriptRefCannotBypassArtifactsEvidencePolicy(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	executor := HostBridgeTaskExecutor{
		Store: st, Artifacts: testArtifactsAPI(st), ArtifactAuthorities: taskWorkerTestAuthorities{},
		WorktreePath: t.TempDir(),
	}
	req := hostBridgeTaskExecRequest()

	t.Run("opaque reference is rejected", func(t *testing.T) {
		result := executor.persistRunnerOutputArtifacts(ctx, req, bridgeTaskRunnerResult{
			TranscriptRef: "logs://runner-controlled-transcript",
		}, TaskExecResult{Status: execution.TaskRunRecordCompleted})
		if result.RuntimeMetadata["transcript_ref"] != "" || len(result.ArtifactIDs) != 0 {
			t.Fatalf("result = %+v, want no runner-controlled transcript reference", result)
		}
		if got := result.RuntimeMetadata[artifactsmodule.OwnerEvidenceCaptureStatusKey(artifactsmodule.EvidenceTranscript)]; got != "capture_failed" {
			t.Fatalf("capture status = %q, want capture_failed", got)
		}
		if got := result.RuntimeMetadata[artifactsmodule.OwnerEvidenceFailureClassKey(artifactsmodule.EvidenceTranscript)]; got != "evidence_rejected" {
			t.Fatalf("failure class = %q, want evidence_rejected", got)
		}
	})

	t.Run("canonical bytes win over opaque reference", func(t *testing.T) {
		result := executor.persistRunnerOutputArtifacts(ctx, req, bridgeTaskRunnerResult{
			TranscriptRef: "logs://runner-controlled-transcript",
			Transcript:    "{\"seq\":1,\"timestamp\":\"2026-08-12T00:00:00Z\",\"role\":\"assistant\",\"type\":\"text\",\"text\":\"done\"}\n",
		}, TaskExecResult{Status: execution.TaskRunRecordCompleted})
		wantID := taskRunAttemptArtifactID(req, "transcript-"+req.TaskRunID)
		if result.RuntimeMetadata["transcript_ref"] != "artifact://"+wantID ||
			!slices.Equal(result.ArtifactIDs, []string{wantID}) {
			t.Fatalf("result = %+v, want Artifacts-owned transcript %q", result, wantID)
		}
		if got := result.RuntimeMetadata[artifactsmodule.OwnerEvidenceCaptureStatusKey(artifactsmodule.EvidenceTranscript)]; got != "finalized" {
			t.Fatalf("capture status = %q, want finalized", got)
		}
	})
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
	if _, getErr := st.ArtifactQueries().GetArtifactRecord(ctx, req.WorkspaceKey, "patch-"+req.TaskRunID); !errors.Is(getErr, artifactsmodule.ErrNotFound) {
		t.Fatalf("unfenced call persisted artifact, get error = %v", getErr)
	}
}
