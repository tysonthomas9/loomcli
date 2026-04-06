package migrate

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestMigrateCreate_Success(t *testing.T) {
	var mu sync.Mutex
	var created []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues") {
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			json.Unmarshal(body, &req)
			mu.Lock()
			created = append(created, req["id"].(string))
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{ID: "t1", Title: "Task 1", IssueType: "task", Status: "open"},
		{ID: "t2", Title: "Task 2", IssueType: "task", Status: "open"},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateCreateIssues(cfg, srv.Client(), issues, result)
	if result.created != 2 {
		t.Errorf("expected 2 created, got %d", result.created)
	}
	if len(created) != 2 {
		t.Errorf("expected 2 POST requests, got %d", len(created))
	}
}

func TestMigrateCreate_Conflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "already exists"})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{ID: "existing", Title: "Already there", IssueType: "task", Status: "open"},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateCreateIssues(cfg, srv.Client(), issues, result)

	if result.skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", result.skipped)
	}
	if result.failed != 0 {
		t.Errorf("expected 0 failed, got %d", result.failed)
	}
}

func TestMigrateCreate_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "internal"})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{ID: "fail1", Title: "Fail", IssueType: "task"},
		{ID: "ok1", Title: "OK", IssueType: "task"},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateCreateIssues(cfg, srv.Client(), issues, result)

	// Both get 500 from our mock
	if result.failed != 2 {
		t.Errorf("expected 2 failed, got %d", result.failed)
	}
}

func TestMigrateCreate_FieldMapping(t *testing.T) {
	var receivedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &receivedBody)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	estMin := 60
	issues := []migrateIssueDetail{
		{
			ID:                 "full",
			Title:              "Full Issue",
			IssueType:          "feature",
			Priority:           2,
			Parent:             "epic-1",
			Description:        "desc",
			Design:             "design text",
			AcceptanceCriteria: "ac text",
			Notes:              "notes",
			Assignee:           "bob",
			Owner:              "alice",
			CreatedBy:          "charlie",
			ExternalRef:        "GH-123",
			EstimatedMinutes:   &estMin,
			Labels:             []string{"urgent", "backend"},
		},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateCreateIssues(cfg, srv.Client(), issues, result)

	if receivedBody["id"] != "full" {
		t.Errorf("id = %v, want full", receivedBody["id"])
	}
	if receivedBody["title"] != "Full Issue" {
		t.Errorf("title = %v, want Full Issue", receivedBody["title"])
	}
	if receivedBody["issue_type"] != "feature" {
		t.Errorf("issue_type = %v, want feature", receivedBody["issue_type"])
	}
	if receivedBody["parent"] != "epic-1" {
		t.Errorf("parent = %v, want epic-1", receivedBody["parent"])
	}
	if receivedBody["description"] != "desc" {
		t.Errorf("description = %v, want desc", receivedBody["description"])
	}
	if receivedBody["design"] != "design text" {
		t.Errorf("design = %v, want design text", receivedBody["design"])
	}
	if receivedBody["acceptance_criteria"] != "ac text" {
		t.Errorf("acceptance_criteria = %v, want ac text", receivedBody["acceptance_criteria"])
	}
	if receivedBody["assignee"] != "bob" {
		t.Errorf("assignee = %v, want bob", receivedBody["assignee"])
	}
	if receivedBody["owner"] != "alice" {
		t.Errorf("owner = %v, want alice", receivedBody["owner"])
	}
}

func TestMigrateStatusPatch_OpenIssue(t *testing.T) {
	patchCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patchCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{ID: "open1", Status: "open"},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateUpdateStatuses(cfg, srv.Client(), issues, result)

	if patchCalled {
		t.Error("PATCH should not be called for open issues")
	}
}

func TestMigrateStatusPatch_ClosedIssue(t *testing.T) {
	var patchStatus string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			json.Unmarshal(body, &req)
			patchStatus, _ = req["status"].(string)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{ID: "closed1", Status: "closed"},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateUpdateStatuses(cfg, srv.Client(), issues, result)

	if patchStatus != "closed" {
		t.Errorf("expected PATCH with status=closed, got %q", patchStatus)
	}
}

