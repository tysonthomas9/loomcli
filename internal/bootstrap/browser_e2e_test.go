//go:build browser_e2e

package bootstrap_test

// Cross-repository proof: a real fleet-db binary (FLEET_DB_BIN, built from the
// paired fleet-db branch) started as Loom's embedded FleetDB, verifying
// delegations minted by Loom's local key, driven through Loom's real browser
// handlers and FleetDB adapter.
//
//	FLEET_DB_BIN=/path/to/fleet-db go test -tags browser_e2e -run BrowserE2E ./internal/bootstrap/

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/browserauth"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/browsers"
)

func TestBrowserE2E_RealFleetDB(t *testing.T) {
	if os.Getenv(bootstrap.EnvFleetDBBin) == "" {
		t.Skip("FLEET_DB_BIN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dataDir, err := os.MkdirTemp("", "lbe2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	emb, err := bootstrap.StartEmbedded(ctx, dataDir, logger)
	if err != nil {
		t.Fatalf("StartEmbedded: %v", err)
	}
	t.Cleanup(func() { _ = emb.Stop() })
	client, err := fleetdb.New(fleetdb.Config{BaseURL: emb.URL(), Actor: "loom-e2e"})
	if err != nil {
		t.Fatal(err)
	}

	for _, ws := range []string{"KB", "KB2"} {
		if _, err := client.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("workspace %s: %v", ws, err)
		}
	}
	mustRole := func(ws, name, kind string) {
		if _, err := client.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: ws, Name: name, Kind: kind}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("role %s/%s: %v", ws, name, err)
		}
	}
	mustAgent := func(ws, name, role string) {
		if _, err := client.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: ws, Name: name, RoleName: role}); err != nil {
			t.Fatalf("agent %s/%s: %v", ws, name, err)
		}
	}
	mustRole("KB", "lead", "")
	mustRole("KB", "pair", "interactive")
	mustRole("KB", "coder", "worker")
	mustAgent("KB", "lead", "lead")
	mustAgent("KB", "pairer", "pair")
	mustAgent("KB", "bob", "coder")

	kp, err := browserauth.LoadOrCreateLocalKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := browserauth.LocalSigner(kp)
	if err != nil {
		t.Fatal(err)
	}
	agents := browserauth.NewAgentSessionRegistry()
	ops := browserauth.NewOperatorSessionRegistry()
	mod := browsers.NewModule(browsers.Config{
		Store: client, Backend: client.Browsers(), Signer: signer,
		AgentSessions: agents, OperatorSessions: ops, Logger: logger,
	})
	mux := http.NewServeMux()
	mod.Register(mux)
	mod.RegisterAgentRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	call := func(method, path, header, token string, body any) (int, map[string]any, []byte) {
		t.Helper()
		var rdr io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		}
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, rdr)
		if header != "" {
			req.Header.Set(header, token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return resp.StatusCode, m, raw
	}
	const agentH = browserauth.AgentSessionHeader
	const opH = browserauth.OperatorSessionHeader

	leadTok, _, _ := agents.Issue("KB", "lead", "orch", "lead-term")
	pairTok, _, _ := agents.Issue("KB", "pairer", "orch", "pair-term")
	bobTok, _, _ := agents.Issue("KB", "bob", "orch", "bob-term")

	// Agent create → Starting, owner from the session, created_by = sub.
	st, b1, raw := call(http.MethodPost, "/api/agent/browsers", agentH, leadTok, map[string]string{"name": "Docs", "request_id": "req-1"})
	if st != http.StatusCreated || b1["owner_agent_id"] != "lead" || b1["status"] != "starting" || b1["created_by"] != "agent:lead" || b1["selected"] != true {
		t.Fatalf("create = %d %s", st, raw)
	}
	// Idempotent replay returns the same browser; conflicting reuse is 409.
	st, again, _ := call(http.MethodPost, "/api/agent/browsers", agentH, leadTok, map[string]string{"name": "Docs", "request_id": "req-1"})
	if st != http.StatusCreated && st != http.StatusOK || again["id"] != b1["id"] {
		t.Fatalf("replay = %d %v", st, again)
	}
	if st, _, raw := call(http.MethodPost, "/api/agent/browsers", agentH, leadTok, map[string]string{"name": "Other", "request_id": "req-1"}); st != http.StatusConflict {
		t.Fatalf("conflicting replay = %d %s", st, raw)
	}
	st, b2, _ := call(http.MethodPost, "/api/agent/browsers", agentH, leadTok, map[string]string{"name": "Second", "request_id": "req-2"})
	if st != http.StatusCreated {
		t.Fatalf("second create = %d", st)
	}

	// State lists both; only one is selected.
	st, state, raw := call(http.MethodGet, "/api/agent/browsers", agentH, leadTok, nil)
	list, _ := state["browsers"].([]any)
	if st != http.StatusOK || len(list) != 2 {
		t.Fatalf("state = %d %s", st, raw)
	}

	// Isolation: another interactive agent sees none of lead's browsers and
	// cannot read or select them; a claimed owner is rejected.
	_, pstate, _ := call(http.MethodGet, "/api/agent/browsers", agentH, pairTok, nil)
	if pl, _ := pstate["browsers"].([]any); len(pl) != 0 {
		t.Fatalf("pairer sees lead's browsers: %v", pstate)
	}
	if st, _, _ := call(http.MethodGet, "/api/agent/browsers/"+b1["id"].(string), agentH, pairTok, nil); st != http.StatusNotFound {
		t.Fatalf("cross-owner get = %d", st)
	}
	if st, _, _ := call(http.MethodPost, "/api/agent/browsers/"+b1["id"].(string)+"/select", agentH, pairTok, nil); st != http.StatusNotFound {
		t.Fatalf("cross-owner select = %d", st)
	}
	if st, _, _ := call(http.MethodPost, "/api/agent/browsers", agentH, pairTok, map[string]string{"name": "x", "request_id": "r", "owner_agent_id": "lead"}); st != http.StatusForbidden {
		t.Fatalf("claimed owner = %d", st)
	}
	if st, _, _ := call(http.MethodGet, "/api/agent/browsers", agentH, bobTok, nil); st != http.StatusNotFound {
		t.Fatalf("worker agent = %d", st)
	}
	if st, _, _ := call(http.MethodGet, "/api/agent/browsers", "X-Actor", "lead", nil); st != http.StatusUnauthorized {
		t.Fatalf("no session = %d", st)
	}

	// Local desktop operator: list and select lead's browsers after lead's
	// agent session ended; cannot use another workspace.
	agents.RevokeToken(leadTok)
	if st, _, _ := call(http.MethodGet, "/api/agent/browsers", agentH, leadTok, nil); st != http.StatusUnauthorized {
		t.Fatalf("revoked agent session = %d", st)
	}
	opTok, _, _ := ops.Issue("KB", uint32(os.Getuid())) //nolint:gosec
	st, olist, raw := call(http.MethodGet, "/api/workspaces/KB/agents/lead/browsers", opH, opTok, nil)
	if l, _ := olist["browsers"].([]any); st != http.StatusOK || len(l) != 2 {
		t.Fatalf("operator list = %d %s", st, raw)
	}
	st, sel, raw := call(http.MethodPost, "/api/workspaces/KB/agents/lead/browsers/"+b1["id"].(string)+"/select", opH, opTok, nil)
	if st != http.StatusOK || sel["selected"] != true {
		t.Fatalf("operator select = %d %s", st, raw)
	}
	_, after, _ := call(http.MethodGet, "/api/workspaces/KB/agents/lead/browsers/"+b2["id"].(string), opH, opTok, nil)
	if after["selected"] != false {
		t.Fatalf("previous selection kept: %v", after)
	}
	if st, _, _ := call(http.MethodGet, "/api/workspaces/KB2/agents/lead/browsers", opH, opTok, nil); st != http.StatusForbidden {
		t.Fatalf("cross-workspace operator = %d", st)
	}
	if st, _, _ := call(http.MethodGet, "/api/workspaces/KB/agents/lead/browsers", "", "", nil); st != http.StatusUnauthorized {
		t.Fatalf("operator without session = %d", st)
	}

	// FleetDB itself refuses Loom-less or forged calls: no delegation, a
	// delegation for another owner used on a different workspace, and an
	// operator-minted create.
	raw2, _ := http.NewRequestWithContext(ctx, http.MethodGet, emb.URL()+"/api/v1/KB/browsers", nil)
	raw2.Header.Set("X-Actor", "lead")
	if resp, err := http.DefaultClient.Do(raw2); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("direct FleetDB without delegation = %v %v", resp.StatusCode, err)
	}
	d, err := signer.Mint("KB", "lead", browserauth.Principal{Kind: domain.BrowserPrincipalAgentSession, Subject: "agent:lead", SessionID: "abs_x"}, domain.BrowserOpList)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Browsers().List(ctx, "KB2", d); !errors.Is(err, domain.ErrBrowserUnauthorized) {
		t.Fatalf("delegation replayed on another workspace: %v", err)
	}
	if _, err := client.Browsers().Create(ctx, "KB", d, domain.BrowserCreate{Name: "x", RequestID: "y"}); !errors.Is(err, domain.ErrBrowserUnauthorized) && !errors.Is(err, domain.ErrBrowserForbidden) {
		t.Fatalf("list-only delegation used to create: %v", err)
	}
	other, _ := browserauth.LoadOrCreateLocalKey(t.TempDir())
	forger, _ := browserauth.LocalSigner(other)
	fd, _ := forger.Mint("KB", "lead", browserauth.Principal{Kind: domain.BrowserPrincipalAgentSession, Subject: "agent:lead", SessionID: "abs_x"}, domain.BrowserOpList)
	if _, err := client.Browsers().List(ctx, "KB", fd); !errors.Is(err, domain.ErrBrowserUnauthorized) {
		t.Fatalf("foreign-key delegation accepted: %v", err)
	}

	// Durability: a fresh fleet-db process over the same data dir still has the records.
	if err := emb.Stop(); err != nil && !strings.Contains(err.Error(), "signal") {
		t.Logf("stop: %v", err)
	}
	emb2, err := bootstrap.StartEmbedded(ctx, dataDir, logger)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	t.Cleanup(func() { _ = emb2.Stop() })
	client2, _ := fleetdb.New(fleetdb.Config{BaseURL: emb2.URL(), Actor: "loom-e2e"})
	d2, _ := signer.Mint("KB", "lead", browserauth.Principal{Kind: domain.BrowserPrincipalAgentSession, Subject: "agent:lead", SessionID: "abs_y"}, domain.BrowserOpList)
	got, err := client2.Browsers().List(ctx, "KB", d2)
	if err != nil || len(got) != 2 {
		t.Fatalf("after restart = %v %v", got, err)
	}
	selected := 0
	for _, b := range got {
		if b.Selected {
			selected++
			if b.ID != b1["id"] {
				t.Fatalf("wrong selection after restart: %+v", b)
			}
		}
	}
	if selected != 1 {
		t.Fatalf("selected count = %d", selected)
	}
}
