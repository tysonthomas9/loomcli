package localmode_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	runStartedAt = "2026-07-15T03:00:00Z"
	freshAt      = "2026-07-15T03:00:01Z"
	staleAt      = "2026-07-15T02:59:59Z"
)

type localModeManifest struct {
	CheckoutID   string `json:"checkout_id"`
	SourceRoot   string `json:"source_root"`
	Project      string `json:"compose_project"`
	RunID        string `json:"run_id"`
	StartedAt    string `json:"started_at"`
	Backend      string `json:"backend"`
	Plane        string `json:"plane,omitempty"`
	Workspace    string `json:"workspace"`
	PlanAgentID  string `json:"plan_agent_record_id,omitempty"`
	CodeAgentID  string `json:"code_agent_record_id,omitempty"`
	PlanAgent    string `json:"plan_agent_name,omitempty"`
	CodeAgent    string `json:"code_agent_name,omitempty"`
	PlanRole     string `json:"plan_agent_role,omitempty"`
	CodeRole     string `json:"code_agent_role,omitempty"`
	PlanBinding  string `json:"plan_binding_id,omitempty"`
	CodeBinding  string `json:"code_binding_id,omitempty"`
	PlanTaskID   string `json:"plan_task_id"`
	CodeTaskID   string `json:"code_task_id"`
	PlanTaskName string `json:"plan_task_title"`
	CodeTaskName string `json:"code_task_title"`
}

type localModeEvidence struct {
	taskCreatedAt    string
	sessionStartedAt string
	agentCreatedAt   string
	planAssignee     string
}

