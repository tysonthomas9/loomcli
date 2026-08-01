package prreview

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"
	"github.com/olesho/harness-wrapper/pkg/transcript/claudecode"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// Conversation states beyond the codex path's starting/reconnecting/idle/
// running: failed (the reviewer runtime died or its transcript is
// unreadable) and unsupported (the backend has no readable conversation —
// the terminal tab still works).
const (
	reviewerStateFailed      = "failed"
	reviewerStateUnsupported = "unsupported"
)

// harnessReadRetryDelay is the pause before the single re-read after a parse
// error. Harness CLIs append to their transcript while we read; a torn final
// line fails some parsers (gemini errors the whole read), and one short retry
// almost always lands after the append completes.
const harnessReadRetryDelay = 75 * time.Millisecond

// harnessTranscriptReader is the slice of the wrapper's transcript.Reader the
// conversation read needs; a package var map keyed by runtime provider so
// tests can substitute fakes or re-rooted readers.
type harnessTranscriptReader interface {
	Read(harnessSessionID, workingDir string) ([]hwtranscript.Event, error)
}

// harnessTranscriptReaders maps a lead runtime provider to the reader for its
// harness-owned transcript. Providers absent here (opencode, cursor, gemini)
// have no known transcript source; their chat state is "unsupported" while the
// terminal tab remains fully functional. Codex is deliberately absent: its
// conversation is read live over the app-server socket, not from files.
//
// Gemini was dropped when harness-wrapper removed its gemini support wholesale
// (transcript reader, harness profile, discovery probe, and versions entry). A
// gemini lead therefore degrades to "unsupported" here rather than failing to
// build against a package that no longer exists upstream.
var harnessTranscriptReaders = map[string]harnessTranscriptReader{
	"claude": claudecode.New(),
}

// reviewerSnapshot is the provider-neutral conversation snapshot both the SSE
// stream and the poll endpoint serve.
type reviewerSnapshot struct {
	state    string
	detail   string
	messages []reviewerStreamMessage
}

// readHarnessReviewerSnapshot reads a harness-backed (non-codex) reviewer's
// conversation from the harness's own transcript on disk.
func (m *Module) readHarnessReviewerSnapshot(session reviewerStreamSession, sess *domain.AgentSession, provider string) reviewerSnapshot {
	reader, ok := harnessTranscriptReaders[provider]
	if !ok {
		return reviewerSnapshot{
			state:  reviewerStateUnsupported,
			detail: "The chat view is not available for the " + provider + " backend. Use the Terminal tab to talk to the reviewer.",
		}
	}
	hrt := leadcontrol.HarnessRuntimeMetadataFromSession(sess)
	if hrt.HarnessSessionID == "" {
		// The reader exists but the harness's own session id was never
		// learned. Claude pins the id at launch, so an empty id there just
		// means the runtime is still booting; other harnesses (gemini) have
		// no id source yet, so the transcript is unreachable — say so
		// instead of spinning on "starting" forever.
		if provider == "claude" {
			return reviewerSnapshot{state: "starting"}
		}
		return reviewerSnapshot{
			state:  reviewerStateUnsupported,
			detail: "The " + provider + " backend does not expose its session id, so the chat view cannot read its conversation yet. Use the Terminal tab.",
		}
	}
	workDir, ok := localworkspace.RememberedAgentWorktree(session.ws, session.agentName)
	if !ok {
		return reviewerSnapshot{
			state:  reviewerStateFailed,
			detail: "The reviewer's worktree is no longer available. Close and reopen the reviewer to recreate it.",
		}
	}
	sessionID := hrt.HarnessSessionID
	events, err := readHarnessTranscriptWithRetry(reader, sessionID, workDir)
	if errors.Is(err, fs.ErrNotExist) && provider == "claude" {
		// Claude rotates its session id when the first boot passes through
		// the folder-trust dialog — which every fresh PR worktree does — so
		// the launch-pinned id can name a file that will never exist.
		// Reconcile against reality: the newest transcript in this
		// worktree's project dir recorded AFTER this runtime started. The
		// time guard is what makes this safe — an older reviewer session in
		// the same worktree can never be picked up.
		if rotated, ok := newestClaudeSessionSince(reader, workDir, hrt.StartedAt); ok {
			sessionID = rotated
			events, err = readHarnessTranscriptWithRetry(reader, sessionID, workDir)
		}
	}
	state := reviewerStateFromRuntimeStatus(hrt.Status)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// The harness hasn't written its transcript yet (normal in the
			// first moments after boot) — the runtime status is still the
			// truth, there is just nothing to show.
			return reviewerSnapshot{state: state}
		}
		return reviewerSnapshot{state: "reconnecting"}
	}
	return reviewerSnapshot{
		state:    state,
		messages: reviewerMessagesFromEvents(provider, sessionID, events),
	}
}

