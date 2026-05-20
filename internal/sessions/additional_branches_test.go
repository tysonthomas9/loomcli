package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestQueryFiltersDedupesAndReadsArtifacts(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	newer := now.Add(-time.Hour)
	records := []SessionRecord{
		{SessionID: "s1", TaskID: "T-1", EpicID: "E-1", AgentName: "nova", Backend: "codex", Status: StatusRunning, StartedAt: old},
		{SessionID: "s2", TaskID: "T-2", EpicID: "E-2", AgentName: "spark", Backend: "claude", Status: StatusCompleted, StartedAt: newer},
		{SessionID: "s1", TaskID: "T-1", EpicID: "E-1", AgentName: "nova", Backend: "codex", Status: StatusCompleted, StartedAt: newer},
	}
	if err := writeRecordsAtomic(filepath.Join(store.Dir(), "index.jsonl"), records); err != nil {
		t.Fatalf("write index: %v", err)
	}

	got, err := store.Query(Filter{TaskID: "T-1", EpicID: "E-1", AgentName: "nova", Backend: "codex", Status: StatusCompleted})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "s1" || got[0].Status != StatusCompleted {
		t.Fatalf("Query returned %#v, want deduped completed s1", got)
	}
	if matched := matchesSessionFilter(records[0], Filter{Since: now}); matched {
		t.Fatal("matchesSessionFilter matched record before Since")
	}
	if matched := matchesSessionFilter(records[1], Filter{Until: old}); matched {
		t.Fatal("matchesSessionFilter matched record after Until")
	}

	sessDir := filepath.Join(store.Dir(), "s1")
	if err := os.MkdirAll(sessDir, sessDirPerm); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	meta := SessionMetadata{SessionRecord: records[2]}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), metaBytes, sessFilePerm); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "prompt.txt"), []byte("prompt"), sessFilePerm); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "diff.patch"), []byte("diff"), sessFilePerm); err != nil {
		t.Fatalf("write diff: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "transcript.jsonl"), []byte(`{"seq":2,"role":"assistant"}`+"\n{bad}\n"+`{"seq":1,"role":"user"}`+"\n"), sessFilePerm); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	if meta, err := store.LoadMetadata("s1"); err != nil || meta.SessionID != "s1" {
		t.Fatalf("LoadMetadata = %+v, %v", meta, err)
	}
	if prompt, err := store.ReadPrompt("s1"); err != nil || prompt != "prompt" {
		t.Fatalf("ReadPrompt = %q, %v", prompt, err)
	}
	if diff, err := store.ReadDiff("s1"); err != nil || diff != "diff" {
		t.Fatalf("ReadDiff = %q, %v", diff, err)
	}
	entries, err := store.LoadTranscript("s1")
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(entries) != 2 || entries[0].Seq != 1 || entries[1].Seq != 2 {
		t.Fatalf("LoadTranscript sorted entries = %#v", entries)
	}
	if byTask, err := store.SessionsByTask("T-1"); err != nil || len(byTask) != 1 {
		t.Fatalf("SessionsByTask = %#v, %v", byTask, err)
	}
}

