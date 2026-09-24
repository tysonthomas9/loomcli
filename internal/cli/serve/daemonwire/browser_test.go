package daemonwire

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestBrowserOperatorGrantResolver(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	alice := middleware.UserIdentity{UserID: "alice"}

	t.Run("unset denies all remote operators", func(t *testing.T) {
		if r := browserOperatorGrantResolver("", "", logger); r != nil {
			t.Fatal("resolver built with no grants")
		}
	})
	t.Run("removed global allowlist is not honored", func(t *testing.T) {
		if r := browserOperatorGrantResolver("", "alice", logger); r != nil {
			t.Fatal("workspace-blind subject allowlist still grants access")
		}
	})
	t.Run("malformed grants fail closed", func(t *testing.T) {
		if r := browserOperatorGrantResolver("alice,bob", "", logger); r != nil {
			t.Fatal("malformed grants produced a resolver")
		}
	})
	t.Run("grant is limited to its workspace", func(t *testing.T) {
		r := browserOperatorGrantResolver(`[{"workspace":"ws","subjects":["alice"]}]`, "", logger)
		if r == nil {
			t.Fatal("valid grants produced no resolver")
		}
		ctx := context.Background()
		if err := r(ctx, "ws", alice, "lead", domain.BrowserOpList); err != nil {
			t.Fatalf("granted workspace denied: %v", err)
		}
		if err := r(ctx, "other", alice, "lead", domain.BrowserOpList); err == nil {
			t.Fatal("grant leaked to another workspace")
		}
	})
}
