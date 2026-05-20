package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestDefaultFleetHealthProbeBranches(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("path = %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer okSrv.Close()
	if err := defaultFleetHealthProbe(context.Background(), okSrv.URL+"/"); err != nil {
		t.Fatalf("healthy probe: %v", err)
	}

	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer badSrv.Close()
	if err := defaultFleetHealthProbe(context.Background(), badSrv.URL); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("bad status err = %v", err)
	}

	if err := defaultFleetHealthProbe(context.Background(), "://bad-url"); err == nil {
		t.Fatal("malformed URL probe returned nil")
	}
}

func TestFleetDBConfigReportingBranches(t *testing.T) {
	auto := reportFleetDBConfig(cfgpkg.FleetDBServerConfig{Workspace: "WS", AutoStart: true})
	if auto.Status != StatusPass || !strings.Contains(auto.Summary, "miniredis") {
		t.Fatalf("auto-start report = %+v", auto)
	}

	missingRedis := reportFleetDBConfig(cfgpkg.FleetDBServerConfig{Workspace: "WS"})
	if missingRedis.Status != StatusFail || !strings.Contains(missingRedis.Summary, "no Redis URL") {
		t.Fatalf("missing redis report = %+v", missingRedis)
	}

	unreachable := reportFleetDBConfig(cfgpkg.FleetDBServerConfig{Workspace: "WS", RedisURL: "redis://127.0.0.1:1"})
	if unreachable.Status != StatusFail || !strings.Contains(unreachable.Summary, "not reachable") {
		t.Fatalf("unreachable redis report = %+v", unreachable)
	}
}

func TestCheckTmuxVersionBranches(t *testing.T) {
	deps, _, execR, _, _ := NewTestDeps(t)
	deps.LookPath = func(name string) (string, error) {
		if name != "tmux" {
			t.Fatalf("LookPath name = %q", name)
		}
		return "/usr/bin/tmux", nil
	}
	execR.RunFunc = func(_ string, name string, args ...string) CommandResult {
		if name != "tmux" || len(args) != 1 || args[0] != "-V" {
			t.Fatalf("Exec = %s %v", name, args)
		}
		return CommandResult{Stdout: "tmux next\n"}
	}
	got := checkTmux(deps)
	if got.Status != StatusPass || got.Summary != "tmux found" {
		t.Fatalf("unparseable tmux = %+v", got)
	}

	execR.RunFunc = func(string, string, ...string) CommandResult {
		return CommandResult{Stdout: "tmux 3.4\n"}
	}
	got = checkTmux(deps)
	if got.Status != StatusPass || !strings.Contains(got.Summary, "3.4") {
		t.Fatalf("versioned tmux = %+v", got)
	}
}

func TestCheckRedisBranches(t *testing.T) {
	t.Setenv("LOOM_REDIS_ADDR", "")
	if got := checkRedis(); got != (CheckResult{}) {
		t.Fatalf("checkRedis without env = %+v, want empty result", got)
	}

	mr := miniredis.RunT(t)
	t.Setenv("LOOM_REDIS_ADDR", mr.Addr())
	t.Setenv("LOOM_REDIS_PASSWORD", "")
	if got := checkRedis(); got.Status != StatusPass || !strings.Contains(got.Summary, mr.Addr()) {
		t.Fatalf("checkRedis pass = %+v", got)
	}

	mr.Close()
	if got := checkRedis(); got.Status != StatusFail || got.Detail == "" {
		t.Fatalf("checkRedis failure = %+v", got)
	}
}
