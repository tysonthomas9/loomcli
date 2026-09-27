//go:build daemon_bugreplay

// Bug-replay fault tests (restart group, config/boot class) for the fleet-db
// client. See internal/cli/daemon/supervisor/bugreplay_restart_test.go.
package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// #555: an older fleet-db that does not serve the skill-lease routes answers
// with a bare 404/405. The client maps a bare 404 to ErrNotFound, so agent
// startup fails instead of treating the lease store as unavailable.
// Root cause: client.go:453-454 a bare 404 maps to ErrNotFound.
func TestBugReplay_PR555_MissingSkillLeaseRouteMeansStoreUnavailable(t *testing.T) {
	path := "/api/v1/WS1/skill-materialization-leases"
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		err := classifyHTTPError(http.MethodPost, path, status, nil)
		if !errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
			t.Errorf("HTTP %d with no body on %s = %v; want ErrSkillMaterializationLeaseStoreUnavailable", status, path, err)
		}
	}
}

// Regression (passes on v5): a bare 404 on an ordinary resource is still
// ErrNotFound — the #555 fix must stay scoped to the lease routes.
func TestBugReplay_PR555_OtherRoutes404StillNotFound(t *testing.T) {
	err := classifyHTTPError(http.MethodGet, "/api/v1/WS1/issues/loom-1", http.StatusNotFound, nil)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bare 404 on an issue route = %v, want ErrNotFound", err)
	}
}

// F36 (#436, fixed at ab6899abd) regression: each loom-only restart_policy
// field, set on its own, must reach the fleet-db upsert wire. Before the fix
// fleetRestartPolicyWire and hasRestartPolicy carried only the basic fields,
// so these settings were silently dropped.
func TestBugReplay_F36_LoomOnlyRestartPolicyFieldsSurviveWire(t *testing.T) {
	iv := func(n int) *int { return &n }
	bv := func(b bool) *bool { return &b }
	cases := map[string]domain.RestartPolicy{
		"output_timeout":      {OutputTimeout: iv(600)},
		"rate_limit_backoff":  {RateLimitBackoff: iv(31)},
		"rate_limit_max_wait": {RateLimitMaxWait: iv(301)},
		"rate_limit_no_count": {RateLimitNoCount: bv(false)},
		"timeout_backoff":     {TimeoutBackoff: iv(7)},
		"no_work_backoff":     {NoWorkBackoff: iv(45)},
		"idle_poll_interval":  {IdlePollInterval: iv(20)},
		"yield_timeout":       {YieldTimeout: iv(90)},
		"sigterm_timeout":     {SigtermTimeout: iv(120)},
	}
	for key, rp := range cases {
		t.Run(key, func(t *testing.T) {
			wire := domainToUpsertWire(&domain.DaemonProfile{WorkspaceKey: "WS1", RestartPolicy: rp})
			raw, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				RestartPolicy map[string]any `json:"restart_policy"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if _, ok := got.RestartPolicy[key]; !ok {
				t.Fatalf("restart_policy.%s dropped on the fleet-db wire: %s", key, raw)
			}
		})
	}
}
