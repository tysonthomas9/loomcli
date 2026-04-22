package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFormatContextBanner_TypicalData(t *testing.T) {
	tc := &TerminalContext{
		Stats: TerminalContextStats{
			Open:       5,
			Closed:     3,
			Total:      8,
			InProgress: 2,
			Review:     1,
			Blocked:    1,
		},
		Agents: []TerminalAgentInfo{
			{Name: "agent-1", Status: "running"},
		},
		Tasks: TerminalContextTasks{
			NeedsPlanning:    2,
			ReadyToImplement: 3,
			InProgress:       1,
			NeedReview:       1,
			Backlog:          4,
		},
	}

	banner := FormatContextBanner(tc, "my-project")

	checks := []struct {
		label string
		want  string
	}{
		{"workspace name", "my-project"},
		{"open count", "5 open"},
		{"blocked count", "1 blocked"},
		{"review count", "1 review"},
		{"in-progress count", "2 in-progress"},
		{"agent info", "agent-1 (running)"},
		{"needs plans", "2 need plans"},
		{"ready to implement", "3 ready to implement"},
	}
	for _, c := range checks {
		if !strings.Contains(banner, c.want) {
			t.Errorf("banner missing %s %q; got:\n%s", c.label, c.want, banner)
		}
	}
}

func TestFormatContextBanner_EmptyWorkspace(t *testing.T) {
	tc := &TerminalContext{}
	banner := FormatContextBanner(tc, "")

	if !strings.Contains(banner, "(default)") {
		t.Errorf("expected banner to contain %q for empty workspace; got:\n%s", "(default)", banner)
	}
}

func TestFormatContextBanner_NoAgents(t *testing.T) {
	tc := &TerminalContext{
		Agents: nil,
	}
	banner := FormatContextBanner(tc, "ws")

	if !strings.Contains(banner, "Agents: none active") {
		t.Errorf("expected banner to contain %q when no agents; got:\n%s", "Agents: none active", banner)
	}
}

func TestFormatContextBanner_MultipleAgents(t *testing.T) {
	tc := &TerminalContext{
		Agents: []TerminalAgentInfo{
			{Name: "alpha", Status: "running"},
			{Name: "beta", Status: "idle"},
			{Name: "gamma", Status: "running"},
		},
	}
	banner := FormatContextBanner(tc, "ws")

	// The middle dot separator (\u00b7) should appear between agents.
	if !strings.Contains(banner, "alpha (running) \u00b7 beta (idle)") {
		t.Errorf("expected agents separated by middle dot; got:\n%s", banner)
	}
	if !strings.Contains(banner, "beta (idle) \u00b7 gamma (running)") {
		t.Errorf("expected all three agents separated by middle dot; got:\n%s", banner)
	}
}

func TestFormatContextBanner_ZeroCounts(t *testing.T) {
	tc := &TerminalContext{
		Stats:  TerminalContextStats{},
		Agents: nil,
		Tasks:  TerminalContextTasks{},
	}
	banner := FormatContextBanner(tc, "empty")

	// Verify the banner is structurally valid: has top border, bottom border, content.
	if !strings.Contains(banner, "\u250c") {
		t.Error("missing top-left corner (box drawing)")
	}
	if !strings.Contains(banner, "\u2518") {
		t.Error("missing bottom-right corner (box drawing)")
	}
	if !strings.Contains(banner, "0 open") {
		t.Errorf("expected zero counts in banner; got:\n%s", banner)
	}
	if !strings.Contains(banner, "0 blocked") {
		t.Errorf("expected zero blocked in banner; got:\n%s", banner)
	}
	if !strings.Contains(banner, "Agents: none active") {
		t.Errorf("expected no agents line; got:\n%s", banner)
	}
	if !strings.Contains(banner, "0 need plans") {
		t.Errorf("expected zero planning count; got:\n%s", banner)
	}
}

func TestFormatContextBanner_BoxAlignment(t *testing.T) {
	tc := &TerminalContext{
		Stats: TerminalContextStats{
			Open:       12,
			Blocked:    3,
			Review:     2,
			InProgress: 5,
		},
		Agents: []TerminalAgentInfo{
			{Name: "falcon", Status: "working"},
			{Name: "nova", Status: "ready"},
		},
		Tasks: TerminalContextTasks{
			NeedsPlanning:    4,
			ReadyToImplement: 6,
		},
	}

	banner := FormatContextBanner(tc, "my-project")

	// Split banner into lines (using \r\n as the line ending).
	rawLines := strings.Split(banner, "\r\n")
	// Remove trailing empty element from final \r\n.
	var bannerLines []string
	for _, l := range rawLines {
		if l != "" {
			bannerLines = append(bannerLines, l)
		}
	}

	if len(bannerLines) < 3 {
		t.Fatalf("expected at least 3 banner lines; got %d:\n%s", len(bannerLines), banner)
	}

	// All lines must have the same rune width.
	widths := make([]int, len(bannerLines))
	for i, l := range bannerLines {
		widths[i] = utf8.RuneCountInString(l)
	}
	for i := 1; i < len(widths); i++ {
		if widths[i] != widths[0] {
			t.Errorf("line %d width = %d, want %d (same as line 0)\nline 0: %q\nline %d: %q",
				i, widths[i], widths[0], bannerLines[0], i, bannerLines[i])
		}
	}
}

