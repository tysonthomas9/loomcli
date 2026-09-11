package fleet

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// captureLog swaps the default slog handler for the duration of the test and
// resets the warn throttle so cases cannot starve each other.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	repoFilterWarns.Lock()
	repoFilterWarns.last = make(map[string]time.Time)
	repoFilterWarns.Unlock()
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// The stall this filter caused was invisible: the agent saw an empty queue and
// idled, and "nothing to do" looked exactly like "everything was filtered out".
func TestWarnReadyRepoFilterStarvation_ReportsAllCandidatesFiltered(t *testing.T) {
	buf := captureLog(t)
	candidates := []backend.IssueData{
		{ID: "X-1"},
		{ID: "X-2", SourceRepo: "other"},
	}
	warnReadyRepoFilterStarvation([]string{"loomcli"}, candidates, nil)

	got := buf.String()
	if !strings.Contains(got, "level=WARN") {
		t.Fatalf("log = %q, want a WARN record", got)
	}
	for _, want := range []string{"candidates=2", "unscoped_candidates=1", "loomcli"} {
		if !strings.Contains(got, want) {
			t.Errorf("log = %q, missing %q", got, want)
		}
	}
}

func TestWarnReadyRepoFilterStarvation_QuietWhenNothingIsStarved(t *testing.T) {
	candidates := []backend.IssueData{{ID: "X-1"}}
	cases := []struct {
		name        string
		sourceRepos []string
		candidates  []backend.IssueData
		kept        []backend.IssueData
	}{
		{"no repo filter", nil, candidates, nil},
		{"server returned nothing", []string{"loomcli"}, nil, nil},
		{"filter kept work", []string{"loomcli"}, candidates, candidates},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			warnReadyRepoFilterStarvation(tc.sourceRepos, tc.candidates, tc.kept)
			if got := buf.String(); got != "" {
				t.Errorf("log = %q, want silence", got)
			}
		})
	}
}

// The daemon polls the ready queue continuously; an unthrottled warning would
// be a flood rather than a signal.
func TestWarnReadyRepoFilterStarvation_IsThrottled(t *testing.T) {
	buf := captureLog(t)
	candidates := []backend.IssueData{{ID: "X-1"}}
	for i := 0; i < 3; i++ {
		warnReadyRepoFilterStarvation([]string{"loomcli"}, candidates, nil)
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Fatalf("WARN records = %d, want 1", n)
	}
	// A different repo set is a different signal and is not throttled with it.
	warnReadyRepoFilterStarvation([]string{"fleet-db"}, candidates, nil)
	if n := strings.Count(buf.String(), "level=WARN"); n != 2 {
		t.Fatalf("WARN records = %d, want 2 after a distinct repo set", n)
	}
}