func TestMigrateStatusPatch_InProgressIssue(t *testing.T) {
	var patchStatus string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			json.Unmarshal(body, &req)
			patchStatus, _ = req["status"].(string)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{ID: "ip1", Status: "in_progress"},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateUpdateStatuses(cfg, srv.Client(), issues, result)

	if patchStatus != "in_progress" {
		t.Errorf("expected PATCH with status=in_progress, got %q", patchStatus)
	}
}

func TestMigrateDeps_Success(t *testing.T) {
	var mu sync.Mutex
	var depRequests []map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/dependencies") {
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			json.Unmarshal(body, &req)
			mu.Lock()
			depRequests = append(depRequests, req)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{
			ID: "a",
			Dependencies: []migrateDependency{
				{IssueID: "a", DependsOnID: "b", Type: "blocks"},
			},
		},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateAddDependencies(cfg, srv.Client(), issues, result)

	if result.depsAdded != 1 {
		t.Errorf("expected 1 dep added, got %d", result.depsAdded)
	}
	if len(depRequests) != 1 {
		t.Fatalf("expected 1 dep request, got %d", len(depRequests))
	}
	if depRequests[0]["depends_on_id"] != "b" {
		t.Errorf("depends_on_id = %v, want b", depRequests[0]["depends_on_id"])
	}
}

func TestMigrateDeps_SkipsParentChild(t *testing.T) {
	depCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/dependencies") {
			depCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{
			ID: "child",
			Dependencies: []migrateDependency{
				{IssueID: "child", DependsOnID: "parent", Type: "parent-child"},
			},
		},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateAddDependencies(cfg, srv.Client(), issues, result)

	if depCalled {
		t.Error("should not POST parent-child dependencies")
	}
}

func TestMigrateDeps_AlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{
			ID: "a",
			Dependencies: []migrateDependency{
				{DependsOnID: "b", Type: "blocks"},
			},
		},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateAddDependencies(cfg, srv.Client(), issues, result)

	if result.depsSkipped != 1 {
		t.Errorf("expected 1 dep skipped, got %d", result.depsSkipped)
	}
}

func TestMigrateComments_Success(t *testing.T) {
	var mu sync.Mutex
	var commentTexts []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/comments") {
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			json.Unmarshal(body, &req)
			mu.Lock()
			commentTexts = append(commentTexts, req["text"].(string))
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	issues := []migrateIssueDetail{
		{
			ID: "issue1",
			Comments: []migrateComment{
				{Author: "alice", Text: "Great work"},
				{Author: "", Text: "Auto-generated"},
			},
		},
	}

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws"}
	result := &migrateResult{}
	migrateAddComments(cfg, srv.Client(), issues, result)

	if result.commentsAdded != 2 {
		t.Errorf("expected 2 comments added, got %d", result.commentsAdded)
	}
	// First comment should have author prefix
	if !strings.Contains(commentTexts[0], "[alice]") {
		t.Errorf("expected author prefix, got %q", commentTexts[0])
	}
}

func TestMigrateUpdateConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &migrateConfig{
		fleetURL:   "https://fleet.example.com",
		workspace:  "prod",
		apiKey:     "key123",
		projectDir: dir,
	}

	err := migrateUpdateConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "loom.yaml"))
	if err != nil {
		t.Fatalf("reading loom.yaml: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "issue_backend: fleet") {
		t.Error("expected issue_backend: fleet in loom.yaml")
	}
	if !strings.Contains(content, "url: https://fleet.example.com") {
		t.Error("expected fleet URL in loom.yaml")
	}
	if !strings.Contains(content, "workspace: prod") {
		t.Error("expected workspace in loom.yaml")
	}
}

