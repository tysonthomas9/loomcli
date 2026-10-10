package claude

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestReplyNotSent: Claude has no permission tool yet, so a Reply sends
// nothing and says so.
func TestReplyNotSent(t *testing.T) {
	err := New(Config{}).Session(loomharness.NativeRef{NativeID: "s1"}).Reply(context.Background(), "a1", loomharness.Reply{Allow: true})
	if !errors.Is(err, loomharness.ErrNotSent) {
		t.Fatalf("Reply = %v; want ErrNotSent", err)
	}
}
