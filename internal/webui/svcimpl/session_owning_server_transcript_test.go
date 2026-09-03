package svcimpl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const rawCodexRollout = `{"type":"session_meta","payload":{"cwd":"/work"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"lead plan"}]}}
`

// writeRawNativeTranscript materializes a session the way a lead does — the
// directory is created by EnsureSession, not CreateSession — and mirrors a raw
// provider rollout into it.
func writeRawNativeTranscript(t *testing.T, runtimeDir, sessionID, backend string) *sessions.Store {
	t.Helper()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := sessStore.EnsureSession(sessionID, backend); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	src := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(src, []byte(rawCodexRollout), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	if err := sessStore.SyncNativeTranscript(sessionID, src, sessions.TranscriptFormatRaw); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}
	return sessStore
}

// TestSessionTranscriptOwningServerParsesRawLeadRollout is the local-mode case:
// loom serve shares its runtime directory with the lead, so the session
// directory the lead materializes makes the reader take the local branch. That
// branch parses by the recorded backend, so a lead that captures without one
// reads back as an empty success — the exact failure the artifact markers exist
// to prevent, relocated onto loom's default deployment.
func TestSessionTranscriptOwningServerParsesRawLeadRollout(t *testing.T) {
	runtimeDir := t.TempDir()
	writeRawNativeTranscript(t, runtimeDir, "lead-owning-codex", "codex")

	events, err := NewSessionServiceWithRuntimeDir(nil, runtimeDir).
		GetSessionTranscriptByID(t.Context(), "WS", "lead-owning-codex")
	if err != nil {
		t.Fatalf("GetSessionTranscriptByID: %v", err)
	}
	if len(events) != 1 || events[0].Role != "assistant" || events[0].Text != "lead plan" {
		t.Fatalf("events = %+v, want the codex rollout parsed as one assistant message", events)
	}
}

// TestSessionTranscriptEmptyLocalFallsBackToControlPlane covers the reader's
// second line of defense. A local session whose metadata records no backend
// parses to zero events without erroring, which is indistinguishable from a
// mis-parse; the control-plane artifact carries explicit format markers, so
// prefer it whenever the local read comes back with nothing.
func TestSessionTranscriptEmptyLocalFallsBackToControlPlane(t *testing.T) {
	runtimeDir := t.TempDir()
	writeRawNativeTranscript(t, runtimeDir, "lead-no-backend", "")

	st := memstore.New()
	createFinalizedArtifact(t, st, "transcript-lead-no-backend", "lead-no-backend", "", "lead-no-backend",
		"transcript", "application/x-ndjson", []byte(rawCodexRollout))
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "lead-no-backend", AgentID: "lead",
		Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"transcript_ref":     "artifact://transcript-lead-no-backend",
			"transcript_format":  sessions.TranscriptFormatRaw,
			"transcript_backend": "codex",
		},
	}); err != nil {
		t.Fatalf("create session record: %v", err)
	}

	events, err := NewSessionServiceWithRuntimeDir(st, runtimeDir).
		GetSessionTranscriptByID(t.Context(), "WS", "lead-no-backend")
	if err != nil {
		t.Fatalf("GetSessionTranscriptByID: %v", err)
	}
	if len(events) != 1 || events[0].Text != "lead plan" {
		t.Fatalf("events = %+v, want the control-plane artifact to rescue an empty local read", events)
	}
}

// TestParseCanonicalTranscriptBytesRejectsNonCanonical pins the guard the local
// decoder already had. Without it a record with no format marker returns a
// structurally valid, semantically empty success instead of an honest miss.
func TestParseCanonicalTranscriptBytesRejectsNonCanonical(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "canonical jsonl", body: `{"seq":1,"role":"assistant","type":"text","text":"hi"}` + "\n"},
		{name: "canonical array", body: `[{"seq":1,"role":"assistant","type":"text","text":"hi"}]`},
		{name: "empty", body: "  \n"},
		{name: "raw codex jsonl", body: rawCodexRollout, wantErr: true},
		{name: "raw codex array", body: `[{"type":"response_item","payload":{"role":"assistant"}}]`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCanonicalTranscriptBytes([]byte(tc.body))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
