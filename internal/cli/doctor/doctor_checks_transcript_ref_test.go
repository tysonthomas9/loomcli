package doctor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestCheckTranscriptRefBackfillDetectsEligibleSessions(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	runtimeDir := t.TempDir()
	repoDir := t.TempDir()
	setupTranscriptRefBackfillWorkspace(t, ctx, st, runtimeDir, map[string]string{"repo": repoDir})

	eligible := stageTranscriptRefBackfillLocalSession(t, repoDir, "worker-a", true)
	stageTranscriptRefBackfillAgentSession(t, ctx, st, "TEST", eligible, domain.AgentSessionKindTask, domain.AgentSessionCompleted, nil)

	nonTask := stageTranscriptRefBackfillLocalSession(t, runtimeDir, "lead", true)
	stageTranscriptRefBackfillAgentSession(t, ctx, st, "TEST", nonTask, domain.AgentSessionKindOrchestration, domain.AgentSessionCompleted, nil)

	running := stageTranscriptRefBackfillLocalSession(t, runtimeDir, "worker-running", true)
	stageTranscriptRefBackfillAgentSession(t, ctx, st, "TEST", running, domain.AgentSessionKindTask, domain.AgentSessionRunning, nil)

	withRef := stageTranscriptRefBackfillLocalSession(t, runtimeDir, "worker-ref", true)
	stageTranscriptRefBackfillAgentSession(t, ctx, st, "TEST", withRef, domain.AgentSessionKindTask, domain.AgentSessionCompleted, map[string]string{"transcript_ref": "artifact://existing"})

	noDiskBytes := stageTranscriptRefBackfillLocalSession(t, runtimeDir, "worker-empty", false)
	stageTranscriptRefBackfillAgentSession(t, ctx, st, "TEST", noDiskBytes, domain.AgentSessionKindTask, domain.AgentSessionCompleted, nil)

	candidates, err := scanTranscriptRefBackfillCandidates(ctx, st, "TEST")
	if err != nil {
		t.Fatalf("scanTranscriptRefBackfillCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].session.SessionID != eligible {
		t.Fatalf("candidates = %+v, want only %s", candidates, eligible)
	}
	if !strings.HasPrefix(candidates[0].transcriptPath, repoDir) {
		t.Fatalf("transcript path = %q, want repo session store under %q", candidates[0].transcriptPath, repoDir)
	}

	res := checkTranscriptRefBackfillWithStore(ctx, st, "TEST", false)
	if res.Status != StatusWarn {
		t.Fatalf("status = %v, summary=%q detail=%q", res.Status, res.Summary, res.Detail)
	}
	if !strings.Contains(res.Summary, "1 terminal task session(s) missing transcript_ref") {
		t.Fatalf("summary = %q", res.Summary)
	}
	if !strings.Contains(res.Detail, "session="+eligible) || !strings.Contains(res.Detail, "loom doctor --fix") {
		t.Fatalf("detail = %q, want session and --fix remedy", res.Detail)
	}
}

func TestCheckTranscriptRefBackfillFixUploadsAndStamps(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	runtimeDir := t.TempDir()
	setupTranscriptRefBackfillWorkspace(t, ctx, st, runtimeDir, nil)

	sessionID := stageTranscriptRefBackfillLocalSession(t, runtimeDir, "worker-a", true)
	stageTranscriptRefBackfillAgentSession(t, ctx, st, "TEST", sessionID, domain.AgentSessionKindTask, domain.AgentSessionCompleted, map[string]string{
		"backend": "codex",
		"keep":    "yes",
	})

	res := checkTranscriptRefBackfillWithStore(ctx, st, "TEST", true)
	if res.Status != StatusPass {
		t.Fatalf("status = %v, summary=%q detail=%q", res.Status, res.Summary, res.Detail)
	}
	if res.Summary != "backfilled 1 transcript_ref(s), 0 failed" {
		t.Fatalf("summary = %q", res.Summary)
	}
	updated, err := st.AgentSessions().Get(ctx, "TEST", sessionID)
	if err != nil {
		t.Fatalf("get updated session: %v", err)
	}
	wantRef := "artifact://transcript-" + sessionID
	if updated.Metadata["transcript_ref"] != wantRef || updated.Metadata["keep"] != "yes" {
		t.Fatalf("metadata = %+v, want transcript_ref %q and preserved keys", updated.Metadata, wantRef)
	}
	artifact, err := st.Artifacts().Get(ctx, "TEST", "transcript-"+sessionID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.OwnerType != "session" || artifact.OwnerID != sessionID || artifact.SessionID != sessionID ||
		artifact.TaskID != "TASK-"+sessionID || artifact.Type != "transcript" || artifact.MIMEType != "application/x-ndjson" ||
		artifact.DurableStatus != "finalized" || artifact.ContentHash == "" || artifact.FinalizedAt == nil {
		t.Fatalf("artifact = %+v, want finalized session-owned transcript", artifact)
	}
	if artifact.Metadata["runtime"] != "doctor-backfill" || artifact.Metadata["backend"] != "codex" {
		t.Fatalf("artifact metadata = %+v, want doctor-backfill/codex", artifact.Metadata)
	}
	reader, ok := st.Artifacts().(store.ArtifactContentReader)
	if !ok {
		t.Fatal("memstore artifact store missing content reader")
	}
	content, err := reader.ReadContent(ctx, "TEST", artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read artifact content: %v", err)
	}
	if !strings.Contains(string(content), `"hello"`) {
		t.Fatalf("content = %q, want staged transcript bytes", content)
	}
}

func TestCheckTranscriptRefBackfillFixContinuesAfterUploadFailure(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	runtimeDir := t.TempDir()
	setupTranscriptRefBackfillWorkspace(t, ctx, st, runtimeDir, nil)

	failingSession := stageTranscriptRefBackfillLocalSession(t, runtimeDir, "worker-fail", true)
	stageTranscriptRefBackfillAgentSession(t, ctx, st, "TEST", failingSession, domain.AgentSessionKindTask, domain.AgentSessionCompleted, nil)
	okSession := stageTranscriptRefBackfillLocalSession(t, runtimeDir, "worker-ok", true)
	stageTranscriptRefBackfillAgentSession(t, ctx, st, "TEST", okSession, domain.AgentSessionKindTask, domain.AgentSessionCompleted, nil)

	wrapped := artifactFailureStore{
		Store: st,
		artifacts: failUploadArtifactStore{
			ArtifactStore:  st.Artifacts(),
			failArtifactID: "transcript-" + failingSession,
		},
	}
	res := checkTranscriptRefBackfillWithStore(ctx, wrapped, "TEST", true)
	if res.Status != StatusWarn {
		t.Fatalf("status = %v, summary=%q detail=%q", res.Status, res.Summary, res.Detail)
	}
	if res.Summary != "backfilled 1 transcript_ref(s), 1 failed" {
		t.Fatalf("summary = %q", res.Summary)
	}
	failed, err := st.AgentSessions().Get(ctx, "TEST", failingSession)
	if err != nil {
		t.Fatalf("get failed session: %v", err)
	}
	if failed.Metadata["transcript_ref"] != "" {
		t.Fatalf("failed session metadata = %+v, want no transcript_ref", failed.Metadata)
	}
	succeeded, err := st.AgentSessions().Get(ctx, "TEST", okSession)
	if err != nil {
		t.Fatalf("get succeeded session: %v", err)
	}
	if succeeded.Metadata["transcript_ref"] != "artifact://transcript-"+okSession {
		t.Fatalf("succeeded metadata = %+v, want transcript_ref", succeeded.Metadata)
	}
	if !strings.Contains(res.Detail, "failed: "+failingSession) || !strings.Contains(res.Detail, "backfilled: "+okSession) {
		t.Fatalf("detail = %q, want failed and backfilled outcomes", res.Detail)
	}
}

func TestCheckTranscriptRefBackfillUnavailableStorePasses(t *testing.T) {
	res := checkTranscriptRefBackfillWithStore(context.Background(), nil, "TEST", false)
	if res.Status != StatusPass || !strings.Contains(res.Summary, "control-plane store unavailable") {
		t.Fatalf("result = %+v, want pass/no-op for unavailable store", res)
	}
}

func setupTranscriptRefBackfillWorkspace(t *testing.T, ctx context.Context, st store.Store, runtimeDir string, repoPaths map[string]string) {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_WORKSPACE", "TEST")
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "Test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	stateRepos := make(map[string]string, len(repoPaths))
	for name, path := range repoPaths {
		if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "TEST", Name: name, DefaultBranch: "main"}); err != nil {
			t.Fatalf("create repo: %v", err)
		}
		stateRepos[name] = path
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		if sc.Workspaces == nil {
			sc.Workspaces = make(map[string]bootstrap.WorkspaceLocalState)
		}
		sc.Workspaces["TEST"] = bootstrap.WorkspaceLocalState{Path: runtimeDir, Repos: stateRepos}
		return nil
	}); err != nil {
		t.Fatalf("mutate state cache: %v", err)
	}
}

