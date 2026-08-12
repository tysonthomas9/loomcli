package transcript_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts/transcript"
)

// TestTSLeafCorpusConformsToCanonicalSchema is the Phase-U/U0 contract lock: it
// pins the TypeScript local-task-runner leaf's `transcript_entries` output to the
// canonical Go transcript.Event schema, so the driver plane (TS) and the daemon
// plane (Go) cannot silently drift apart on transcript shape.
//
// testdata/ts_leaf_corpus.json is produced by running the REAL TS parser
// (internal/infra/workflowdistribution/builtin/local-task-runner.ts parseStreamJSONTranscript) over
// representative claude/codex/cursor stream-json — regenerate with
// scripts/gen-ts-leaf-transcript-corpus.mjs whenever the parser changes.
func TestTSLeafCorpusConformsToCanonicalSchema(t *testing.T) {
	raw, err := os.ReadFile("testdata/ts_leaf_corpus.json")
	if err != nil {
		t.Fatalf("read TS-leaf corpus: %v", err)
	}

	// (1) The raw TS output must decode into []transcript.Event without error.
	// Timestamp is a time.Time, so a non-RFC3339 stamp would fail HERE — that is
	// the latent "one bad timestamp poisons the whole run" hazard, pinned shut.
	var byBackend map[string][]transcript.Event
	if err := json.Unmarshal(raw, &byBackend); err != nil {
		t.Fatalf("TS-leaf transcript_entries must decode into []transcript.Event: %v", err)
	}
	if len(byBackend) == 0 {
		t.Fatal("corpus is empty")
	}

	seenType := map[string]bool{}
	seenRole := map[string]bool{}
	total := 0

	for backend, events := range byBackend {
		if len(events) == 0 {
			t.Errorf("%s: corpus has no entries", backend)
			continue
		}
		// (2) Every transcript opens with a session_meta marker.
		if events[0].Type != transcript.EventSessionMeta {
			t.Errorf("%s: first entry type = %q, want %q", backend, events[0].Type, transcript.EventSessionMeta)
		}
		lastSeq := 0
		for i, e := range events {
			total++
			seenType[e.Type] = true
			seenRole[e.Role] = true
			// (3) type, role, and timestamp must satisfy the shared canonical
			// validator used by both local and durable transcript readers.
			if err := transcript.ValidateCanonicalEvent(e); err != nil {
				t.Errorf("%s[%d]: %v", backend, i, err)
			}
			// (4) seq is monotonically increasing within a backend's transcript.
			if e.Seq <= lastSeq {
				t.Errorf("%s[%d]: seq %d not greater than previous %d", backend, i, e.Seq, lastSeq)
			}
			lastSeq = e.Seq
		}
	}

	// (6) The corpus must actually exercise the two TS-only types this phase
	// blessed (reasoning + result) — otherwise the test isn't pinning the drift
	// that motivated U0.
	for _, required := range []string{transcript.EventReasoning, transcript.EventResult, transcript.EventToolUse, transcript.EventToolResult} {
		if !seenType[required] {
			t.Errorf("corpus does not exercise canonical type %q (regenerate the corpus)", required)
		}
	}
	if total < 6 {
		t.Errorf("corpus too small (%d entries) to be a meaningful contract", total)
	}
}

// TestNonRFC3339TimestampFailsDecode documents the hazard the TS leaf's toISO guard
// exists to prevent: a single non-RFC3339 timestamp string fails the WHOLE
// []transcript.Event decode (turning a successful run into a task failure). The TS
// side must never emit one; this pins that a bad stamp is genuinely fatal at the Go
// boundary, justifying the guard.
func TestNonRFC3339TimestampFailsDecode(t *testing.T) {
	bad := `[{"seq":1,"timestamp":"not-a-date","role":"system","type":"session_meta"}]`
	var events []transcript.Event
	if err := json.Unmarshal([]byte(bad), &events); err == nil {
		t.Fatal("expected a non-RFC3339 timestamp to fail the []transcript.Event decode, but it succeeded")
	}
}