// newestClaudeSessionSince returns the session id (file basename) of the
// newest transcript in the claude project dir for workDir whose mtime is at
// or after since. Returns ok=false when since is zero (no launch timestamp —
// a guardless newest-file scan could surface a stale session) or when no
// qualifying transcript exists.
func newestClaudeSessionSince(reader harnessTranscriptReader, workDir string, since time.Time) (string, bool) {
	if since.IsZero() {
		return "", false
	}
	root := ""
	if cr, ok := reader.(*claudecode.Reader); ok {
		root = cr.ProjectsRoot
	}
	if root == "" {
		configDir := sessions.ClaudeConfigDir()
		if configDir == "" {
			return "", false
		}
		root = filepath.Join(configDir, "projects")
	}
	entries, err := os.ReadDir(filepath.Join(root, claudecode.EncodedCWD(workDir)))
	if err != nil {
		return "", false
	}
	best, bestTime := "", time.Time{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		if info.ModTime().After(bestTime) {
			best, bestTime = strings.TrimSuffix(entry.Name(), ".jsonl"), info.ModTime()
		}
	}
	return best, best != ""
}

func readHarnessTranscriptWithRetry(reader harnessTranscriptReader, harnessSessionID, workDir string) ([]hwtranscript.Event, error) {
	events, err := reader.Read(harnessSessionID, workDir)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return events, err
	}
	time.Sleep(harnessReadRetryDelay)
	return reader.Read(harnessSessionID, workDir)
}

// reviewerStateFromRuntimeStatus maps the harness runtime status vocabulary
// (leadcontrol.RuntimeStatus*) onto the conversation state the chat panel
// shows. Exhaustive over the runtime vocabulary; unrecognized values map to
// failed so a new runtime status surfaces visibly instead of masquerading as
// progress.
func reviewerStateFromRuntimeStatus(status string) string {
	switch status {
	case leadcontrol.RuntimeStatusActive, leadcontrol.RuntimeStatusWaitingApproval:
		return "running"
	case leadcontrol.RuntimeStatusIdle, leadcontrol.RuntimeStatusWaitingUserInput:
		return "idle"
	case "", leadcontrol.RuntimeStatusStarting:
		return "starting"
	case leadcontrol.RuntimeStatusDisconnected:
		return "reconnecting"
	default:
		return reviewerStateFailed
	}
}

// reviewerMessagesFromEvents flattens canonical transcript events into chat
// messages: text events with a user/assistant role and non-empty text; tool
// calls, tool results, and system/session records are skipped. IDs are
// namespaced by provider+session so a backend migration can never collide
// ids across transcripts, and de-duplicated with an ordinal because
// Event.ID() can repeat (gemini's content-hash fallback collides for
// identical messages with absent timestamps).
func reviewerMessagesFromEvents(provider, harnessSessionID string, events []hwtranscript.Event) []reviewerStreamMessage {
	idPrefix := provider + "/" + shortSessionID(harnessSessionID) + "/"
	ordinals := make(map[string]int)
	var out []reviewerStreamMessage
	for _, ev := range events {
		if ev.Type != hwtranscript.EventText {
			continue
		}
		if ev.Role != hwtranscript.RoleUser && ev.Role != hwtranscript.RoleAssistant {
			continue
		}
		if strings.TrimSpace(ev.Text) == "" {
			continue
		}
		base := ev.ID()
		itemID := idPrefix + base
		if n := ordinals[base]; n > 0 {
			itemID += "#" + strconv.Itoa(n)
		}
		ordinals[base]++
		turnID := itemID
		if ev.UUID != "" {
			// Multiple content blocks of one native message share the message
			// UUID — group them under one turn.
			turnID = idPrefix + "msg:" + ev.UUID
		}
		out = append(out, reviewerStreamMessage{
			TurnID: turnID,
			ItemID: itemID,
			Role:   ev.Role,
			Text:   ev.Text,
		})
	}
	return trimReviewerPreamble(out)
}

func shortSessionID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
