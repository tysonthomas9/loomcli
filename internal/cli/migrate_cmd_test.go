package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// migrateMockBD simulates bd CLI output for testing migration.
type migrateMockBD struct {
	responses map[string][]byte
	errors    map[string]error
}

func newMigrateMockBD() *migrateMockBD {
	return &migrateMockBD{
		responses: make(map[string][]byte),
		errors:    make(map[string]error),
	}
}

func (m *migrateMockBD) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	if err, ok := m.errors[key]; ok {
		return nil, err
	}
	if resp, ok := m.responses[key]; ok {
		return resp, nil
	}
	// Try prefix matching for "show <id> --json"
	for k, v := range m.responses {
		if strings.HasPrefix(key, k) || key == k {
			return v, nil
		}
	}
	return nil, fmt.Errorf("unexpected bd command: %s", key)
}

func (m *migrateMockBD) setList(status string, issues []migrateIssue) {
	data, _ := json.Marshal(issues)
	m.responses[fmt.Sprintf("list --status=%s --json --limit 0", status)] = data
}

func (m *migrateMockBD) setShow(id string, detail migrateIssueDetail) {
	data, _ := json.Marshal([]migrateIssueDetail{detail})
	m.responses[fmt.Sprintf("show %s --json", id)] = data
}

func (m *migrateMockBD) setVersion() {
	m.responses["--version"] = []byte("beads v1.0.0")
}

func TestMigratePreflight_BDNotFound(t *testing.T) {
	bd := newMigrateMockBD()
	bd.errors["--version"] = fmt.Errorf("not found")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws1"}
	err := migratePreflight(cfg, bd, srv.Client())
	if err == nil || !strings.Contains(err.Error(), "bd CLI not found") {
		t.Errorf("expected bd CLI not found error, got: %v", err)
	}
}

func TestMigratePreflight_FleetUnreachable(t *testing.T) {
	bd := newMigrateMockBD()
	bd.setVersion()

	cfg := &migrateConfig{fleetURL: "http://localhost:1", workspace: "ws1"}
	err := migratePreflight(cfg, bd, &http.Client{})
	if err == nil || !strings.Contains(err.Error(), "cannot reach fleet server") {
		t.Errorf("expected unreachable error, got: %v", err)
	}
}

func TestMigratePreflight_WorkspaceNotFound(t *testing.T) {
	bd := newMigrateMockBD()
	bd.setVersion()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/api/workspaces/missing-ws":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "missing-ws"}
	err := migratePreflight(cfg, bd, srv.Client())
	if err == nil || !strings.Contains(err.Error(), "not found on fleet server") {
		t.Errorf("expected workspace not found, got: %v", err)
	}
}

func TestMigratePreflight_Success(t *testing.T) {
	bd := newMigrateMockBD()
	bd.setVersion()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "test-ws"}
	err := migratePreflight(cfg, bd, srv.Client())
	if err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

