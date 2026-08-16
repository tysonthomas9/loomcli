package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// intPtr lives in role_max_run_duration_test.go.
func boolPtr(v bool) *bool { return &v }

// fullRestartPolicy exercises every field loom's domain type carries.
func fullRestartPolicy() domain.RestartPolicy {
	return domain.RestartPolicy{
		MaxRetries:       intPtr(5),
		BackoffInitial:   intPtr(10),
		BackoffMax:       intPtr(600),
		OutputTimeout:    intPtr(120),
		RateLimitBackoff: intPtr(30),
		RateLimitMaxWait: intPtr(3600),
		RateLimitNoCount: boolPtr(true),
		TimeoutBackoff:   intPtr(45),
		NoWorkBackoff:    intPtr(15),
		IdlePollInterval: intPtr(300),
		YieldTimeout:     intPtr(90),
		SigtermTimeout:   intPtr(20),
	}
}

// daemonRoundTripServer echoes the PUT body back as the profile, which is
// what fleet-db does — so a field lost in either direction shows up as a
// difference between the sent policy and the one Upsert returns.
func daemonRoundTripServer(t *testing.T, sent *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/WS/daemon" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode PUT body: %v", err)
		}
		*sent = body
		body["workspace_key"] = "WS"
		writeJSON(t, w, body)
	}))
}

func TestDaemonProfileUpsertRoundTripsEveryRestartPolicyField(t *testing.T) {
	var sent map[string]any
	ts := daemonRoundTripServer(t, &sent)
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}

	want := fullRestartPolicy()
	got, err := client.Daemon().Upsert(t.Context(), &domain.DaemonProfile{
		WorkspaceKey:  "WS",
		RestartPolicy: want,
	})
	if err != nil {
		t.Fatalf("upsert daemon profile: %v", err)
	}

	policy, ok := sent["restart_policy"].(map[string]any)
	if !ok {
		t.Fatalf("PUT body has no restart_policy object: %v", sent)
	}

	// Every wire key must be present in the PUT body with the sent value,
	// and must survive the mapping back into the domain type.
	cases := []struct {
		wireKey  string
		wireWant any
		got      func() any
		want     any
	}{
		{"max_retries", float64(5), func() any { return got.RestartPolicy.MaxRetries }, want.MaxRetries},
		{"backoff_initial", float64(10), func() any { return got.RestartPolicy.BackoffInitial }, want.BackoffInitial},
		{"backoff_max", float64(600), func() any { return got.RestartPolicy.BackoffMax }, want.BackoffMax},
		{"output_timeout", float64(120), func() any { return got.RestartPolicy.OutputTimeout }, want.OutputTimeout},
		{"rate_limit_backoff", float64(30), func() any { return got.RestartPolicy.RateLimitBackoff }, want.RateLimitBackoff},
		{"rate_limit_max_wait", float64(3600), func() any { return got.RestartPolicy.RateLimitMaxWait }, want.RateLimitMaxWait},
		{"rate_limit_no_count", true, func() any { return got.RestartPolicy.RateLimitNoCount }, want.RateLimitNoCount},
		{"timeout_backoff", float64(45), func() any { return got.RestartPolicy.TimeoutBackoff }, want.TimeoutBackoff},
		{"no_work_backoff", float64(15), func() any { return got.RestartPolicy.NoWorkBackoff }, want.NoWorkBackoff},
		{"idle_poll_interval", float64(300), func() any { return got.RestartPolicy.IdlePollInterval }, want.IdlePollInterval},
		{"yield_timeout", float64(90), func() any { return got.RestartPolicy.YieldTimeout }, want.YieldTimeout},
		{"sigterm_timeout", float64(20), func() any { return got.RestartPolicy.SigtermTimeout }, want.SigtermTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.wireKey, func(t *testing.T) {
			v, present := policy[tc.wireKey]
			if !present {
				t.Fatalf("PUT body restart_policy is missing %q", tc.wireKey)
			}
			if v != tc.wireWant {
				t.Fatalf("PUT body restart_policy.%s = %v, want %v", tc.wireKey, v, tc.wireWant)
			}
			switch w := tc.want.(type) {
			case *int:
				g, _ := tc.got().(*int)
				if g == nil || *g != *w {
					t.Fatalf("domain %s = %v, want %d", tc.wireKey, g, *w)
				}
			case *bool:
				g, _ := tc.got().(*bool)
				if g == nil || *g != *w {
					t.Fatalf("domain %s = %v, want %t", tc.wireKey, g, *w)
				}
			}
		})
	}
}