func TestVerifyLocalModeAcceptsOnlyManifestOwnedFreshEvidence(t *testing.T) {
	manifest := testManifest()
	server := newLocalModeVerifyServer(t, manifest, freshEvidence())
	defer server.Close()

	out, err := runVerifier(t, server.URL, manifest, nil)
	if err != nil {
		t.Fatalf("verify fresh run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "local-mode daemon, agent, transcript, and diff flow verified") {
		t.Fatalf("verification success marker missing:\n%s", out)
	}
}

func TestVerifyLocalModeAcceptsTSPromptAgentsSeededBeforeTasks(t *testing.T) {
	manifest := tsTestManifest()
	server := newLocalModeVerifyServer(t, manifest, freshEvidence())
	defer server.Close()

	out, err := runVerifier(t, server.URL, manifest, nil)
	if err != nil {
		t.Fatalf("verify TS prompt-agent run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "local-mode TS prompt-agent, transcript, and diff flow verified") {
		t.Fatalf("TS verification success marker missing:\n%s", out)
	}
}

func TestVerifyLocalModeRejectsTSPromptAgentsCreatedAfterTasks(t *testing.T) {
	manifest := tsTestManifest()
	evidence := freshEvidence()
	evidence.agentCreatedAt = "2026-07-15T03:00:02Z"
	server := newLocalModeVerifyServer(t, manifest, evidence)
	defer server.Close()

	out, err := runVerifier(t, server.URL, manifest, nil)
	if err == nil {
		t.Fatalf("late prompt agents unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(out, "public planner/coder prompt agents were seeded before tasks") {
		t.Fatalf("late-agent failure was not explicit:\n%s", out)
	}
}

func TestVerifyLocalModeRejectsSessionsOlderThanRun(t *testing.T) {
	manifest := testManifest()
	evidence := freshEvidence()
	evidence.sessionStartedAt = staleAt
	server := newLocalModeVerifyServer(t, manifest, evidence)
	defer server.Close()

	out, err := runVerifier(t, server.URL, manifest, nil)
	if err == nil {
		t.Fatalf("stale sessions unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(out, "planner completed session exists") {
		t.Fatalf("stale-session failure was not explicit:\n%s", out)
	}
}

func TestVerifyLocalModeRejectsTasksOlderThanRun(t *testing.T) {
	manifest := testManifest()
	evidence := freshEvidence()
	evidence.taskCreatedAt = staleAt
	server := newLocalModeVerifyServer(t, manifest, evidence)
	defer server.Close()

	out, err := runVerifier(t, server.URL, manifest, nil)
	if err == nil {
		t.Fatalf("stale tasks unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(out, "planner task belongs to this run") {
		t.Fatalf("stale-task failure was not explicit:\n%s", out)
	}
}

func TestVerifyLocalModeRejectsPlannerWithStaleAssignee(t *testing.T) {
	manifest := testManifest()
	evidence := freshEvidence()
	evidence.planAssignee = "driver-run:stale"
	server := newLocalModeVerifyServer(t, manifest, evidence)
	defer server.Close()

	out, err := runVerifier(t, server.URL, manifest, nil)
	if err == nil {
		t.Fatalf("planner with stale assignee unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(out, "planner task released its assignee") {
		t.Fatalf("stale-assignee failure was not explicit:\n%s", out)
	}
}

func TestVerifyLocalModeRejectsMalformedManifestTimestamp(t *testing.T) {
	manifest := testManifest()
	manifest.StartedAt = "not-a-time"

	out, err := runVerifier(t, "http://127.0.0.1:1", manifest, nil)
	if err == nil {
		t.Fatalf("malformed manifest timestamp unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(out, "must be a valid positive UTC timestamp") {
		t.Fatalf("malformed manifest timestamp failure was not explicit:\n%s", out)
	}
}

func TestVerifyLocalModeRejectsMalformedTaskTimestamp(t *testing.T) {
	manifest := testManifest()
	evidence := freshEvidence()
	evidence.taskCreatedAt = "not-a-time"
	server := newLocalModeVerifyServer(t, manifest, evidence)
	defer server.Close()

	out, err := runVerifier(t, server.URL, manifest, nil)
	if err == nil {
		t.Fatalf("malformed task timestamp unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(out, "planner task belongs to this run") {
		t.Fatalf("malformed task timestamp failure was not explicit:\n%s", out)
	}
}

func TestVerifyLocalModeRejectsMalformedSessionTimestamp(t *testing.T) {
	manifest := testManifest()
	evidence := freshEvidence()
	evidence.sessionStartedAt = "not-a-time"
	server := newLocalModeVerifyServer(t, manifest, evidence)
	defer server.Close()

	out, err := runVerifier(t, server.URL, manifest, nil)
	if err == nil {
		t.Fatalf("malformed session timestamp unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(out, "planner completed session exists") {
		t.Fatalf("malformed session timestamp failure was not explicit:\n%s", out)
	}
}

func TestVerifyLocalModeRejectsUnexpectedBackend(t *testing.T) {
	manifest := testManifest()
	manifest.Backend = "localdogfood"
	out, err := runVerifier(t, "http://127.0.0.1:1", manifest, map[string]string{
		"LOCAL_MODE_EXPECTED_BACKEND": "codex",
	})
	if err == nil {
		t.Fatalf("wrong backend unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(out, "does not match expected backend codex") {
		t.Fatalf("wrong-backend failure was not explicit:\n%s", out)
	}
}

func TestVerifyLocalModeRejectsManifestCheckoutMismatch(t *testing.T) {
	manifest := testManifest()
	out, err := runVerifier(t, "http://127.0.0.1:1", manifest, map[string]string{
		"LOCAL_MODE_CHECKOUT_ID": "different-checkout",
	})
	if err == nil {
		t.Fatalf("checkout mismatch unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(out, "does not match requested checkout") {
		t.Fatalf("checkout mismatch failure was not explicit:\n%s", out)
	}
}

func testManifest() localModeManifest {
	return localModeManifest{
		CheckoutID:   "checkout-rc2",
		SourceRoot:   "/workspace/rc-2/loomcli",
		Project:      "loomcli-local-mode-rc2",
		RunID:        "run-fresh",
		StartedAt:    runStartedAt,
		Backend:      "codex",
		Workspace:    "LOCALMODE",
		PlanTaskID:   "LOCALMODE-101",
		CodeTaskID:   "LOCALMODE-102",
		PlanTaskName: "Local mode planner dogfood [run:run-fresh]",
		CodeTaskName: "Local mode coder dogfood [run:run-fresh]",
	}
}

func freshEvidence() localModeEvidence {
	return localModeEvidence{taskCreatedAt: freshAt, sessionStartedAt: freshAt, agentCreatedAt: runStartedAt}
}

func tsTestManifest() localModeManifest {
	manifest := testManifest()
	manifest.Backend = "localdogfood"
	manifest.Plane = "ts"
	manifest.PlanAgentID = "agt-local-mode-ts-planner"
	manifest.CodeAgentID = "agt-local-mode-ts-coder"
	manifest.PlanAgent = "Local mode TS planner"
	manifest.CodeAgent = "Local mode TS coder"
	manifest.PlanRole = "local-mode-ts-plan"
	manifest.CodeRole = "local-mode-ts-task"
	manifest.PlanBinding = "local-mode-ts-planner"
	manifest.CodeBinding = "local-mode-ts-coder"
	return manifest
}

func newLocalModeVerifyServer(t *testing.T, manifest localModeManifest, evidence localModeEvidence) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/config":
			_, _ = w.Write([]byte(`{}`))
		case "/api/workspaces/LOCALMODE/agents":
			writeTestJSON(t, w, map[string]any{"data": []map[string]any{
				promptAgentFixture(manifest.PlanAgentID, manifest.PlanAgent, manifest.PlanRole, manifest.PlanBinding, evidence.agentCreatedAt),
				promptAgentFixture(manifest.CodeAgentID, manifest.CodeAgent, manifest.CodeRole, manifest.CodeBinding, evidence.agentCreatedAt),
			}})
		case "/api/workspaces/LOCALMODE/issues/" + manifest.PlanTaskID:
			writeTestJSON(t, w, map[string]any{"data": map[string]any{
				"id": manifest.PlanTaskID, "title": manifest.PlanTaskName, "status": "review",
				"design": "Approved design", "assignee": evidence.planAssignee, "created_at": evidence.taskCreatedAt,
			}})
		case "/api/workspaces/LOCALMODE/issues/" + manifest.CodeTaskID:
			status := "closed"
			externalRef := ""
			if manifest.Plane == "ts" {
				status = "review"
				externalRef = "local-branch:loom/" + manifest.CodeTaskID + "@0123456789abcdef0123456789abcdef01234567"
			}
			writeTestJSON(t, w, map[string]any{"data": map[string]any{
				"id": manifest.CodeTaskID, "title": manifest.CodeTaskName, "status": status,
				"external_ref": externalRef, "assignee": "", "created_at": evidence.taskCreatedAt,
			}})
		case "/api/workspaces/LOCALMODE/tasks/" + manifest.PlanTaskID + "/sessions":
			writeTestJSON(t, w, map[string]any{"data": map[string]any{"sessions": []map[string]any{{
				"session_id": "plan-session", "status": "completed", "is_active": false,
				"has_transcript": true, "backend": manifest.Backend, "exit_code": 0,
				"started_at": evidence.sessionStartedAt, "ended_at": evidence.sessionStartedAt,
			}}}})
		case "/api/workspaces/LOCALMODE/tasks/" + manifest.CodeTaskID + "/sessions":
			writeTestJSON(t, w, map[string]any{"data": map[string]any{"sessions": []map[string]any{{
				"session_id": "code-session", "status": "completed", "is_active": false,
				"has_transcript": true, "has_diff": true, "files_changed": 1, "backend": manifest.Backend, "exit_code": 0,
				"started_at": evidence.sessionStartedAt, "ended_at": evidence.sessionStartedAt,
			}}}})
		case "/api/workspaces/LOCALMODE/tasks/" + manifest.PlanTaskID + "/sessions/plan-session/transcript",
			"/api/workspaces/LOCALMODE/tasks/" + manifest.CodeTaskID + "/sessions/code-session/transcript":
			writeTestJSON(t, w, map[string]any{"data": map[string]any{"entries": []map[string]any{{"type": "assistant", "text": "done"}}}})
		case "/api/workspaces/LOCALMODE/tasks/" + manifest.CodeTaskID + "/sessions/code-session/diff":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("diff --git a/local-mode-agent-output.txt b/local-mode-agent-output.txt\n"))
		default:
			http.Error(w, fmt.Sprintf("unexpected path %s", r.URL.Path), http.StatusNotFound)
		}
	}))
}

func promptAgentFixture(id, name, role, binding, createdAt string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "kind": "prompt", "enabled": true, "created_at": createdAt,
		"behavior": map[string]any{"role_name": role},
		"bindings": []map[string]any{{
			"binding_id": binding, "source_kind": "internal", "enabled": true,
			"event_type_patterns": []string{"internal.task.ready"},
		}},
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func runVerifier(t *testing.T, apiURL string, manifest localModeManifest, overrides map[string]string) (string, error) {
	t.Helper()
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "verify-local-mode.sh") //nolint:norawexec -- the test exercises the verifier as a real shell entrypoint
	env := filteredEnvironment(
		"LOCAL_MODE_API_URL",
		"LOCAL_MODE_RUN_MANIFEST_JSON",
		"LOCAL_MODE_CHECKOUT_ID",
		"LOCAL_MODE_SOURCE_ROOT",
		"LOCAL_MODE_COMPOSE_PROJECT",
		"LOCAL_MODE_EXPECTED_BACKEND",
		"LOCAL_MODE_VERIFY_TIMEOUT",
		"LOCAL_MODE_VERIFY_POLL_SECONDS",
	)
	values := map[string]string{
		"LOCAL_MODE_API_URL":             apiURL,
		"LOCAL_MODE_RUN_MANIFEST_JSON":   string(rawManifest),
		"LOCAL_MODE_CHECKOUT_ID":         manifest.CheckoutID,
		"LOCAL_MODE_SOURCE_ROOT":         manifest.SourceRoot,
		"LOCAL_MODE_COMPOSE_PROJECT":     manifest.Project,
		"LOCAL_MODE_VERIFY_TIMEOUT":      "1",
		"LOCAL_MODE_VERIFY_POLL_SECONDS": "0.05",
	}
	for key, value := range overrides {
		values[key] = value
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	out, runErr := cmd.CombinedOutput()
	return string(out), runErr
}

func filteredEnvironment(keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key] = struct{}{}
	}
	out := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, skip := blocked[key]; !skip {
			out = append(out, entry)
		}
	}
	return out
}
