package codex

import (
	"encoding/json"
	"errors"
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
