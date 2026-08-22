package terminal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket" //nolint:staticcheck

	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal/proto"
)

func TestTerminalCloseStatus(t *testing.T) {
	tests := []struct {
		reason webuterminal.CloseReason
		code   websocket.StatusCode //nolint:staticcheck
		text   string
	}{
		{webuterminal.CloseExited, 4001, "exited"},
		{webuterminal.CloseKilled, 4002, "killed"},
		{webuterminal.CloseShutdown, websocket.StatusGoingAway, "shutdown"},
		{webuterminal.CloseSlowConsumer, 4003, "slow consumer; resnapshot required"},
		{webuterminal.CloseStateRebuild, 4004, "state rebuilding; retry"},
		{webuterminal.CloseReplaced, websocket.StatusNormalClosure, "replaced"},
	}
	for _, tt := range tests {
		code, text := terminalCloseStatus(tt.reason)
		if code != tt.code || text != tt.text {
			t.Errorf("%q = (%d, %q), want (%d, %q)", tt.reason, code, text, tt.code, tt.text)
		}
	}
}

func TestRelayV1RejectsMissingSubprotocol(t *testing.T) { //nolint:paralleltest
	server := newRelayTestServer(t, newRelayTestAttachment())
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+server.URL[4:], nil) //nolint:staticcheck
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")          //nolint:staticcheck
	_, _, err = conn.Read(context.Background())                      //nolint:staticcheck
	if websocket.CloseStatus(err) != websocket.StatusProtocolError { //nolint:staticcheck
		t.Fatalf("close error = %v, status=%d", err, websocket.CloseStatus(err))
	}
}

func TestEventFrameAndGenerationMismatch(t *testing.T) {
	gen := webuterminal.Generation{1}
	data, err := eventFrame(gen, webuterminal.TerminalEvent{Sequence: 7, Kind: webuterminal.EventOutput, Data: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := proto.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Kind != proto.KindOutput || frame.Sequence != 7 || string(frame.Data) != "hi" {
		t.Fatalf("frame = %#v", frame)
	}
	if _, err := eventFrame(gen, webuterminal.TerminalEvent{Kind: 99}); !errors.Is(err, proto.ErrUnknownKind) {
		t.Fatalf("unknown event error = %v", err)
	}
}

func TestRelayV1InitialStateFirstAndMalformedCloses1002(t *testing.T) { //nolint:paralleltest
	att := newRelayTestAttachment()
	att.initial.Generation = webuterminal.Generation{2}
	att.initial.Sequence, att.initial.Cols, att.initial.Rows = 4, 80, 24
	att.initial.Encoding, att.initial.Data = "xterm-vt/1", []byte("snapshot")
	server := newRelayTestServer(t, att)
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+server.URL[4:], &websocket.DialOptions{Subprotocols: []string{terminalV1Subprotocol}}) //nolint:staticcheck
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done") //nolint:staticcheck
	_, data, err := conn.Read(context.Background())         //nolint:staticcheck
	if err != nil {
		t.Fatal(err)
	}
	initial, err := proto.Decode(data)
	if err != nil || initial.Kind != proto.KindInitialState {
		t.Fatalf("initial = %#v, err=%v", initial, err)
	}
	wrongGen := proto.Frame{Kind: proto.KindInput, Generation: webuterminal.Generation{9}, Data: []byte("ignored")}
	data, err = proto.Encode(wrongGen)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(context.Background(), websocket.MessageBinary, data); err != nil { //nolint:staticcheck
		t.Fatal(err)
	}
	wrongGen.Generation = att.initial.Generation
	wrongGen.Data = []byte("accepted")
	data, err = proto.Encode(wrongGen)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(context.Background(), websocket.MessageBinary, data); err != nil { //nolint:staticcheck
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		att.mu.Lock()
		count := len(att.inputs)
		att.mu.Unlock()
		if count == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	att.mu.Lock()
	if len(att.inputs) != 1 || string(att.inputs[0]) != "accepted" {
		t.Fatalf("inputs = %q, want accepted only", att.inputs)
	}
	att.mu.Unlock()
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte{0, 1, 2}); //nolint:staticcheck
	err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.Read(context.Background())                      //nolint:staticcheck
	if websocket.CloseStatus(err) != websocket.StatusProtocolError { //nolint:staticcheck
		t.Fatalf("close error = %v, status=%d", err, websocket.CloseStatus(err))
	}
}

func TestRelayV1SendsCloseFrameBeforeCancellingReader(t *testing.T) { //nolint:paralleltest
	for _, tc := range []struct {
		name   string
		reason webuterminal.CloseReason
		want   websocket.StatusCode //nolint:staticcheck
	}{
		{name: "slow consumer", reason: webuterminal.CloseSlowConsumer, want: 4003},
		{name: "exited", reason: webuterminal.CloseExited, want: 4001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for attempt := 0; attempt < 20; attempt++ {
				att := newRelayTestAttachment()
				att.reason = tc.reason
				server := newRelayTestServer(t, att)
				conn, _, err := websocket.Dial(context.Background(), "ws"+server.URL[4:], &websocket.DialOptions{Subprotocols: []string{terminalV1Subprotocol}}) //nolint:staticcheck
				if err != nil {
					server.Close()
					t.Fatal(err)
				}
				if _, _, err := conn.Read(context.Background()); err != nil { //nolint:staticcheck
					conn.Close(websocket.StatusNormalClosure, "done") //nolint:staticcheck
					server.Close()
					t.Fatalf("initial read: %v", err)
				}
				close(att.out)
				_, data, err := conn.Read(context.Background()) //nolint:staticcheck
				if err != nil {
					conn.Close(websocket.StatusNormalClosure, "done") //nolint:staticcheck
					server.Close()
					t.Fatalf("close frame read: %v", err)
				}
				frame, err := proto.Decode(data)
				if err != nil || frame.Kind != proto.KindClose || frame.Reason != string(tc.reason) {
					conn.Close(websocket.StatusNormalClosure, "done") //nolint:staticcheck
					server.Close()
					t.Fatalf("close frame = %#v, err=%v", frame, err)
				}
				_, _, err = conn.Read(context.Background()) //nolint:staticcheck
				if got := websocket.CloseStatus(err); got != tc.want {
					conn.Close(websocket.StatusNormalClosure, "done") //nolint:staticcheck
					server.Close()
					t.Fatalf("attempt %d close status = %d, err=%v; want %d", attempt, got, err, tc.want)
				}
				conn.Close(websocket.StatusNormalClosure, "done") //nolint:staticcheck
				server.Close()
			}
		})
	}
}

