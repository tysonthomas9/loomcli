package leadcontrol

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	pinnedSessionID  = "11111111-1111-1111-1111-111111111111"
	scrapedSessionID = "22222222-2222-2222-2222-222222222222"
)

func newBackfillWatcher(t *testing.T, st store.Store, pinned string) (*harnessLeadRuntimeWatcher, *fakeHarnessConversation) {
	t.Helper()
	fake := newFakeHarnessConversation()
	return &harnessLeadRuntimeWatcher{
		cfg: HarnessLeadRuntimeConfig{
			Store: st, Workspace: "WS", LeadName: "nova", SessionID: "lead-session",
			Logger: slog.New(slog.DiscardHandler),
		},
		conv: fake,
		runtime: HarnessRuntimeMetadata{
			HarnessSessionID: pinned,
			StartedAt:        time.Now().UTC(),
		},
	}, fake
}

func persistedHarnessSessionID(t *testing.T, st store.Store) string {
	t.Helper()
	session, err := st.AgentSessions().Get(context.Background(), "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return session.Metadata[MetadataHarnessSessionID]
}

// Claude rotates its session id on a first boot that clears the folder-trust
// dialog, and may fork on resume. The id whose transcript exists on disk is the
// SCRAPED one, so the pin must yield to it -- otherwise the next --continue
// resumes an id with nothing behind it.
func TestBackfillHarnessSessionIDAdoptsRotatedID(t *testing.T) {
	st := memstore.New()
	createHarnessLeadSession(t, st)
	w, fake := newBackfillWatcher(t, st, pinnedSessionID)
	fake.harnessSessionID = scrapedSessionID

	w.backfillHarnessSessionID(context.Background())

	if w.runtime.HarnessSessionID != scrapedSessionID {
		t.Fatalf("runtime id = %q, want the scraped %q", w.runtime.HarnessSessionID, scrapedSessionID)
	}
	if got := persistedHarnessSessionID(t, st); got != scrapedSessionID {
		t.Fatalf("persisted id = %q, want the scraped %q", got, scrapedSessionID)
	}
}

// A scrape that is not a UUID is TUI noise, not a session id. It must never
// overwrite a good pin.
func TestBackfillHarnessSessionIDIgnoresNonUUIDScrape(t *testing.T) {
	st := memstore.New()
	createHarnessLeadSession(t, st)
	w, fake := newBackfillWatcher(t, st, pinnedSessionID)
	fake.harnessSessionID = "Session ID: (loading)"

	w.backfillHarnessSessionID(context.Background())

	if w.runtime.HarnessSessionID != pinnedSessionID {
		t.Fatalf("runtime id = %q, want the pin kept", w.runtime.HarnessSessionID)
	}
}

// StartedAt is what transcript readers use to tell this run's files from an
// earlier run's. Without it an adoption cannot be reconciled, so it is refused.
func TestBackfillHarnessSessionIDRequiresStartedAt(t *testing.T) {
	st := memstore.New()
	createHarnessLeadSession(t, st)
	w, fake := newBackfillWatcher(t, st, pinnedSessionID)
	w.runtime.StartedAt = time.Time{}
	fake.harnessSessionID = scrapedSessionID

	w.backfillHarnessSessionID(context.Background())

	if w.runtime.HarnessSessionID != pinnedSessionID {
		t.Fatalf("runtime id = %q, want the pin kept without a launch instant", w.runtime.HarnessSessionID)
	}
}

func TestBackfillHarnessSessionIDIdenticalScrapeIsANoOp(t *testing.T) {
	st := memstore.New()
	createHarnessLeadSession(t, st)
	w, fake := newBackfillWatcher(t, st, pinnedSessionID)
	fake.harnessSessionID = pinnedSessionID

	w.backfillHarnessSessionID(context.Background())

	if got := persistedHarnessSessionID(t, st); got != "" {
		t.Fatalf("persisted id = %q, want no write for an identical scrape", got)
	}
}

// The original behavior: an unpinned launch takes whatever it scrapes. The
// UUID guard applies only to OVERWRITING a pin, so a backend whose session id
// is not a uuid keeps working.
func TestBackfillHarnessSessionIDStillFillsAnEmptyPin(t *testing.T) {
	st := memstore.New()
	createHarnessLeadSession(t, st)
	w, fake := newBackfillWatcher(t, st, "")
	fake.harnessSessionID = "not-a-uuid-but-real"

	w.backfillHarnessSessionID(context.Background())

	if w.runtime.HarnessSessionID != "not-a-uuid-but-real" {
		t.Fatalf("runtime id = %q, want the scrape adopted into an empty pin", w.runtime.HarnessSessionID)
	}
}

// ── codex argv ──────────────────────────────────────────────────────────────

// Fresh and resumed argv must differ ONLY in the leading resume tokens: the
// prompt is positional in both `codex` and `codex resume`, so every other flag
// keeps its position.
func TestCodexResumeArgs(t *testing.T) {
	if got := codexResumeArgs(CodexLeadRuntimeConfig{}); got != nil {
		t.Fatalf("fresh codexResumeArgs() = %#v, want nil", got)
	}
	got := codexResumeArgs(CodexLeadRuntimeConfig{ResumeThreadID: "thread-9"})
	if strings.Join(got, " ") != "resume thread-9" {
		t.Fatalf("codexResumeArgs() = %#v, want [resume thread-9]", got)
	}
	got = codexResumeArgs(CodexLeadRuntimeConfig{ResumeLast: true})
	if strings.Join(got, " ") != "resume --last" {
		t.Fatalf("codexResumeArgs() = %#v, want [resume --last]", got)
	}
	// A recorded thread id always beats --last: it is the thread loom itself
	// launched, whereas --last is codex's own guess.
	got = codexResumeArgs(CodexLeadRuntimeConfig{ResumeThreadID: "thread-9", ResumeLast: true})
	if strings.Join(got, " ") != "resume thread-9" {
		t.Fatalf("codexResumeArgs() = %#v, want the recorded thread to win", got)
	}
}

// ── resume failure hint ─────────────────────────────────────────────────────

// "claude exited with status 1" cannot distinguish a wrong id from a deleted
// transcript. Naming both the id and the directory makes it a one-look call.
func TestResumeFailureHintNamesIDAndTranscriptDir(t *testing.T) {
	if got := resumeFailureHint(HarnessLeadRuntimeConfig{Backend: "claude", WorkDir: "/repo"}); got != "" {
		t.Fatalf("fresh launch hint = %q, want empty", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/claude-cfg")
	hint := resumeFailureHint(HarnessLeadRuntimeConfig{
		Backend: "claude", WorkDir: "/repo", ResumedFromSessionID: pinnedSessionID,
	})
	if !strings.Contains(hint, pinnedSessionID) {
		t.Fatalf("hint = %q, want the resumed id", hint)
	}
	if !strings.Contains(hint, "/tmp/claude-cfg/projects/") {
		t.Fatalf("hint = %q, want the transcript dir", hint)
	}
}