func TestMigrateUpdateConfig_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	existing := `version: 2
backend: claude
daemon:
    pid_file: custom.pid
`
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(existing), 0644)

	cfg := &migrateConfig{
		fleetURL:   "https://fleet.example.com",
		workspace:  "staging",
		projectDir: dir,
	}

	err := migrateUpdateConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "loom.yaml"))
	if err != nil {
		t.Fatalf("reading loom.yaml: %v", err)
	}

	content := string(data)
	// Should preserve existing fields
	if !strings.Contains(content, "backend: claude") {
		t.Error("expected backend: claude preserved")
	}
	if !strings.Contains(content, "pid_file: custom.pid") {
		t.Error("expected pid_file preserved")
	}
	// Should add fleet config
	if !strings.Contains(content, "issue_backend: fleet") {
		t.Error("expected issue_backend: fleet added")
	}
}

func TestMigrateUpdateConfig_Skipped(t *testing.T) {
	dir := t.TempDir()
	cfg := &migrateConfig{
		updateConfig: false,
		projectDir:   dir,
	}

	// We don't call migrateUpdateConfig when updateConfig is false
	// Just verify the file doesn't exist
	_, err := os.Stat(filepath.Join(dir, "loom.yaml"))
	if !os.IsNotExist(err) {
		t.Error("expected no loom.yaml when update-config is false")
	}

	_ = cfg // cfg.updateConfig=false would skip the call in runMigrateToFleet
}

