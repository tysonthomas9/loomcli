package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const transcriptRefBackfillCheckName = "transcript_ref_backfill"

type transcriptRefBackfillStoreOpener func(context.Context) (*bootstrap.StoreHandle, error)

var openTranscriptRefBackfillStore transcriptRefBackfillStoreOpener = cmdstore.OpenStore

type transcriptRefBackfillCandidate struct {
	session        *domain.AgentSession
	transcriptPath string
	sizeBytes      int64
}

func checkTranscriptRefBackfill() CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handle, err := openTranscriptRefBackfillStore(ctx)
	if err != nil || handle == nil || handle.Store == nil {
		return skippedTranscriptRefBackfill("control-plane store unavailable")
	}
	defer func() { _ = handle.Close() }()

	workspaceKey, err := cmdstore.ActiveWorkspace(ctx, handle.Store)
	if err != nil || workspaceKey == "" {
		return skippedTranscriptRefBackfill("active workspace unavailable")
	}
	return checkTranscriptRefBackfillWithStore(ctx, handle.Store, workspaceKey, doctorFix)
}

func checkTranscriptRefBackfillWithStore(ctx context.Context, st store.Store, workspaceKey string, fix bool) CheckResult {
	if st == nil || workspaceKey == "" {
		return skippedTranscriptRefBackfill("control-plane store unavailable")
	}
	candidates, err := scanTranscriptRefBackfillCandidates(ctx, st, workspaceKey)
	if err != nil {
		return CheckResult{
			Name:    transcriptRefBackfillCheckName,
			Status:  StatusWarn,
			Summary: "could not scan task sessions for missing transcript_ref",
			Detail:  err.Error(),
		}
	}
	if len(candidates) == 0 {
		return CheckResult{
			Name:    transcriptRefBackfillCheckName,
			Status:  StatusPass,
			Summary: "all terminal task sessions with local transcripts have transcript_ref",
		}
	}
	if fix {
		return fixTranscriptRefBackfill(ctx, st, workspaceKey, candidates)
	}
	return CheckResult{
		Name:    transcriptRefBackfillCheckName,
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d terminal task session(s) missing transcript_ref with local transcripts", len(candidates)),
		Detail:  strings.Join(formatTranscriptRefBackfillCandidates(candidates), "\n") + "\nRun: loom doctor --fix to upload transcripts and stamp transcript_ref",
	}
}

func skippedTranscriptRefBackfill(reason string) CheckResult {
	return CheckResult{
		Name:    transcriptRefBackfillCheckName,
		Status:  StatusPass,
		Summary: "transcript_ref backfill skipped: " + reason,
	}
}

func scanTranscriptRefBackfillCandidates(ctx context.Context, st store.Store, workspaceKey string) ([]transcriptRefBackfillCandidate, error) {
	records, err := st.AgentSessions().List(ctx, workspaceKey, store.AgentSessionFilter{Kind: domain.AgentSessionKindTask})
	if err != nil {
		return nil, fmt.Errorf("list task sessions: %w", err)
	}
	stores := doctorSessionStoresForWorkspace(ctx, st, workspaceKey)
	var candidates []transcriptRefBackfillCandidate
	for _, rec := range records {
		if rec == nil || !needsTranscriptRefBackfill(rec) {
			continue
		}
		path, size, ok := localNativeTranscript(stores, rec.SessionID)
		if !ok {
			continue
		}
		candidates = append(candidates, transcriptRefBackfillCandidate{
			session:        rec,
			transcriptPath: path,
			sizeBytes:      size,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].session.SessionID < candidates[j].session.SessionID
	})
	return candidates, nil
}

func needsTranscriptRefBackfill(rec *domain.AgentSession) bool {
	if rec.Kind != domain.AgentSessionKindTask || !terminalAgentSessionStatus(rec.Status) {
		return false
	}
	if rec.Metadata != nil && strings.TrimSpace(rec.Metadata["transcript_ref"]) != "" {
		return false
	}
	return true
}

func terminalAgentSessionStatus(status domain.AgentSessionStatus) bool {
	switch status {
	case domain.AgentSessionCompleted, domain.AgentSessionFailed, domain.AgentSessionCancelled, domain.AgentSessionExpired:
		return true
	default:
		return false
	}
}

