//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestExecutorRunOnceMintsRunTokenAtClaim drives the real claim path against
// memstore with an injected recording runner (no workflow process is ever
// spawned — the loomExecutablePath fork-bomb lesson) and verifies the token
// minted at claim time is bound to the claimed lease + fence with the locked
// TTL semantics (max run duration: default 24h, LOOM_RUN_TOKEN_TTL knob,
// invalid knob degrades to the default instead of failing the run).
func TestExecutorRunOnceMintsRunTokenAtClaim(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	cases := []struct {
		name      string
		key       []byte
		ttlEnv    string
		wantToken bool
		wantTTL   time.Duration
	}{
		{name: "default ttl is max run duration", key: key, ttlEnv: "", wantToken: true, wantTTL: DefaultRunTokenTTL},
		{name: "ttl env knob overrides", key: key, ttlEnv: "45m", wantToken: true, wantTTL: 45 * time.Minute},
		{name: "invalid ttl env degrades to default", key: key, ttlEnv: "not-a-duration", wantToken: true, wantTTL: DefaultRunTokenTTL},
		{name: "nil key disables minting", key: nil, ttlEnv: "", wantToken: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(RunTokenTTLEnv, tc.ttlEnv)
			runner, claimed := runOnceWithRunTokenKey(t, tc.key)
			if !tc.wantToken {
				if runner.req.RunToken != "" {
					t.Fatalf("RunToken = %q, want empty without RunTokenKey", runner.req.RunToken)
				}
				return
			}
			if runner.req.RunToken == "" {
				t.Fatal("RunToken empty, want minted token")
			}
			claims, err := ParseRunToken(runner.req.RunToken, tc.key)
			if err != nil {
				t.Fatalf("ParseRunToken: %v", err)
			}
			assertRunTokenBoundToClaim(t, claims, claimed)
			if got := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time); got != tc.wantTTL {
				t.Fatalf("token ttl = %s, want %s", got, tc.wantTTL)
			}
		})
	}
}

// runOnceWithRunTokenKey registers a Flue driver, queues one run and executes
// RunOnce with a recording runner, returning the runner and the claimed run.
func runOnceWithRunTokenKey(t *testing.T, key []byte) (*recordingRunner, *domain.DriverRun) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1"}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	runner := &recordingRunner{result: RunResult{Status: domain.DriverRunCompleted, Summary: "driver completed"}}
	result, err := (&Executor{
		Store:             st,
		WorkspaceKey:      "TEST",
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		Runner:            runner,
		RunTokenKey:       key,
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if result.Claimed == nil || result.Claimed.LeaseID != "lease-1" || result.Claimed.FencingToken == 0 {
		t.Fatalf("claimed = %+v, want lease-1 with fencing token", result.Claimed)
	}
	return runner, result.Claimed
}

func assertRunTokenBoundToClaim(t *testing.T, claims *RunTokenClaims, claimed *domain.DriverRun) {
	t.Helper()
	if claims.WorkspaceKey != claimed.WorkspaceKey || claims.RunID != claimed.RunID {
		t.Fatalf("token run identity = %s/%s, want %s/%s", claims.WorkspaceKey, claims.RunID, claimed.WorkspaceKey, claimed.RunID)
	}
	if claims.NodeID != claimed.NodeID || claims.LeaseID != claimed.LeaseID || claims.FencingToken != claimed.FencingToken {
		t.Fatalf("token lease binding = %s/%s/%d, want %s/%s/%d",
			claims.NodeID, claims.LeaseID, claims.FencingToken, claimed.NodeID, claimed.LeaseID, claimed.FencingToken)
	}
	if len(claims.Caps) != 0 {
		t.Fatalf("token caps = %v, want reserved-but-empty", claims.Caps)
	}
}

// TestFlueRuntimeEnvInjectsRunToken verifies the env seam: the per-run token
// rides RunRequest into LOOM_RUN_TOKEN, no token env appears without a mint,
// and any inherited LOOM_RUN_TOKEN* parent vars (TOKEN fragment) are filtered
// so a workflow only ever sees the token minted for its own run.
func TestFlueRuntimeEnvInjectsRunToken(t *testing.T) {
	t.Setenv("LOOM_RUN_TOKEN", "stale-parent-token")
	t.Setenv(RunTokenSigningKeyEnv, "deadbeef")
	cases := []struct {
		name     string
		runToken string
		want     string
	}{
		{name: "minted token exported", runToken: "minted.jwt.token", want: "minted.jwt.token"},
		{name: "no mint no token env", runToken: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := flueRuntimeEnv(RunRequest{
				Run: &domain.DriverRun{
					WorkspaceKey: "TEST",
					RunID:        "run-1",
					NodeID:       "node-1",
					LeaseID:      "lease-1",
					FencingToken: 42,
				},
				BundleRoot: "/tmp/bundle",
				ServerPath: "/tmp/bundle/dist/server.mjs",
				Manifest:   map[string]string{"workflow_name": "epic-runner"},
				RunToken:   tc.runToken,
			}, []byte(`{}`), nil)
			if err != nil {
				t.Fatalf("flueRuntimeEnv: %v", err)
			}
			got := envMap(env)
			if got["LOOM_RUN_TOKEN"] != tc.want {
				t.Fatalf("LOOM_RUN_TOKEN = %q, want %q", got["LOOM_RUN_TOKEN"], tc.want)
			}
			if _, leaked := got[RunTokenSigningKeyEnv]; leaked {
				t.Fatalf("%s leaked into workflow runtime env", RunTokenSigningKeyEnv)
			}
		})
	}
}