func stageTranscriptRefBackfillLocalSession(t *testing.T, runtimeDir, agent string, withTranscript bool) string {
	t.Helper()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: agent,
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if withTranscript {
		data := []byte(`{"seq":1,"role":"assistant","type":"text","text":"hello"}` + "\n")
		if err := os.WriteFile(sessStore.NativeTranscriptPath(sess.SessionID()), data, 0o600); err != nil {
			t.Fatalf("write transcript: %v", err)
		}
	}
	return sess.SessionID()
}

func stageTranscriptRefBackfillAgentSession(t *testing.T, ctx context.Context, st store.Store, ws, sessionID string, kind domain.AgentSessionKind, status domain.AgentSessionStatus, metadata map[string]string) {
	t.Helper()
	if metadata == nil {
		metadata = map[string]string{}
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: ws,
		SessionID:    sessionID,
		AgentID:      "agent-" + sessionID,
		Kind:         kind,
		TaskID:       "TASK-" + sessionID,
		Status:       status,
		Metadata:     metadata,
	}); err != nil {
		t.Fatalf("create agent session: %v", err)
	}
}

type artifactFailureStore struct {
	store.Store
	artifacts store.ArtifactStore
}

func (s artifactFailureStore) Artifacts() store.ArtifactStore {
	return s.artifacts
}

type failUploadArtifactStore struct {
	store.ArtifactStore
	failArtifactID string
}

func (s failUploadArtifactStore) UploadContent(ctx context.Context, workspaceKey, artifactID string, upload store.ArtifactContentUpload) (*domain.Artifact, error) {
	if artifactID == s.failArtifactID {
		return nil, errors.New("forced upload failure")
	}
	return s.ArtifactStore.UploadContent(ctx, workspaceKey, artifactID, upload)
}
