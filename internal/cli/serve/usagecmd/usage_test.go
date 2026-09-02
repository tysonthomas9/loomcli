package usagecmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSessionsIndex writes a <dir>/sessions/index.jsonl fixture.
func writeSessionsIndex(t *testing.T, dir string, lines ...map[string]any) {
	t.Helper()
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	var sb strings.Builder
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(sessDir, "index.jsonl"), []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("write index.jsonl: %v", err)
	}
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeSessionsIndex(t, dir,
		// s1 appears twice — running, then finalized. Deduping is what keeps
		// the served totals from doubling.
		map[string]any{
			"session_id": "s1", "agent_name": "nova", "backend": "claude",
			"status": "running", "started_at": "2026-08-28T10:00:00Z",
			"input_tokens": 1,
		},
		map[string]any{
			"session_id": "s1", "agent_name": "nova", "backend": "claude",
			"task_id": "T-1", "status": "completed",
			"started_at": "2026-08-28T10:00:00Z", "ended_at": "2026-08-28T10:30:00Z",
			"duration_s":   1800,
			"input_tokens": 1000, "output_tokens": 200,
			"cache_read_tokens": 30, "cache_write_tokens": 40,
		},
		map[string]any{
			"session_id": "s2", "agent_name": "orion", "backend": "codex",
			"task_id": "T-2", "status": "failed", "exit_code": 1,
			"started_at": "2026-08-29T09:00:00Z", "ended_at": "2026-08-29T09:00:05Z",
			"duration_s":   5,
			"input_tokens": 0, "output_tokens": 0,
		},
	)
	return dir
}

func getUsage(t *testing.T, reader Reader, query string) (*httptest.ResponseRecorder, Response) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/usage"+query, nil)
	rr := httptest.NewRecorder()
	HandleUsage(reader)(rr, req)

	var resp Response
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v\nbody: %s", err, rr.Body.String())
		}
	}
	return rr, resp
}

func TestHandleUsage_SessionsReader(t *testing.T) {
	reader := InitSessionsReader(fixtureDir(t))
	if reader == nil {
		t.Fatal("InitSessionsReader returned nil for a readable directory")
	}

	rr, resp := getUsage(t, reader, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if resp.SessionCount != 2 {
		t.Errorf("session count = %d, want 2 (3 index lines, 2 sessions)", resp.SessionCount)
	}
	// Guard: the fixture must carry real tokens, or this test proves nothing.
	if resp.TotalInputTokens != 1000 {
		t.Errorf("total input tokens = %d, want the non-zero fixture value 1000", resp.TotalInputTokens)
	}
	if resp.TotalOutputTokens != 200 {
		t.Errorf("total output tokens = %d, want 200", resp.TotalOutputTokens)
	}
	if resp.TotalCacheReadTokens != 30 || resp.TotalCacheWriteTokens != 40 {
		t.Errorf("cache tokens = %d/%d, want 30/40", resp.TotalCacheReadTokens, resp.TotalCacheWriteTokens)
	}
	if len(resp.ByAgent) != 2 {
		t.Errorf("by_agent has %d entries, want 2", len(resp.ByAgent))
	}
}

func TestHandleUsage_SessionsReaderFilters(t *testing.T) {
	reader := InitSessionsReader(fixtureDir(t))

	cases := []struct {
		name         string
		query        string
		wantSessions int
		wantInput    int64
	}{
		{"agent", "?agent=nova", 1, 1000},
		{"backend", "?backend=codex", 1, 0},
		{"status", "?status=failed", 1, 0},
		{"since", "?since=2026-08-29", 1, 0},
		{"until", "?until=2026-08-28", 1, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr, resp := getUsage(t, reader, tc.query)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
			}
			if resp.SessionCount != tc.wantSessions {
				t.Errorf("session count = %d, want %d", resp.SessionCount, tc.wantSessions)
			}
			if resp.TotalInputTokens != tc.wantInput {
				t.Errorf("input tokens = %d, want %d", resp.TotalInputTokens, tc.wantInput)
			}
		})
	}
}

