package terminal

import (
	"context"

	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// interimAttachmentAdapter keeps the raw pre-v1 WebSocket relay compiling
// between transport Slice A and protocol Slice B. It forwards only terminal
// output bytes; sequenced resize, notice, and close frames begin in Slice B.
type interimAttachmentAdapter struct {
	att    webuterminal.Attachment
	output chan []byte
}

func newInterimAttachmentAdapter(ctx context.Context, att webuterminal.Attachment) *interimAttachmentAdapter {
	a := &interimAttachmentAdapter{att: att, output: make(chan []byte)}
	go a.pump(ctx)
	return a
}

func (a *interimAttachmentAdapter) pump(ctx context.Context) {
	defer close(a.output)
	for event := range a.att.Output() {
		if event.Kind != webuterminal.EventOutput {
			continue
		}
		select {
		case a.output <- event.Data:
		case <-ctx.Done():
			return
		}
	}
}

func (a *interimAttachmentAdapter) Output() <-chan []byte { return a.output }
func (a *interimAttachmentAdapter) ExitReason() string {
	return string(a.att.CloseReason())
}
func (a *interimAttachmentAdapter) Write(p []byte) (int, error) {
	return a.att.WriteInput(p)
}
func (a *interimAttachmentAdapter) Resize(_ string, cols, rows uint16) error {
	return a.att.RequestResize(cols, rows)
}