type relayTestAttachment struct {
	initial webuterminal.TerminalInitialState
	out     chan webuterminal.TerminalEvent
	reason  webuterminal.CloseReason
	mu      sync.Mutex
	inputs  [][]byte
}

func newRelayTestAttachment() *relayTestAttachment {
	return &relayTestAttachment{out: make(chan webuterminal.TerminalEvent, 4), reason: webuterminal.CloseExited}
}
func (a *relayTestAttachment) ConnID() string                                  { return "test" }
func (a *relayTestAttachment) InitialState() webuterminal.TerminalInitialState { return a.initial }
func (a *relayTestAttachment) Output() <-chan webuterminal.TerminalEvent       { return a.out }
func (a *relayTestAttachment) WriteInput(p []byte) (int, error) {
	a.mu.Lock()
	a.inputs = append(a.inputs, append([]byte(nil), p...))
	a.mu.Unlock()
	return len(p), nil
}
func (a *relayTestAttachment) RequestResize(uint16, uint16) error    { return nil }
func (a *relayTestAttachment) Focus() error                          { return nil }
func (a *relayTestAttachment) CloseReason() webuterminal.CloseReason { return a.reason }

type relayTestSource struct{ att *relayTestAttachment }

func (s *relayTestSource) AttachSession(webuterminal.SessionKey, uint16, uint16, *webuterminal.LaunchSpec) (webuterminal.Attachment, bool, error) {
	return s.att, false, nil
}
func (s *relayTestSource) Detach(webuterminal.SessionKey, string)      {}
func (s *relayTestSource) Kill(webuterminal.SessionKey) error          { return nil }
func (s *relayTestSource) HasSession(webuterminal.SessionKey) bool     { return true }
func (s *relayTestSource) SessionClosed(webuterminal.SessionKey) bool  { return false }
func (s *relayTestSource) AttachmentCount(webuterminal.SessionKey) int { return 1 }
func (s *relayTestSource) SessionCount() int                           { return 1 }
func (s *relayTestSource) SessionCountFor(string) int                  { return 1 }
func (s *relayTestSource) MaxSessions() int                            { return 10 }

func newRelayTestServer(t *testing.T, att *relayTestAttachment) *httptest.Server {
	t.Helper()
	source := &relayTestSource{att: att}
	p := &terminalWSParams{manager: source}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, ok := upgradeTerminalWS(w, r, nil)
		if !ok {
			return
		}
		_, _ = runTerminalRelayV1(r.Context(), conn, p, webuterminal.SessionKey{Name: "test"}, att)
	}))
}
