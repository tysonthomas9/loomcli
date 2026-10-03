package agentsv1

import (
	"context"
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

// probed is a fake harness with a capability probe.
type probed struct {
	*fake.Harness
	caps loomharness.Capabilities
	ok   bool
}

func (p *probed) Capabilities() (loomharness.Capabilities, bool) { return p.caps, p.ok }

func harnessServer(t *testing.T, hs map[string]loomharness.Harness) *httptest.Server {
	t.Helper()
	st, err := loomstore.Open(context.Background(), filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws", Harnesses: hs})
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
}
