package fleetsim

import "testing"

// Response shapes pinned to FleetDB 40e8431d: GET /issues/ready (and the
// workspace-root /ready alias) returns readyListResponse {issues, count}, and
// models.Issue has no has_design field. The live differential
// (sim_fleetdb_diff_test.go, tag fleetdbdiff) found both drifts.
func TestServerReadyEnvelopeAndNoHasDesign(t *testing.T) {
	sim := New(t0, "LOCALMODE", Guards{})
	s := sim.Server
	s.Seed(Issue{ID: "LOCALMODE-1", Title: "one", Design: "d", Priority: 2})
	s.Seed(Issue{ID: "LOCALMODE-2", Title: "two", Priority: 1})

	for _, path := range []string{"/issues/ready", "/ready"} {
		code, m := cpDo(t, s, "GET", path, "", nil)
		if code != 200 {
			t.Fatalf("%s: status %d", path, code)
		}
		items, ok := m["issues"].([]any)
		if !ok {
			t.Fatalf("%s: want {issues,count} envelope, got %v", path, m)
		}
		if n, _ := m["count"].(float64); int(n) != len(items) || len(items) != 2 {
			t.Fatalf("%s: count %v, len %d, want 2", path, m["count"], len(items))
		}
		for _, it := range items {
			if _, has := it.(map[string]any)["has_design"]; has {
				t.Fatalf("%s: item carries has_design: %v", path, it)
			}
		}
	}

	if _, m := cpDo(t, s, "GET", "/issues/ready?assignee=nobody", "", nil); m["count"] != float64(0) {
		t.Fatalf("empty ready: want count 0 and issues [], got %v", m)
	} else if items, ok := m["issues"].([]any); !ok || len(items) != 0 {
		t.Fatalf("empty ready: want issues [], got %v", m["issues"])
	}

	code, m := cpDo(t, s, "GET", "/issues/LOCALMODE-1", "", nil)
	if code != 200 || m["design"] != "d" {
		t.Fatalf("get: status %d body %v", code, m)
	}
	if _, has := m["has_design"]; has {
		t.Fatalf("get: body carries has_design: %v", m)
	}
}