func TestHandleUsage_NilReaderIs503(t *testing.T) {
	rr, _ := getUsage(t, nil, "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestHandleUsage_InvalidDates(t *testing.T) {
	reader := InitSessionsReader(fixtureDir(t))
	for _, q := range []string{"?since=yesterday", "?until=08/29/2026"} {
		rr, _ := getUsage(t, reader, q)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rr.Code)
		}
	}
}

// TestHandleUsage_LegacyStoreStillSatisfiesReader keeps the legacy ledger
// readable through the same handler.
func TestHandleUsage_LegacyStoreStillSatisfiesReader(t *testing.T) {
	dir := t.TempDir()
	store := InitStore(dir)
	if store == nil {
		t.Fatal("InitStore returned nil")
	}
	var reader Reader = store

	rr, resp := getUsage(t, reader, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if resp.SessionCount != 0 {
		t.Errorf("session count = %d, want 0 for an empty legacy ledger", resp.SessionCount)
	}
	if resp.Sessions == nil {
		t.Error("sessions should be an empty array, not null")
	}
}

func TestInitSessionsReader_MissingIndexStillReads(t *testing.T) {
	reader := InitSessionsReader(t.TempDir())
	if reader == nil {
		t.Fatal("a directory without a session index should still yield a reader")
	}
	rr, resp := getUsage(t, reader, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if resp.SessionCount != 0 {
		t.Errorf("session count = %d, want 0", resp.SessionCount)
	}
}

// writeProfiles stages <dir>/profiles/<agent> directories, the shape
// agentprofiles.ConfiguredAgentNames reads the allowlist from.
func writeProfiles(t *testing.T, dir string, agents ...string) {
	t.Helper()
	for _, agent := range agents {
		if err := os.MkdirAll(filepath.Join(dir, "profiles", agent), 0o700); err != nil {
			t.Fatalf("mkdir profile %q: %v", agent, err)
		}
	}
}

// TestInitSessionsReader_ScopesToConfiguredAgents: the panel resolves the
// allowlist from the directory it is given, so a ledger row from an agent this
// workspace never configured cannot move the served totals.
func TestInitSessionsReader_ScopesToConfiguredAgents(t *testing.T) {
	dir := fixtureDir(t)
	writeProfiles(t, dir, "nova")

	reader := InitSessionsReader(dir)
	if reader == nil {
		t.Fatal("InitSessionsReader returned nil for a readable directory")
	}
	rr, resp := getUsage(t, reader, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if resp.SessionCount != 1 {
		t.Fatalf("session count = %d, want 1 (only nova is configured)", resp.SessionCount)
	}
	if len(resp.ByAgent) != 1 || resp.ByAgent[0].Name != "nova" {
		t.Errorf("by_agent = %+v, want nova alone", resp.ByAgent)
	}
}

// TestInitSessionsReader_NoProfilesDirServesEverything is the invariant: with
// no profiles/ directory the allowlist is empty and the panel reads the whole
// ledger, exactly as it did before this opt-in.
func TestInitSessionsReader_NoProfilesDirServesEverything(t *testing.T) {
	reader := InitSessionsReader(fixtureDir(t))
	if reader == nil {
		t.Fatal("InitSessionsReader returned nil for a readable directory")
	}
	rr, resp := getUsage(t, reader, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if resp.SessionCount != 2 {
		t.Errorf("session count = %d, want 2 (unfiltered)", resp.SessionCount)
	}
}

// TestInitSessionsReader_AllRowsUnconfiguredStillOpens: the openability probe
// stays unfiltered, so a workspace whose every ledger row belongs to an
// unconfigured agent gets an empty panel, not a 503.
func TestInitSessionsReader_AllRowsUnconfiguredStillOpens(t *testing.T) {
	dir := fixtureDir(t)
	writeProfiles(t, dir, "nobody-in-the-ledger")

	reader := InitSessionsReader(dir)
	if reader == nil {
		t.Fatal("InitSessionsReader returned nil; the probe must not be filtered")
	}
	rr, resp := getUsage(t, reader, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if resp.SessionCount != 0 {
		t.Errorf("session count = %d, want 0", resp.SessionCount)
	}
}