func TestNativeTranscriptEventsUsageAndNotifyBranches(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	const sid = "20260520-120000-codex-abcd-0123abcd"
	store, sessDir := newStoreWithSession(t, sid)

	badMeta := filepath.Join(sessDir, "metadata.json")
	if err := os.WriteFile(badMeta, []byte("{bad json"), sessFilePerm); err != nil {
		t.Fatalf("write bad metadata: %v", err)
	}
	if _, err := store.LoadNativeEvents(sid); err == nil || !strings.Contains(err.Error(), "load metadata") {
		t.Fatalf("LoadNativeEvents bad metadata err = %v", err)
	}
	if err := os.WriteFile(badMeta, []byte(`{"session_id":"`+sid+`","backend":"opencode"}`), sessFilePerm); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	srcDir := filepath.Join(t.TempDir(), "src-dir")
	if err := os.Mkdir(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src-dir: %v", err)
	}
	if err := store.SyncNativeTranscript(sid, srcDir); err == nil || !strings.Contains(err.Error(), "read source transcript") {
		t.Fatalf("SyncNativeTranscript directory err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, NativeTranscriptFile), []byte("{bad json}\n"), sessFilePerm); err != nil {
		t.Fatalf("write bad native transcript: %v", err)
	}
	if _, err := store.LoadNativeEvents(sid); err == nil {
		t.Fatal("LoadNativeEvents malformed native transcript returned nil error")
	}

	missingUsage, err := SumTranscriptUsage(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err != nil || missingUsage != (TokenUsage{}) {
		t.Fatalf("SumTranscriptUsage missing = %+v, %v", missingUsage, err)
	}
	usagePath := filepath.Join(t.TempDir(), "claude.jsonl")
	usageJSON := strings.Join([]string{
		"",
		"{bad json}",
		`{"type":"user","message":{"id":"u1"}}`,
		`{"type":"assistant","message":{"id":"","usage":{"input_tokens":99}}}`,
		`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}`,
		`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":5,"output_tokens":6,"cache_read_input_tokens":7,"cache_creation_input_tokens":8}}}`,
	}, "\n")
	if err := os.WriteFile(usagePath, []byte(usageJSON), sessFilePerm); err != nil {
		t.Fatalf("write usage: %v", err)
	}
	usage, err := SumTranscriptUsage(usagePath)
	if err != nil {
		t.Fatalf("SumTranscriptUsage: %v", err)
	}
	want := TokenUsage{InputTokens: 5, OutputTokens: 6, CacheReadTokens: 7, CacheWriteTokens: 8}
	if usage != want {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}

	NotifyWebUI(context.Background(), "", "T-1", sid, StatusCompleted, "")
	NotifyWebUI(context.Background(), "://bad-url", "T-1", sid, StatusCompleted, "")
	badStatus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer badStatus.Close()
	NotifyWebUI(context.Background(), badStatus.URL, "T-1", sid, StatusFailed, "token")
}

func TestCodexRolloutFallbackBranches(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got, err := store.SyncLatestCodexRollout("missing", t.TempDir(), time.Now().Add(-time.Hour)); err != nil || got != "" {
		t.Fatalf("SyncLatestCodexRollout empty root = %q, %v", got, err)
	}
	if roots := codexSessionWalkRoots("", time.Now(), time.Now()); roots != nil {
		t.Fatalf("empty codexSessionWalkRoots = %#v", roots)
	}

	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(filepath.Join(root, "2026", "05", "20"), 0755); err != nil {
		t.Fatalf("mkdir dated root: %v", err)
	}
	futureSince := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if roots := codexSessionWalkRoots(root, futureSince, futureSince.Add(-time.Hour)); roots != nil {
		t.Fatalf("future since roots = %#v", roots)
	}
	if hasCodexDateLayout(filepath.Join(root, "missing")) {
		t.Fatal("missing root should not have Codex date layout")
	}
	flatRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(flatRoot, "abcd"), 0755); err != nil {
		t.Fatalf("mkdir non-date dir: %v", err)
	}
	if hasCodexDateLayout(flatRoot) {
		t.Fatal("non-date root should not have Codex date layout")
	}

	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte("{bad json}\n{\"type\":\"other\"}\n"), sessFilePerm); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	if codexRolloutMatchesWorkDir(rollout, t.TempDir()) {
		t.Fatal("malformed/non-session rollout matched workdir")
	}
}

func TestPurgeOlderThanSkipsAndDeletesSessions(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Now().UTC()
	oldEnd := now.Add(-48 * time.Hour)
	recentEnd := now.Add(-time.Hour)
	records := []SessionRecord{
		{SessionID: "running", Status: StatusRunning, StartedAt: recentEnd},
		{SessionID: "no-ended", Status: StatusFailed, StartedAt: oldEnd},
		{SessionID: "recent", Status: StatusCompleted, StartedAt: recentEnd, EndedAt: &recentEnd},
		{SessionID: "old", Status: StatusCompleted, StartedAt: oldEnd, EndedAt: &oldEnd},
	}
	if err := writeRecordsAtomic(filepath.Join(store.Dir(), "index.jsonl"), records); err != nil {
		t.Fatalf("write index: %v", err)
	}
	for _, id := range []string{"running", "no-ended", "recent", "old"} {
		if err := os.MkdirAll(filepath.Join(store.Dir(), id), sessDirPerm); err != nil {
			t.Fatalf("mkdir %s: %v", id, err)
		}
	}
	purged, err := store.PurgeOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1", purged)
	}
	for _, id := range []string{"running", "no-ended", "recent"} {
		if _, err := os.Stat(filepath.Join(store.Dir(), id)); err != nil {
			t.Fatalf("%s should remain: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), "old")); !os.IsNotExist(err) {
		t.Fatalf("old session still exists err=%v", err)
	}
}

func TestReadDedupedIndexMissingAndFilterHelpers(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got, err := store.readDedupedIndex(Filter{})
	if err != nil {
		t.Fatalf("readDedupedIndex missing: %v", err)
	}
	if !reflect.DeepEqual(got, []SessionRecord(nil)) {
		t.Fatalf("missing index records = %#v", got)
	}
}