func TestDaemonProfileGetMapsNewRestartPolicyFields(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/daemon" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(t, w, map[string]any{
			"workspace_key": "WS",
			"restart_policy": map[string]any{
				// backoff_multiplier / reset_after_success are fleet-db-only
				// and must be ignored rather than break the decode.
				"backoff_multiplier":  2,
				"reset_after_success": 3600,
				"output_timeout":      120,
				"rate_limit_backoff":  30,
				"rate_limit_max_wait": 3600,
				"rate_limit_no_count": true,
				"timeout_backoff":     45,
				"no_work_backoff":     15,
				"idle_poll_interval":  300,
				"yield_timeout":       90,
				"sigterm_timeout":     20,
			},
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := client.Daemon().Get(t.Context(), "WS")
	if err != nil {
		t.Fatalf("get daemon profile: %v", err)
	}
	rp := p.RestartPolicy
	if rp.OutputTimeout == nil || *rp.OutputTimeout != 120 {
		t.Fatalf("output_timeout = %v", rp.OutputTimeout)
	}
	if rp.RateLimitNoCount == nil || !*rp.RateLimitNoCount {
		t.Fatalf("rate_limit_no_count = %v", rp.RateLimitNoCount)
	}
	if rp.NoWorkBackoff == nil || *rp.NoWorkBackoff != 15 {
		t.Fatalf("no_work_backoff = %v", rp.NoWorkBackoff)
	}
	if rp.IdlePollInterval == nil || *rp.IdlePollInterval != 300 {
		t.Fatalf("idle_poll_interval = %v", rp.IdlePollInterval)
	}
	if rp.SigtermTimeout == nil || *rp.SigtermTimeout != 20 {
		t.Fatalf("sigterm_timeout = %v", rp.SigtermTimeout)
	}
}

// A policy that sets only a loom-only field must still emit a
// restart_policy block — hasRestartPolicy used to look at the three
// original fields only, so the value was dropped silently.
func TestDaemonProfileEmitsRestartPolicyForNewFieldsAlone(t *testing.T) {
	cases := []struct {
		name    string
		policy  domain.RestartPolicy
		wireKey string
		want    any
	}{
		{"no_work_backoff only", domain.RestartPolicy{NoWorkBackoff: intPtr(15)}, "no_work_backoff", float64(15)},
		{"idle_poll_interval only", domain.RestartPolicy{IdlePollInterval: intPtr(300)}, "idle_poll_interval", float64(300)},
		{"output_timeout only", domain.RestartPolicy{OutputTimeout: intPtr(120)}, "output_timeout", float64(120)},
		{"rate_limit_no_count only", domain.RestartPolicy{RateLimitNoCount: boolPtr(true)}, "rate_limit_no_count", true},
		{"sigterm_timeout only", domain.RestartPolicy{SigtermTimeout: intPtr(20)}, "sigterm_timeout", float64(20)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			ts := daemonRoundTripServer(t, &sent)
			defer ts.Close()

			client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Daemon().Upsert(t.Context(), &domain.DaemonProfile{
				WorkspaceKey:  "WS",
				RestartPolicy: tc.policy,
			}); err != nil {
				t.Fatalf("upsert daemon profile: %v", err)
			}
			policy, ok := sent["restart_policy"].(map[string]any)
			if !ok {
				t.Fatalf("PUT body omitted restart_policy entirely: %v", sent)
			}
			if len(policy) != 1 {
				t.Fatalf("restart_policy = %v, want only %s", policy, tc.wireKey)
			}
			if policy[tc.wireKey] != tc.want {
				t.Fatalf("restart_policy.%s = %v, want %v", tc.wireKey, policy[tc.wireKey], tc.want)
			}
		})
	}
}

// An entirely-empty policy must still send no block at all, so fleet-db's
// defaults are not clobbered by a replace-semantics PUT.
func TestDaemonProfileOmitsEmptyRestartPolicy(t *testing.T) {
	var sent map[string]any
	ts := daemonRoundTripServer(t, &sent)
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Daemon().Upsert(t.Context(), &domain.DaemonProfile{WorkspaceKey: "WS"}); err != nil {
		t.Fatalf("upsert daemon profile: %v", err)
	}
	if _, present := sent["restart_policy"]; present {
		t.Fatalf("PUT body carries a restart_policy block for an empty policy: %v", sent)
	}
}
