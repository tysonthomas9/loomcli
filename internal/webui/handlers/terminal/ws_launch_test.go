package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"nhooyr.io/websocket"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
)

type terminalAttachmentStub struct{}

func (terminalAttachmentStub) ConnID() string                            { return "conn-1" }
func (terminalAttachmentStub) Output() <-chan []byte                     { return make(chan []byte) }
func (terminalAttachmentStub) WriteInput(value []byte) (int, error)      { return len(value), nil }
func (terminalAttachmentStub) Replay() []interaction.TerminalReplayEvent { return nil }
func (terminalAttachmentStub) Resize(string, uint16, uint16) error       { return nil }
func (terminalAttachmentStub) ExitReason() string                        { return "" }

func TestTerminalReplayFramesPreserveResizeOutputOrder(t *testing.T) {
	frames, err := terminalReplayFrames([]interaction.TerminalReplayEvent{
		{Columns: 80, Rows: 24},
		{Output: []byte("before")},
		{Columns: 132, Rows: 41},
		{Output: []byte("after")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 5 || frames[0].messageType != websocket.MessageBinary ||
		!bytes.Equal(frames[0].payload, []byte("\x1b[3J\x1b[2J\x1b[H")) {
		t.Fatalf("replay frames = %#v", frames)
	}
	if frames[1].messageType != websocket.MessageText ||
		frames[2].messageType != websocket.MessageBinary || string(frames[2].payload) != "before" ||
		frames[3].messageType != websocket.MessageText ||
		frames[4].messageType != websocket.MessageBinary || string(frames[4].payload) != "after" {
		t.Fatalf("replay frame ordering = %#v", frames)
	}
	var first loomapi.TerminalReplayControl
	if err := json.Unmarshal(frames[1].payload, &first); err != nil {
		t.Fatal(err)
	}
	var second loomapi.TerminalReplayControl
	if err := json.Unmarshal(frames[3].payload, &second); err != nil {
		t.Fatal(err)
	}
	if first.Type != loomapi.TerminalReplayResize || first.Columns != 80 || first.Rows != 24 ||
		second.Type != loomapi.TerminalReplayResize || second.Columns != 132 || second.Rows != 41 {
		t.Fatalf("resize controls = %#v, %#v", first, second)
	}
}

type terminalConnectionStub struct {
	interaction.TerminalTabs
	command interaction.TerminalAttachCommand
	result  *interaction.TerminalAttachResult
	err     error
}

func (stub *terminalConnectionStub) AttachTerminal(
	_ context.Context,
	command interaction.TerminalAttachCommand,
) (*interaction.TerminalAttachResult, error) {
	stub.command = command
	return stub.result, stub.err
}

func TestWebSocketAttachDelegatesWholeTransactionToInteraction(t *testing.T) {
	stub := &terminalConnectionStub{result: &interaction.TerminalAttachResult{
		Attachment: terminalAttachmentStub{}, Reattached: true,
	}}
	params := &terminalWSParams{terminals: stub}
	operator := &authority.OperatorAuthority{}
	attachment, reattached, err := attachTerminalSession(
		t.Context(), params,
		interaction.TerminalKey{WorkspaceKey: "WS", TerminalID: "term_reviewer"},
		132, 41, operator,
	)
	if err != nil {
		t.Fatal(err)
	}
	if attachment == nil || !reattached {
		t.Fatalf("attach result = %#v, reattached = %v", attachment, reattached)
	}
	if stub.command.WorkspaceKey != "WS" || stub.command.TerminalID != "term_reviewer" ||
		stub.command.Columns != 132 || stub.command.Rows != 41 || stub.command.StartAuthority != operator {
		t.Fatalf("Interaction attach command = %#v", stub.command)
	}
}

func TestWebSocketAttachFailsClosedWithoutInteractionService(t *testing.T) {
	_, _, err := attachTerminalSession(
		t.Context(), &terminalWSParams{},
		interaction.TerminalKey{WorkspaceKey: "WS", TerminalID: "term_reviewer"},
		80, 24,
	)
	if !errors.Is(err, interaction.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

func TestClassifyAttachErrorUsesSafeInteractionTerminalMessage(t *testing.T) {
	err := errors.Join(interaction.ErrAgentTerminalWorker, errors.New("private adapter detail"))
	status, message := classifyAttachErr(err, "term_worker", "WS")
	if status != websocket.StatusPolicyViolation {
		t.Fatalf("status = %v", status)
	}
	if message == "private adapter detail" || message == err.Error() {
		t.Fatalf("unsafe websocket message = %q", message)
	}
}
