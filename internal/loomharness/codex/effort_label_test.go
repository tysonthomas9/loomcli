package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/claude"
	"github.com/tysonthomas9/loomcli/internal/loomharness/codex/protocol"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomharness/opencode"
)

// TestEffortLabelEveryHarness: every harness's catalog calls its effort
// option "Effort" and shows xhigh as "Extra High". It lives here because
// codex's catalog mapping is unexported; the others go through Models.
func TestEffortLabelEveryHarness(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/model", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt","providerID":"openai","variants":[{"id":"medium"},{"id":"xhigh"}]}]}`))
	})
	mux.HandleFunc("GET /api/model/default", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	mux.HandleFunc("GET /api/provider", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	codexModel := catalogModel(protocol.Model{Model: "gpt", DefaultReasoningEffort: "medium",
		SupportedReasoningEfforts: []protocol.ReasoningEffortOption{{ReasoningEffort: "medium"}, {ReasoningEffort: "xhigh"}}})
	for _, tc := range []struct {
		name   string
		xhigh  bool // offers an xhigh choice
		models func() ([]loomharness.Model, error)
	}{
		{"claude", true, func() ([]loomharness.Model, error) { return (&claude.Adapter{}).Models(ctx) }},
		{"codex", true, func() ([]loomharness.Model, error) { return []loomharness.Model{codexModel}, nil }},
		{"fake", false, func() ([]loomharness.Model, error) { return fake.New().Models(ctx) }},
		{"opencode", true, func() ([]loomharness.Model, error) {
			return (&opencode.Adapter{Client: opencode.NewClient(srv.URL, "pw")}).Models(ctx)
		}},
	} {
		ms, err := tc.models()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		n, xhigh := 0, false
		for _, m := range ms {
			for _, d := range m.Options {
				if d.ID != loomharness.OptionEffort {
					continue
				}
				n++
				if d.Label != "Effort" {
					t.Errorf("%s %s: effort label %q, want Effort", tc.name, m.ID, d.Label)
				}
				for _, c := range d.Choices {
					if c.ID != "xhigh" {
						continue
					}
					xhigh = true
					if c.Label != "Extra High" {
						t.Errorf("%s %s: xhigh label %q, want Extra High", tc.name, m.ID, c.Label)
					}
				}
			}
		}
		if n == 0 || xhigh != tc.xhigh {
			t.Errorf("%s: effort options %d, xhigh %v (want %v) in %+v", tc.name, n, xhigh, tc.xhigh, ms)
		}
	}
}
