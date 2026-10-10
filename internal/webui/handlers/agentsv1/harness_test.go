package agentsv1

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// probed is a fake harness with a capability probe; it records the dirs asked.
type probed struct {
	*fake.Harness
	caps loomharness.Capabilities
	ok   bool
	dirs []string
}

func (p *probed) Capabilities(dir string) (loomharness.Capabilities, bool) {
	p.dirs = append(p.dirs, dir)
	return p.caps, p.ok
}

func harnessServer(t *testing.T, hs map[string]loomharness.Harness) *httptest.Server {
	t.Helper()
	st, err := loomstore.Open(context.Background(), filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return serveService(t, loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws", Harnesses: hs}))
}

func serveService(t *testing.T, svc *loomagent.Service) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	ws := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}
	New(func(string) *loomagent.Service { return svc }, nil).Register(mux, ws, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestHarnessInfoRoute: a probed harness shows its account kind, label,
// commands and probe time; one not yet probed shows none; a harness without
// a probe is a 200 with capabilities_supported false; an unwired harness is
// harness_unavailable.
func TestHarnessInfoRoute(t *testing.T) {
	at := time.Date(2026, 10, 3, 23, 50, 0, 0, time.UTC)
	claude := &probed{Harness: fake.New(), ok: true, caps: loomharness.Capabilities{AccountKind: "subscription",
		AccountLabel: "Claude Max Subscription", ProbedAt: at,
		SlashCommands: []loomharness.SlashCommand{{Name: "compact", Description: "Summarize"}, {Name: "review", ArgumentHint: "<pr>"}}}}
	srv := harnessServer(t, map[string]loomharness.Harness{"opencode": fake.New(), "claude": claude, "codex": &probed{Harness: fake.New()}})

	status, out := call(t, srv, "GET", "ws/v1/harnesses/claude", "", "")
	cmds, _ := out["slash_commands"].([]any)
	if status != 200 || out["harness"] != "claude" || out["capabilities_supported"] != true || out["account_kind"] != "subscription" ||
		out["account_label"] != "Claude Max Subscription" || out["probed_at"] != "2026-10-03T23:50:00Z" || len(cmds) != 2 {
		t.Fatalf("claude = %d %v", status, out)
	}
	if c := cmds[1].(map[string]any); c["name"] != "review" || c["argument_hint"] != "<pr>" || c["description"] != nil {
		t.Fatalf("command = %v", c)
	}

	status, out = call(t, srv, "GET", "ws/v1/harnesses/codex", "", "")
	if cmds, _ := out["slash_commands"].([]any); status != 200 || out["capabilities_supported"] != true || out["probed_at"] != nil ||
		out["account_kind"] != nil || cmds == nil || len(cmds) != 0 {
		t.Fatalf("unprobed = %d %v", status, out)
	}

	status, out = call(t, srv, "GET", "ws/v1/harnesses/opencode", "", "")
	if status != 200 || out["harness"] != "opencode" || out["capabilities_supported"] != false || out["account_kind"] != nil {
		t.Fatalf("unsupported = %d %v", status, out)
	}

	status, out = call(t, srv, "GET", "ws/v1/harnesses/zz", "", "")
	want(t, "unwired harness", status, out, 503, "harness_unavailable")

	status, out = call(t, srv, "GET", "ws/v1/harnesses/claude?repo=%2Fsrc%2Fapp", "", "")
	if status != 200 || out["account_kind"] != "subscription" || len(claude.dirs) != 2 || claude.dirs[0] != "" || claude.dirs[1] != "/src/app" {
		t.Fatalf("repo probe = %d %v, dirs %q", status, out, claude.dirs)
	}
}

// TestHarnessInfoRepoChecked: ?repo= is checked as a create's repo is, and a
// refused repo is never probed.
func TestHarnessInfoRepoChecked(t *testing.T) {
	claude := &probed{Harness: fake.New(), ok: true}
	st, err := loomstore.Open(context.Background(), filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws", Harnesses: map[string]loomharness.Harness{"claude": claude},
		ResolveRepo: func(_ context.Context, _ loomagent.Target, repo string) (string, error) {
			if repo != "/ok" {
				return "", &loomagent.Error{Code: loomagent.CodePresetInvalid, Message: "not a clone"}
			}
			return "/resolved/ok", nil
		}})
	srv := serveService(t, svc)
	status, out := call(t, srv, "GET", "ws/v1/harnesses/claude?repo=relative", "", "")
	want(t, "bad repo", status, out, 400, "preset_invalid")
	if status, _ = call(t, srv, "GET", "ws/v1/harnesses/claude?repo=%2Fok", "", ""); status != 200 || len(claude.dirs) != 1 || claude.dirs[0] != "/resolved/ok" {
		t.Fatalf("good repo = %d, dirs %q", status, claude.dirs)
	}
}

// versioned is a fake harness whose Health is a real version check.
type versioned struct {
	*fake.Harness
	name, out string
}

func (v *versioned) Health(context.Context) (loomharness.Health, error) {
	vc, err := loomharness.CheckVersion(v.name, v.out)
	if err != nil {
		return loomharness.Health{Version: vc, Warning: err.Error()}, nil
	}
	return loomharness.Health{OK: true, Version: vc, Warning: vc.Warning()}, nil
}

// TestHarnessInfoHealth: the harness's health says why it is unavailable (a
// version below the minimum, with the upgrade hint) or warns, still ok, of
// one newer than tested; a current harness has no warning. Same for every
// harness.
func TestHarnessInfoHealth(t *testing.T) {
	for _, tc := range []struct{ harness, old, newer, cur string }{
		{"opencode", "2.0.1", "2.0.20", "2.0.19"},
		{"codex", "codex-cli 0.150.0", "codex-cli 0.158.0", "codex-cli 0.157.1"},
		{"claude", "2.1.0 (Claude Code)", "2.1.286 (Claude Code)", "2.1.285 (Claude Code)"},
	} {
		h := &versioned{Harness: fake.New(), name: tc.harness}
		srv := harnessServer(t, map[string]loomharness.Harness{tc.harness: h})
		get := func(out string) map[string]any {
			h.out = out
			status, body := call(t, srv, "GET", "ws/v1/harnesses/"+tc.harness, "", "")
			health, _ := body["health"].(map[string]any)
			if status != 200 || health == nil {
				t.Fatalf("%s %s = %d %v", tc.harness, out, status, body)
			}
			return health
		}
		g := loomharness.Versions[tc.harness]
		old, _ := loomharness.ParseVersion(tc.old)
		if got := get(tc.old); got["ok"] != false || got["warning"] != fmt.Sprintf(
			"harness_too_old: %s %s is below the minimum %s; upgrade %s", tc.harness, old, g.Minimum, tc.harness) {
			t.Fatalf("%s too old = %v", tc.harness, got)
		}
		newer, _ := loomharness.ParseVersion(tc.newer)
		if got := get(tc.newer); got["ok"] != true || got["warning"] != fmt.Sprintf(
			"%s %s is newer than the last tested %s", tc.harness, newer, g.Tested) {
			t.Fatalf("%s newer = %v", tc.harness, got)
		}
		if got := get(tc.cur); got["ok"] != true || got["warning"] != nil {
			t.Fatalf("%s current = %v", tc.harness, got)
		}
	}
}
