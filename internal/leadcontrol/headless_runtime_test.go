package leadcontrol

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// writeFakeHeadlessAgent installs a stand-in for `cursor-agent -p --resume`:
// it appends "<chat>|<message>" to a log file and emits the stream-json
// result line the runtime parses. Exits 1 with is_error when the message
// contains "FAIL-ME".
func writeFakeHeadlessAgent(t *testing.T, dir string) (bin, turnLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake agent script needs a POSIX shell")
	}
	turnLog = filepath.Join(dir, "turns.log")
	bin = filepath.Join(dir, "fake-agent")
	script := `#!/bin/sh
# args: <fixed...> --resume <chat> <message>
chat=""; msg=""
while [ $# -gt 0 ]; do
  case "$1" in --resume) chat="$2"; shift 2;; *) msg="$1"; shift;; esac
done
printf '%s|%s\n' "$chat" "$msg" >> "` + turnLog + `"
printf '{"type":"system","subtype":"init","session_id":"%s"}\n' "$chat"
printf '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"echo: %s"}]},"session_id":"%s"}\n' "$msg" "$chat"
case "$msg" in *FAIL-ME*)
  printf '{"type":"result","subtype":"error","is_error":true,"result":"boom","session_id":"%s"}\n' "$chat"; exit 1;;
esac
printf '{"type":"result","subtype":"success","duration_ms":5,"is_error":false,"result":"ok","session_id":"%s","usage":{"inputTokens":1,"outputTokens":2}}\n' "$chat"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, turnLog
}

// syncBuffer is a goroutine-safe bytes.Buffer for runtime stdout.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func readTurnLog(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestRunHeadlessLeadRuntimeSeedsThenDrainsInbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	dir := t.TempDir()
	bin, turnLog := writeFakeHeadlessAgent(t, dir)

	var out syncBuffer
	runtimeErr := make(chan error, 1)
	go func() {
		runtimeErr <- RunHeadlessLeadRuntime(ctx, HeadlessLeadRuntimeConfig{
			Store:         st,
			Workspace:     "WS",
			LeadName:      "nova",
			SessionID:     "lead-session",
			WorkDir:       dir,
			Prompt:        "seed prompt",
			Backend:       "cursor",
			BinaryPath:    bin,
			Args:          []string{"-p", "--force"},
			ChatSessionID: "chat-123",
			Stdout:        &out,
			Stderr:        &out,
		})
	}()

	// Seed turn ran with the pinned chat id and the runtime settled idle.
	waitForCondition(t, func() bool {
		return getLeadSession(t, st).Metadata[MetadataRuntimeStatus] == RuntimeStatusIdle
	}, "runtime never became idle after the seed turn")
	turns := readTurnLog(t, turnLog)
	if len(turns) != 1 || turns[0] != "chat-123|seed prompt" {
		t.Fatalf("seed turn log = %q", turns)
	}
	meta := getLeadSession(t, st).Metadata
	if meta[MetadataHarnessName] != HarnessNameHeadless || meta[MetadataRuntimeProvider] != "cursor" ||
		meta[MetadataHarnessChatSessionID] != "chat-123" || meta[MetadataRuntimeControlled] != "true" {
		t.Fatalf("runtime metadata = %+v", meta)
	}

	// A message enqueued by another process (leadmsg) is drained into a
	// resumed turn — and a failing turn still leaves the session idle so the
	// next message is attempted.
	for i, msg := range []string{"FAIL-ME please", "second message"} {
		if _, err := createLeadInboxMessage(ctx, st, "WS", "nova", "lead-session", msg, LeadMessageDeliveryOptions{
			SourceKind: "user_chat", DedupeKey: "headless-" + string(rune('a'+i)),
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	waitForCondition(t, func() bool { return len(readTurnLog(t, turnLog)) == 3 }, "drain never ran both queued turns")
	turns = readTurnLog(t, turnLog)
	if turns[1] != "chat-123|FAIL-ME please" || turns[2] != "chat-123|second message" {
		t.Fatalf("turn log = %q", turns)
	}
	waitForCondition(t, func() bool {
		return strings.Contains(out.String(), "echo: second message") &&
			getLeadSession(t, st).Metadata[MetadataRuntimeStatus] == RuntimeStatusIdle
	}, "runtime not idle (with the assistant echo) after drained turns")
	if !strings.Contains(out.String(), "[lead turn 2 failed") {
		t.Fatalf("stdout missing failure note:\n%s", out.String())
	}

	cancel()
	select {
	case err := <-runtimeErr:
		if err != nil {
			t.Fatalf("runtime returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not exit on cancel")
	}
	if s := getLeadSession(t, st).Metadata[MetadataRuntimeStatus]; s != RuntimeStatusDisconnected {
		t.Fatalf("final status = %q, want disconnected", s)
	}
	if lookupHeadlessRuntime("lead-session") != nil {
		t.Fatal("runtime still registered after exit")
	}
}

func TestRunHeadlessLeadRuntimeSeedFailureIsLaunchFailure(t *testing.T) {
	st := memstore.New()
	createHarnessLeadSession(t, st)
	dir := t.TempDir()
	bin, _ := writeFakeHeadlessAgent(t, dir)
	var out syncBuffer
	err := RunHeadlessLeadRuntime(context.Background(), HeadlessLeadRuntimeConfig{
		Store: st, Workspace: "WS", LeadName: "nova", SessionID: "lead-session",
		WorkDir: dir, Prompt: "FAIL-ME seed", Backend: "cursor", BinaryPath: bin,
		Stdout: &out, Stderr: &out,
	})
	if err == nil || !strings.Contains(err.Error(), "seed turn") {
		t.Fatalf("err = %v, want seed turn failure", err)
	}
	if s := getLeadSession(t, st).Metadata[MetadataRuntimeStatus]; s != RuntimeStatusFailed {
		t.Fatalf("status = %q, want failed", s)
	}
}

func TestRunHeadlessLeadRuntimeMissingBinaryIsLaunchFailure(t *testing.T) {
	st := memstore.New()
	createHarnessLeadSession(t, st)
	var out syncBuffer
	err := RunHeadlessLeadRuntime(context.Background(), HeadlessLeadRuntimeConfig{
		Store: st, Workspace: "WS", LeadName: "nova", SessionID: "lead-session",
		WorkDir: t.TempDir(), Prompt: "seed", Backend: "cursor",
		BinaryPath: filepath.Join(t.TempDir(), "no-such-agent"),
		Stdout:     &out, Stderr: &out,
	})
	if err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("err = %v, want a process start failure", err)
	}
	if lookupHeadlessRuntime("lead-session") != nil {
		t.Fatal("runtime still registered after a failed launch")
	}
}

// A message delivered from another process while the runtime is busy must
// stay queued (never leased and handed back), so the runtime's own drain
// always takes the oldest message next.
func TestHeadlessCrossProcessDeliveryIsEnqueueOnly(t *testing.T) {
	st := memstore.New()
	createHarnessLeadSession(t, st)
	ctx := context.Background()
	if err := UpdateHarnessRuntimeMetadata(ctx, st, "WS", "lead-session", HarnessRuntimeMetadata{
		Provider: "cursor", HarnessName: HarnessNameHeadless, ChatSessionID: "chat-1", Controlled: true, Status: RuntimeStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"first", "second"} {
		res, err := DeliverLeadMessage(ctx, st, "WS", "nova", msg)
		if err != nil {
			t.Fatal(err)
		}
		if res.State != DeliveryStatePending || res.Reason != harnessRegistryMissReason {
			t.Fatalf("%s: result = %+v", msg, res)
		}
	}
	// Both messages are still queued, in order, unclaimed.
	msgs, err := st.AgentInboxMessages().List(ctx, "WS", store.AgentInboxMessageFilter{TargetAgentID: "nova"})
	if err != nil {
		t.Fatal(err)
	}
	var queued []string
	for _, m := range msgs {
		if m.Status == domain.AgentInboxMessageQueued && m.ClaimedBy == "" {
			queued = append(queued, m.Body)
		}
	}
	if len(queued) != 2 || queued[0] != "first" || queued[1] != "second" {
		t.Fatalf("queued = %q, want [first second]", queued)
	}
}

func TestDelivererForSessionPicksHeadless(t *testing.T) {
	st := memstore.New()
	createHarnessLeadSession(t, st)
	ctx := context.Background()
	if err := UpdateHarnessRuntimeMetadata(ctx, st, "WS", "lead-session", HarnessRuntimeMetadata{
		Provider: "cursor", HarnessName: HarnessNameHeadless, ChatSessionID: "chat-1", Controlled: true, Status: RuntimeStatusIdle,
	}); err != nil {
		t.Fatal(err)
	}
	session := getLeadSession(t, st)
	if _, ok := delivererForSession(session).(*headlessTurnDeliverer); !ok {
		t.Fatalf("deliverer = %T, want *headlessTurnDeliverer", delivererForSession(session))
	}
	// Cross-process delivery (no registered runtime) stays enqueue-only.
	res, err := DeliverLeadMessage(ctx, st, "WS", "nova", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if res.State != DeliveryStatePending || res.Reason != harnessRegistryMissReason {
		t.Fatalf("result = %+v", res)
	}
}