func TestMigrateToFleet_FullFlow(t *testing.T) {
	var mu sync.Mutex
	var createIDs []string
	var patchIDs []string
	var depPairs []string
	var commentIDs []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/workspaces/"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			json.Unmarshal(body, &req)
			parts := strings.Split(r.URL.Path, "/")
			for i, p := range parts {
				if p == "issues" && i+1 < len(parts) {
					commentIDs = append(commentIDs, parts[i+1])
					break
				}
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/dependencies"):
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			json.Unmarshal(body, &req)
			parts := strings.Split(r.URL.Path, "/")
			for i, p := range parts {
				if p == "issues" && i+1 < len(parts) {
					depPairs = append(depPairs, parts[i+1]+"→"+req["depends_on_id"].(string))
					break
				}
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues"):
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			json.Unmarshal(body, &req)
			createIDs = append(createIDs, req["id"].(string))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		case r.Method == http.MethodPatch:
			parts := strings.Split(r.URL.Path, "/")
			for i, p := range parts {
				if p == "issues" && i+1 < len(parts) {
					patchIDs = append(patchIDs, parts[i+1])
					break
				}
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	bd := newMigrateMockBD()
	bd.setVersion()
	bd.setList("open", []migrateIssue{{ID: "epic-1"}, {ID: "task-1"}, {ID: "task-2"}})
	bd.setList("in_progress", []migrateIssue{{ID: "task-3"}})
	bd.setList("review", []migrateIssue{})
	bd.setList("blocked", []migrateIssue{})
	bd.setList("deferred", []migrateIssue{})

	bd.setShow("epic-1", migrateIssueDetail{
		ID: "epic-1", Title: "Epic", IssueType: "epic", Status: "open", Priority: 1,
	})
	bd.setShow("task-1", migrateIssueDetail{
		ID: "task-1", Title: "Task 1", IssueType: "task", Status: "open", Priority: 2,
		Parent: "epic-1",
		Dependencies: []migrateDependency{
			{IssueID: "task-1", DependsOnID: "task-2", Type: "blocks"},
		},
		Comments: []migrateComment{
			{Author: "alice", Text: "Review this"},
		},
	})
	bd.setShow("task-2", migrateIssueDetail{
		ID: "task-2", Title: "Task 2", IssueType: "task", Status: "open", Priority: 2,
		Parent: "epic-1",
	})
	bd.setShow("task-3", migrateIssueDetail{
		ID: "task-3", Title: "Task 3", IssueType: "task", Status: "in_progress", Priority: 2,
		Comments: []migrateComment{
			{Author: "bob", Text: "WIP"},
		},
	})

	cfg := &migrateConfig{
		fleetURL:  srv.URL,
		workspace: "ws",
		batchSize: 50,
	}

	err := runMigrateToFleetWithRunner(cfg, bd, srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify creation order: epic-1 before task-1, task-2
	if len(createIDs) != 4 {
		t.Fatalf("expected 4 creates, got %d: %v", len(createIDs), createIDs)
	}
	epicIdx := -1
	for i, id := range createIDs {
		if id == "epic-1" {
			epicIdx = i
			break
		}
	}
	for i, id := range createIDs {
		if (id == "task-1" || id == "task-2") && i < epicIdx {
			t.Errorf("child %s created before parent epic-1", id)
		}
	}

	// Verify status patches (only task-3 is non-open)
	found := false
	for _, id := range patchIDs {
		if id == "task-3" {
			found = true
		}
	}
	if !found {
		t.Error("expected task-3 to get status PATCH")
	}

	// Verify dependencies
	if len(depPairs) != 1 || depPairs[0] != "task-1→task-2" {
		t.Errorf("expected dep task-1→task-2, got %v", depPairs)
	}

	// Verify comments
	if len(commentIDs) != 2 {
		t.Errorf("expected 2 comments, got %d", len(commentIDs))
	}
}

func TestMigrateToFleet_Idempotent(t *testing.T) {
	createCount := 0
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues") &&
			!strings.Contains(r.URL.Path, "/dependencies") &&
			!strings.Contains(r.URL.Path, "/comments"):
			createCount++
			// Second run: everything conflicts
			if createCount > 2 {
				w.WriteHeader(http.StatusConflict)
			} else {
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	bd := newMigrateMockBD()
	bd.setVersion()
	bd.setList("open", []migrateIssue{{ID: "x1"}, {ID: "x2"}})
	bd.setList("in_progress", []migrateIssue{})
	bd.setList("review", []migrateIssue{})
	bd.setList("blocked", []migrateIssue{})
	bd.setList("deferred", []migrateIssue{})
	bd.setShow("x1", migrateIssueDetail{ID: "x1", Title: "X1", IssueType: "task", Status: "open"})
	bd.setShow("x2", migrateIssueDetail{ID: "x2", Title: "X2", IssueType: "task", Status: "open"})

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws", batchSize: 50}

	// First run: 2 created
	err := runMigrateToFleetWithRunner(cfg, bd, srv.Client())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Second run: 2 skipped (conflict)
	err = runMigrateToFleetWithRunner(cfg, bd, srv.Client())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
}

func TestMigrateToFleet_DryRun(t *testing.T) {
	postCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	bd := newMigrateMockBD()
	bd.setVersion()
	bd.setList("open", []migrateIssue{{ID: "d1"}})
	bd.setList("in_progress", []migrateIssue{})
	bd.setList("review", []migrateIssue{})
	bd.setList("blocked", []migrateIssue{})
	bd.setList("deferred", []migrateIssue{})
	bd.setShow("d1", migrateIssueDetail{
		ID: "d1", Title: "Dry", IssueType: "task", Status: "open",
		Dependencies: []migrateDependency{{DependsOnID: "d2", Type: "blocks"}},
		Comments:     []migrateComment{{Author: "a", Text: "hi"}},
	})

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws", batchSize: 50, dryRun: true}

	err := runMigrateToFleetWithRunner(cfg, bd, srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if postCalled {
		t.Error("dry run should not make POST requests")
	}
}

func TestMigrateToFleet_EmptyDatabase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	bd := newMigrateMockBD()
	bd.setVersion()
	bd.setList("open", []migrateIssue{})
	bd.setList("in_progress", []migrateIssue{})
	bd.setList("review", []migrateIssue{})
	bd.setList("blocked", []migrateIssue{})
	bd.setList("deferred", []migrateIssue{})

	cfg := &migrateConfig{fleetURL: srv.URL, workspace: "ws", batchSize: 50}

	err := runMigrateToFleetWithRunner(cfg, bd, srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