func TestFetchTerminalContext_Success(t *testing.T) {
	tc := TerminalContext{
		Stats: TerminalContextStats{
			Open:       10,
			Closed:     5,
			Total:      15,
			InProgress: 3,
			Review:     2,
			Blocked:    1,
		},
		Agents: []TerminalAgentInfo{
			{Name: "worker-1", Status: "active"},
			{Name: "worker-2", Status: "idle"},
		},
		Tasks: TerminalContextTasks{
			NeedsPlanning:    4,
			ReadyToImplement: 6,
			InProgress:       3,
			NeedReview:       2,
			Backlog:          8,
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces/test-ws/monitor/status" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %q", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tc)
	}))
	defer srv.Close()

	got, err := FetchTerminalContext(srv.URL, "test-ws")
	if err != nil {
		t.Fatalf("FetchTerminalContext() error = %v", err)
	}

	// Verify stats
	if got.Stats.Open != 10 {
		t.Errorf("Stats.Open = %d, want 10", got.Stats.Open)
	}
	if got.Stats.Closed != 5 {
		t.Errorf("Stats.Closed = %d, want 5", got.Stats.Closed)
	}
	if got.Stats.Total != 15 {
		t.Errorf("Stats.Total = %d, want 15", got.Stats.Total)
	}
	if got.Stats.InProgress != 3 {
		t.Errorf("Stats.InProgress = %d, want 3", got.Stats.InProgress)
	}
	if got.Stats.Review != 2 {
		t.Errorf("Stats.Review = %d, want 2", got.Stats.Review)
	}
	if got.Stats.Blocked != 1 {
		t.Errorf("Stats.Blocked = %d, want 1", got.Stats.Blocked)
	}

	// Verify agents
	if len(got.Agents) != 2 {
		t.Fatalf("len(Agents) = %d, want 2", len(got.Agents))
	}
	if got.Agents[0].Name != "worker-1" || got.Agents[0].Status != "active" {
		t.Errorf("Agents[0] = %+v, want {worker-1 active}", got.Agents[0])
	}
	if got.Agents[1].Name != "worker-2" || got.Agents[1].Status != "idle" {
		t.Errorf("Agents[1] = %+v, want {worker-2 idle}", got.Agents[1])
	}

	// Verify tasks
	if got.Tasks.NeedsPlanning != 4 {
		t.Errorf("Tasks.NeedsPlanning = %d, want 4", got.Tasks.NeedsPlanning)
	}
	if got.Tasks.ReadyToImplement != 6 {
		t.Errorf("Tasks.ReadyToImplement = %d, want 6", got.Tasks.ReadyToImplement)
	}
	if got.Tasks.InProgress != 3 {
		t.Errorf("Tasks.InProgress = %d, want 3", got.Tasks.InProgress)
	}
	if got.Tasks.NeedReview != 2 {
		t.Errorf("Tasks.NeedReview = %d, want 2", got.Tasks.NeedReview)
	}
	if got.Tasks.Backlog != 8 {
		t.Errorf("Tasks.Backlog = %d, want 8", got.Tasks.Backlog)
	}
}

func TestFetchTerminalContext_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	_, err := FetchTerminalContext(srv.URL, "test-ws")
	if err == nil {
		t.Fatal("FetchTerminalContext() expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "decode response")
	}
}

func TestFetchTerminalContext_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := FetchTerminalContext(srv.URL, "test-ws")
	if err == nil {
		t.Fatal("FetchTerminalContext() expected error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected status 500") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "unexpected status 500")
	}
}

func TestFetchTerminalContext_ServerDown(t *testing.T) {
	// Use a URL that will not connect to anything.
	_, err := FetchTerminalContext("http://127.0.0.1:1", "test-ws")
	if err == nil {
		t.Fatal("FetchTerminalContext() expected error for unreachable server, got nil")
	}
	if !strings.Contains(err.Error(), "fetch status") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "fetch status")
	}
}
