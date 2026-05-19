package terminal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"nhooyr.io/websocket" //nolint:staticcheck

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

func newTabMetaStoreForWSTest(t *testing.T) *tabmeta.Store {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return tabmeta.NewStore(rdb, nil)
}

func TestLaunchSpecRejectsUUIDSessionWithoutMetadata(t *testing.T) {
	ctx := context.Background()
	p := &terminalWSParams{tabMetaStore: newTabMetaStoreForWSTest(t)}

	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "term_550e8400-e29b-41d4-a716-446655440000")
	if launch != nil {
		t.Fatalf("launch = %#v, want nil", launch)
	}
	if !errors.Is(err, errTerminalLaunchMetaMissing) {
		t.Fatalf("err = %v, want errTerminalLaunchMetaMissing", err)
	}
}

func TestLaunchSpecRejectsUUIDSessionWithoutTabStore(t *testing.T) {
	ctx := context.Background()
	p := &terminalWSParams{}

	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "term_550e8400-e29b-41d4-a716-446655440000")
	if launch != nil {
		t.Fatalf("launch = %#v, want nil", launch)
	}
	if !errors.Is(err, errTerminalLaunchMetaMissing) {
		t.Fatalf("err = %v, want errTerminalLaunchMetaMissing", err)
	}
}

func TestLaunchSpecKeepsLegacyNamedLeadTabs(t *testing.T) {
	ctx := context.Background()
	p := &terminalWSParams{tabMetaStore: newTabMetaStoreForWSTest(t)}

	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "lead-codex-1")
	if err != nil {
		t.Fatalf("launchSpecForTerminalSession: %v", err)
	}
	if launch == nil || len(launch.Argv) == 0 {
		t.Fatalf("launch = %#v, want legacy lead argv", launch)
	}
}

func TestLaunchSpecUsesTabMetadataBranches(t *testing.T) {
	ctx := context.Background()
	store := newTabMetaStoreForWSTest(t)
	now := time.Now().UTC()
	p := &terminalWSParams{tabMetaStore: store}

	for _, meta := range []*tabmeta.TabMetadata{
		{Workspace: "E2E", SessionName: "agent_missing", Kind: "agent", CreatedAt: now, UpdatedAt: now},
		{Workspace: "E2E", SessionName: "agent_env_only", Kind: "agent", Launch: &tabmeta.LaunchSpec{Env: map[string]string{"A": "B"}}, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.Set(ctx, meta); err != nil {
			t.Fatalf("set %s: %v", meta.SessionName, err)
		}
		launch, err := launchSpecForTerminalSession(ctx, p, "E2E", meta.SessionName)
		if launch != nil || !errors.Is(err, errAgentLaunchSpecMissing) {
			t.Fatalf("%s launch=%#v err=%v, want agent launch missing", meta.SessionName, launch, err)
		}
	}

	if err := store.Set(ctx, &tabmeta.TabMetadata{
		Workspace:   "E2E",
		SessionName: "agent_valid",
		Kind:        "agent",
		Launch:      &tabmeta.LaunchSpec{Argv: []string{"loom", "agent"}},
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("set agent_valid: %v", err)
	}
	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "agent_valid")
	if err != nil || launch == nil || strings.Join(launch.Argv, " ") != "loom agent" {
		t.Fatalf("agent_valid launch=%#v err=%v", launch, err)
	}

	if err := store.Set(ctx, &tabmeta.TabMetadata{
		Workspace:   "E2E",
		SessionName: "shell_env",
		Kind:        "shell",
		Launch:      &tabmeta.LaunchSpec{Env: map[string]string{"A": "B"}},
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("set shell_env: %v", err)
	}
	launch, err = launchSpecForTerminalSession(ctx, p, "E2E", "shell_env")
	if err != nil || launch == nil || launch.Env["A"] != "B" {
		t.Fatalf("shell_env launch=%#v err=%v", launch, err)
	}
}

func TestMaybeEmitStaleRestartBanner(t *testing.T) {
	ctx := context.Background()
	store := newTabMetaStoreForWSTest(t)
	startedAt := time.Now().UTC()
	if err := store.Set(ctx, &tabmeta.TabMetadata{
		Workspace:   "E2E",
		SessionName: "main",
		CreatedAt:   startedAt.Add(-time.Hour),
		UpdatedAt:   startedAt.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("set metadata: %v", err)
	}

	p := &terminalWSParams{tabMetaStore: store, serverStartedAt: startedAt}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil) //nolint:staticcheck
		if err != nil {
			t.Errorf("Accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done") //nolint:staticcheck
		maybeEmitStaleRestartBanner(r.Context(), conn, p, "E2E", "main")
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil) //nolint:staticcheck
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done") //nolint:staticcheck

	msgType, data, err := conn.Read(ctx) //nolint:staticcheck
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msgType != websocket.MessageBinary || !strings.Contains(string(data), "Previous shell did not survive") { //nolint:staticcheck
		t.Fatalf("banner type=%v data=%q", msgType, data)
	}
}
