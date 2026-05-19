package appstores

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestWrapperConstructors(t *testing.T) {
	hub := NewHub()
	if hub == nil {
		t.Fatal("NewHub returned nil")
	}
	auth, err := NewTerminalAuth()
	if err != nil || auth == nil {
		t.Fatalf("NewTerminalAuth = %#v err=%v", auth, err)
	}
	tokens, err := NewTokenStore()
	if err != nil || tokens == nil {
		t.Fatalf("NewTokenStore = %#v err=%v", tokens, err)
	}
	if GetMutationsSinceFn(nil) != nil {
		t.Fatal("GetMutationsSinceFn(nil) returned non-nil")
	}
	if err := ValidateIssueID("ISSUE-1"); err != nil {
		t.Fatalf("ValidateIssueID valid error = %v", err)
	}
	if err := ValidateIssueID("../bad"); err == nil {
		t.Fatal("ValidateIssueID invalid error = nil")
	}
}

func TestNewSubscriptionModuleAndMultiSub(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := realtime.NewHub()
	sub := NewMultiSub(ctx, hub, slog.Default())
	if sub == nil {
		t.Fatal("NewMultiSub returned nil")
	}
	if GetMutationsSinceFn(sub) == nil {
		t.Fatal("GetMutationsSinceFn returned nil for subscriber")
	}

	module := NewSubscriptionModule(
		hub,
		func(string, string) []rpc.MutationEvent { return nil },
		func(context.Context) string { return "WS" },
		func(context.Context, string) {},
		nil,
	)
	if module == nil {
		t.Fatal("NewSubscriptionModule returned nil")
	}
	mux := http.NewServeMux()
	module.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/events/token", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("token route status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRedisStoreInitializersReturnStoresAndCleanup(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	redisCfg := &fleet.RedisConfig{Address: "127.0.0.1:1"}

	tabStore, tabCleanup := InitTabMeta(ctx, redisCfg, logger)
	if tabStore == nil || tabCleanup == nil {
		t.Fatalf("InitTabMeta store=%#v cleanup nil=%t", tabStore, tabCleanup == nil)
	}
	tabCleanup()

	issueTabs, issueCleanup := InitIssueTabs(ctx, redisCfg, "WS", logger)
	if issueTabs == nil || issueCleanup == nil {
		t.Fatalf("InitIssueTabs store=%#v cleanup nil=%t", issueTabs, issueCleanup == nil)
	}
	issueCleanup()

	history, historyCleanup := InitSessionHistory(ctx, redisCfg, "WS", logger)
	if history == nil || historyCleanup == nil {
		t.Fatalf("InitSessionHistory store=%#v cleanup nil=%t", history, historyCleanup == nil)
	}
	historyCleanup()
}