func doctorSessionStoresForWorkspace(ctx context.Context, st store.Store, workspaceKey string) []*sessions.Store {
	var stores []*sessions.Store
	seen := make(map[string]struct{})
	addStore := func(runtimeDir string) {
		runtimeDir = strings.TrimSpace(runtimeDir)
		if runtimeDir == "" {
			return
		}
		key := filepath.Clean(runtimeDir)
		if abs, err := filepath.Abs(runtimeDir); err == nil {
			key = filepath.Clean(abs)
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		if sessStore, err := sessions.NewStore(runtimeDir); err == nil {
			stores = append(stores, sessStore)
		}
	}

	addStore(cli.GetWorkspaceRuntimeDir())
	if st == nil || workspaceKey == "" {
		return stores
	}
	wsData, err := storeadapter.BuildWorkspaceDataForKey(ctx, st, workspaceKey)
	if err != nil || wsData == nil {
		return stores
	}
	addStore(wsData.Path)
	for _, repo := range wsData.Repos {
		addStore(repo.Path)
	}
	return stores
}

func localNativeTranscript(stores []*sessions.Store, sessionID string) (string, int64, bool) {
	for _, sessStore := range stores {
		if sessStore == nil {
			continue
		}
		if _, err := sessStore.LoadMetadata(sessionID); err != nil {
			continue
		}
		path := sessStore.NativeTranscriptPath(sessionID)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() <= 0 {
			continue
		}
		return path, info.Size(), true
	}
	return "", 0, false
}

func formatTranscriptRefBackfillCandidates(candidates []transcriptRefBackfillCandidate) []string {
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		rec := candidate.session
		lines = append(lines, fmt.Sprintf("session=%s task=%s agent=%s bytes=%d", rec.SessionID, rec.TaskID, rec.AgentID, candidate.sizeBytes))
	}
	return lines
}

func fixTranscriptRefBackfill(ctx context.Context, st store.Store, workspaceKey string, candidates []transcriptRefBackfillCandidate) CheckResult {
	fixed, failed := 0, 0
	details := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ref, err := backfillTranscriptRef(ctx, st, workspaceKey, candidate)
		if err != nil {
			failed++
			details = append(details, fmt.Sprintf("failed: %s: %v", candidate.session.SessionID, err))
			continue
		}
		fixed++
		details = append(details, fmt.Sprintf("backfilled: %s -> %s", candidate.session.SessionID, ref))
	}
	status := StatusPass
	if failed > 0 {
		status = StatusWarn
	}
	return CheckResult{
		Name:    transcriptRefBackfillCheckName,
		Status:  status,
		Summary: fmt.Sprintf("backfilled %d transcript_ref(s), %d failed", fixed, failed),
		Detail:  strings.Join(details, "\n"),
	}
}

func backfillTranscriptRef(ctx context.Context, st store.Store, workspaceKey string, candidate transcriptRefBackfillCandidate) (string, error) {
	rec := candidate.session
	data, err := os.ReadFile(candidate.transcriptPath) //nolint:gosec // resolved from the owning sessions.Store.
	if err != nil {
		return "", fmt.Errorf("read transcript: %w", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("transcript is empty")
	}
	artifactID := "transcript-" + rec.SessionID
	metadata := map[string]string{"runtime": "doctor-backfill"}
	if rec.Metadata != nil {
		if backend := strings.TrimSpace(rec.Metadata["backend"]); backend != "" {
			metadata["backend"] = backend
		}
	}
	finalized, err := store.UploadContentArtifact(ctx, st.Artifacts(), store.ArtifactCreate{
		WorkspaceKey:  workspaceKey,
		ArtifactID:    artifactID,
		SessionID:     rec.SessionID,
		TaskID:        rec.TaskID,
		OwnerType:     "session",
		OwnerID:       rec.SessionID,
		Type:          "transcript",
		Summary:       "agent session transcript",
		MIMEType:      "application/x-ndjson",
		DurableStatus: "declared",
		Metadata:      metadata,
	}, data)
	if err != nil {
		return "", fmt.Errorf("upload transcript artifact: %w", err)
	}
	current, err := st.AgentSessions().Get(ctx, workspaceKey, rec.SessionID)
	if err != nil {
		return "", fmt.Errorf("reload session: %w", err)
	}
	nextMetadata := cloneDoctorStringMap(current.Metadata)
	nextMetadata["transcript_ref"] = "artifact://" + finalized.ArtifactID
	if _, err := st.AgentSessions().Update(ctx, workspaceKey, rec.SessionID, store.AgentSessionUpdate{Metadata: &nextMetadata}); err != nil {
		return "", fmt.Errorf("stamp transcript_ref: %w", err)
	}
	return nextMetadata["transcript_ref"], nil
}

func cloneDoctorStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
