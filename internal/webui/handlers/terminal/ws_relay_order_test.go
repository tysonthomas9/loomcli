package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket" //nolint:staticcheck // SA1019: websocket migration tracked separately

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// relayFakeAttachment serves a canned scrollback and never emits live output;
// closing the output channel ends the relay's pump goroutine.
type relayFakeAttachment struct {
	scrollback []byte
	output     chan []byte
}

func (a *relayFakeAttachment) ConnID() string                     { return "conn-1" }
func (a *relayFakeAttachment) Output() <-chan []byte              { return a.output }
func (a *relayFakeAttachment) WriteInput(p []byte) (int, error)   { return len(p), nil }
func (a *relayFakeAttachment) Scrollback() []byte                 { return a.scrollback }
func (a *relayFakeAttachment) Resize(_ string, _, _ uint16) error { return nil }
func (a *relayFakeAttachment) ExitReason() string                 { return "" }

// relayFakePTYSource hands runTerminalRelay a single canned attachment.
type relayFakePTYSource struct {
	att      *relayFakeAttachment
	reattach bool
}

func (f *relayFakePTYSource) AttachSession(_ webuterminal.SessionKey, _, _ uint16, _ *tabmeta.LaunchSpec) (webuterminal.Attachment, bool, error) {
	return f.att, f.reattach, nil
}
func (f *relayFakePTYSource) Detach(_ webuterminal.SessionKey, _ string) {}
func (f *relayFakePTYSource) Kill(_ webuterminal.SessionKey) error       { return nil }
func (f *relayFakePTYSource) HasSession(_ webuterminal.SessionKey) bool  { return true }
func (f *relayFakePTYSource) SessionClosed(_ webuterminal.SessionKey) bool {
	return false
}
func (f *relayFakePTYSource) AttachmentCount(_ webuterminal.SessionKey) int { return 1 }
func (f *relayFakePTYSource) SessionCount() int                             { return 1 }
func (f *relayFakePTYSource) SessionCountFor(_ string) int                  { return 1 }
func (f *relayFakePTYSource) MaxSessions() int                              { return 20 }

type relayFrame struct {
	typ  websocket.MessageType //nolint:staticcheck // SA1019
	data []byte
}

// runRelayForTest upgrades a real WebSocket, runs runTerminalRelay against it,
// and returns the frames the client observed in order.
func runRelayForTest(t *testing.T, p *terminalWSParams, scrollback []byte) []relayFrame {
	t.Helper()

	att := &relayFakeAttachment{scrollback: scrollback, output: make(chan []byte)}
	p.manager = &relayFakePTYSource{att: att}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil) //nolint:staticcheck // SA1019
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		ctx := middleware.WithWorkspace(r.Context(), "E2E")
		runTerminalRelay(ctx, conn, p, "term_1", "E2E", 80, 24)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil) //nolint:staticcheck // SA1019
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "") //nolint:staticcheck // SA1019

	// The control frame is the last thing the relay writes before going live,
	// so read until it arrives, then let the server's pump exit.
	var frames []relayFrame
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read frame %d: %v", len(frames), err)
		}
		frames = append(frames, relayFrame{typ: typ, data: data})
		if typ == websocket.MessageText { //nolint:staticcheck // SA1019
			break
		}
	}
	close(att.output)
	return frames
}

// The scrollback replay opens with \x1b[2J\x1b[H, which clears whatever the
// client already rendered. The attach control frame must therefore arrive
// AFTER it — writing it first is the bug this feature exists to fix.
func TestRelay_AttachControlFrameFollowsScrollbackReplay(t *testing.T) {
	tests := []struct {
		name       string
		scrollback []byte
		wantBinary int
	}{
		{
			name:       "non-empty scrollback",
			scrollback: []byte("\x1b[2J\x1b[Hprevious output\r\n"),
			wantBinary: 1,
		},
		{
			// A fresh spawn skips the replay write entirely, so there is no
			// \x1b[2J at all. This is the case that made a frame written too
			// early look correct in manual testing.
			name:       "empty scrollback",
			scrollback: nil,
			wantBinary: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTabMetaStoreForWSTest(t)
			startedAt := time.Now().UTC()
			ctx := context.Background()
			if err := store.Set(ctx, &tabmeta.TabMetadata{
				SessionName: "term_1",
				Workspace:   "E2E",
				Kind:        "shell",
				Launch:      &tabmeta.LaunchSpec{Argv: []string{"bash"}},
				CreatedAt:   startedAt.Add(-time.Hour),
				UpdatedAt:   startedAt.Add(-time.Hour),
			}); err != nil {
				t.Fatalf("seed Set: %v", err)
			}

			frames := runRelayForTest(t, &terminalWSParams{
				tabMetaStore:    store,
				serverStartedAt: startedAt,
			}, tt.scrollback)

			if len(frames) != tt.wantBinary+1 {
				t.Fatalf("got %d frames, want %d", len(frames), tt.wantBinary+1)
			}
			for i, f := range frames[:tt.wantBinary] {
				if f.typ != websocket.MessageBinary { //nolint:staticcheck // SA1019
					t.Fatalf("frame %d type = %v, want binary", i, f.typ)
				}
				if !bytes.Contains(f.data, []byte("\x1b[2J")) {
					t.Errorf("replay frame %d does not clear the screen: %q", i, f.data)
				}
			}

			last := frames[len(frames)-1]
			if last.typ != websocket.MessageText { //nolint:staticcheck // SA1019
				t.Fatalf("last frame type = %v, want text", last.typ)
			}
			var msg attachControlMessage
			if err := json.Unmarshal(last.data, &msg); err != nil {
				t.Fatalf("decode control frame %q: %v", last.data, err)
			}
			if msg.Type != "attach" || !msg.Replaced {
				t.Errorf("control frame = %+v, want an attach announcing the replacement", msg)
			}
			if msg.ReplacedReason != replacedReasonServerRestart {
				t.Errorf("replaced_reason = %q, want %q", msg.ReplacedReason, replacedReasonServerRestart)
			}

			// The marker is persisted, so a reload sees it without reattaching.
			stored, err := store.Get(ctx, "E2E", "term_1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if stored.ReplacedAt.IsZero() {
				t.Error("replaced_at was not persisted")
			}
		})
	}
}

// A tab created inside this server process is new, not replaced: the frame
// still goes out, but it announces nothing.
func TestRelay_FreshTabIsNotAReplacement(t *testing.T) {
	store := newTabMetaStoreForWSTest(t)
	startedAt := time.Now().UTC().Add(-time.Hour)
	if err := store.Set(context.Background(), &tabmeta.TabMetadata{
		SessionName: "term_1",
		Workspace:   "E2E",
		Launch:      &tabmeta.LaunchSpec{Argv: []string{"bash"}},
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	frames := runRelayForTest(t, &terminalWSParams{
		tabMetaStore:    store,
		serverStartedAt: startedAt,
	}, nil)

	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	var msg attachControlMessage
	if err := json.Unmarshal(frames[0].data, &msg); err != nil {
		t.Fatalf("decode control frame: %v", err)
	}
	if msg.Replaced || msg.ReplacedAt != "" {
		t.Errorf("control frame = %+v, want no replacement", msg)
	}
}
