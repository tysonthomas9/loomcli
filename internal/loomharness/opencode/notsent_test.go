package opencode

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestCallNotSentWhenNoConnection: a call that cannot connect never reached
// OpenCode, so its error says it was not sent.
func TestCallNotSentWhenNoConnection(t *testing.T) {
	down := NewClient("http://127.0.0.1:1", "pw")
	err := down.call(context.Background(), "POST", "/permission/per_x/reply", map[string]string{"decision": "once"}, nil)
	if !errors.Is(err, loomharness.ErrNotSent) || !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("connection refused = %v; want ErrNotSent and ErrUnavailable", err)
	}
}
