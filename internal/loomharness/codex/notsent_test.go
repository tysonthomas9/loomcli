package codex

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestRespondNotSentOnEndedConn: an answer on a connection that has already
// ended is never written, so its error says it was not sent.
func TestRespondNotSentOnEndedConn(t *testing.T) {
	c, _ := newPair(t, nil)
	_ = c.Close()
	if err := c.Respond(json.RawMessage(`7`), map[string]string{"decision": "accept"}); !errors.Is(err, loomharness.ErrNotSent) {
		t.Fatalf("Respond on an ended connection = %v; want ErrNotSent", err)
	}
}

// halfWriter takes n bytes of a write, then fails as a pipe closed meanwhile.
type halfWriter struct{ n int }

func (w halfWriter) Write(p []byte) (int, error) { return min(w.n, len(p)), io.ErrClosedPipe }
func (halfWriter) Close() error                  { return nil }

// TestRespondPartlyWrittenMaySend: a write that fails after some bytes went
// out may have reached codex, so it is not marked not sent; one that wrote
// nothing is.
func TestRespondPartlyWrittenMaySend(t *testing.T) {
	for n, want := range map[int]bool{0: true, 5: false} {
		c := NewConn(strings.NewReader(""), halfWriter{n}, nil)
		err := c.Respond(json.RawMessage(`7`), map[string]string{"decision": "accept"})
		if err == nil || errors.Is(err, loomharness.ErrNotSent) != want {
			t.Errorf("%d bytes written: %v; want ErrNotSent %v", n, err, want)
		}
	}
}