func TestMigrateEnumerate_AllStatuses(t *testing.T) {
	bd := newMigrateMockBD()
	bd.setList("open", []migrateIssue{{ID: "a", Status: "open"}})
	bd.setList("in_progress", []migrateIssue{{ID: "b", Status: "in_progress"}})
	bd.setList("review", []migrateIssue{{ID: "c", Status: "review"}})
	bd.setList("blocked", []migrateIssue{{ID: "d", Status: "blocked"}})
	bd.setList("deferred", []migrateIssue{{ID: "e", Status: "deferred"}})

	cfg := &migrateConfig{includeClosed: false}
	issues, err := migrateEnumerate(cfg, bd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 5 {
		t.Errorf("expected 5 issues, got %d", len(issues))
	}
}

func TestMigrateEnumerate_ClosedExcluded(t *testing.T) {
	bd := newMigrateMockBD()
	bd.setList("open", []migrateIssue{{ID: "a"}})
	bd.setList("in_progress", []migrateIssue{})
	bd.setList("review", []migrateIssue{})
	bd.setList("blocked", []migrateIssue{})
	bd.setList("deferred", []migrateIssue{})

	cfg := &migrateConfig{includeClosed: false}
	issues, err := migrateEnumerate(cfg, bd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 1 {
		t.Errorf("expected 1 issue (closed excluded), got %d", len(issues))
	}
}

func TestMigrateEnumerate_ClosedIncluded(t *testing.T) {
	bd := newMigrateMockBD()
	bd.setList("open", []migrateIssue{{ID: "a"}})
	bd.setList("in_progress", []migrateIssue{})
	bd.setList("review", []migrateIssue{})
	bd.setList("blocked", []migrateIssue{})
	bd.setList("deferred", []migrateIssue{})
	bd.setList("closed", []migrateIssue{{ID: "z", Status: "closed"}})

	cfg := &migrateConfig{includeClosed: true}
	issues, err := migrateEnumerate(cfg, bd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Errorf("expected 2 issues, got %d", len(issues))
	}
}

func TestMigrateEnumerate_Deduplication(t *testing.T) {
	bd := newMigrateMockBD()
	bd.setList("open", []migrateIssue{{ID: "dup1"}, {ID: "dup2"}})
	bd.setList("in_progress", []migrateIssue{{ID: "dup1"}}) // same as open
	bd.setList("review", []migrateIssue{})
	bd.setList("blocked", []migrateIssue{})
	bd.setList("deferred", []migrateIssue{})

	cfg := &migrateConfig{includeClosed: false}
	issues, err := migrateEnumerate(cfg, bd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Errorf("expected 2 (deduplicated), got %d", len(issues))
	}
}

func TestTopologicalSort_FlatList(t *testing.T) {
	issues := []migrateIssueDetail{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	}
	sorted, circular := topologicalSort(issues)
	if len(circular) != 0 {
		t.Errorf("expected no circular refs, got %v", circular)
	}
	if len(sorted) != 3 {
		t.Errorf("expected 3 items, got %d", len(sorted))
	}
}

func TestTopologicalSort_ParentsFirst(t *testing.T) {
	issues := []migrateIssueDetail{
		{ID: "child", Parent: "parent"},
		{ID: "parent"},
	}
	sorted, circular := topologicalSort(issues)
	if len(circular) != 0 {
		t.Errorf("expected no circular refs, got %v", circular)
	}
	if sorted[0].ID != "parent" {
		t.Errorf("expected parent first, got %s", sorted[0].ID)
	}
	if sorted[1].ID != "child" {
		t.Errorf("expected child second, got %s", sorted[1].ID)
	}
}

func TestTopologicalSort_MultiLevel(t *testing.T) {
	issues := []migrateIssueDetail{
		{ID: "grandchild", Parent: "child"},
		{ID: "child", Parent: "parent"},
		{ID: "parent"},
	}
	sorted, _ := topologicalSort(issues)
	// parent (depth 0) → child (depth 1) → grandchild (depth 2)
	if sorted[0].ID != "parent" {
		t.Errorf("expected parent first, got %s", sorted[0].ID)
	}
	if sorted[1].ID != "child" {
		t.Errorf("expected child second, got %s", sorted[1].ID)
	}
	if sorted[2].ID != "grandchild" {
		t.Errorf("expected grandchild third, got %s", sorted[2].ID)
	}
}

func TestTopologicalSort_CircularParent(t *testing.T) {
	issues := []migrateIssueDetail{
		{ID: "a", Parent: "b"},
		{ID: "b", Parent: "a"},
	}
	_, circular := topologicalSort(issues)
	if len(circular) == 0 {
		t.Error("expected circular parent detection")
	}
	if !circular["a"] || !circular["b"] {
		t.Errorf("expected both a and b in circular set, got %v", circular)
	}
}

func TestTopologicalSort_OrphanParent(t *testing.T) {
	issues := []migrateIssueDetail{
		{ID: "child", Parent: "missing-parent"},
	}
	sorted, circular := topologicalSort(issues)
	if len(circular) != 0 {
		t.Errorf("expected no circular refs for orphan, got %v", circular)
	}
	if len(sorted) != 1 || sorted[0].ID != "child" {
		t.Errorf("expected child in sorted, got %v", sorted)
	}
}
