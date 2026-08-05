package localmode_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	fleetbackend "github.com/tysonthomas9/loomcli/internal/backend/fleet"
	issuehandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	webservice "github.com/tysonthomas9/loomcli/internal/webui/service"
)

type provenanceMarker struct {
	CheckoutID string `json:"checkout_id"`
	SourceRoot string `json:"source_root"`
	Project    string `json:"compose_project"`
}

type runManifest struct {
	CheckoutID   string `json:"checkout_id"`
	SourceRoot   string `json:"source_root"`
	Project      string `json:"compose_project"`
	RunID        string `json:"run_id"`
	StartedAt    string `json:"started_at"`
	Workspace    string `json:"workspace"`
	Backend      string `json:"backend"`
	PlanTaskID   string `json:"plan_task_id"`
	CodeTaskID   string `json:"code_task_id"`
	PlanTaskName string `json:"plan_task_title"`
	CodeTaskName string `json:"code_task_title"`
}

func TestLocalModeTSPromptsLeaveLifecycleToHost(t *testing.T) {
	raw, err := os.ReadFile("local-mode-entrypoint")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, want := range []string{
		"Do not change status or assignee; the workflow host moves it to review after the terminal receipt.",
		"Do not close or release the task; the workflow host owns terminalization.",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("local-mode TS prompt missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"move it to review, and make no repository changes",
		"and complete the task.",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("local-mode TS prompt still delegates lifecycle mutation: %q", forbidden)
		}
	}
}

func TestLocalModeProfileAllowsUIAgentCatalogProof(t *testing.T) {
	raw, err := os.ReadFile("local-mode-entrypoint")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "loom daemon profile set max_agents 20") {
		t.Fatal("local-mode profile must retain enough capacity to exercise every UI-created agent template")
	}
}

func TestWorkspaceDaemonPIDRejectsReusedForeignProcess(t *testing.T) {
	workspace := t.TempDir()
	fakeBin := t.TempDir()
	fakeLoom := filepath.Join(fakeBin, "loom")
	fakeCodex := filepath.Join(fakeBin, "codex")
	for _, path := range []string{fakeLoom, fakeCodex} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".loom"), 0o755); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	if err := os.WriteFile(
		filepath.Join(workspace, ".loom", "daemon.pid"),
		[]byte(strconv.Itoa(pid)+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		argv    []string
		cwd     string
		exe     string
		wantPID bool
	}{
		{
			name:    "matching workspace daemon",
			argv:    []string{"/usr/local/bin/loom", "daemon"},
			cwd:     workspace,
			exe:     fakeLoom,
			wantPID: true,
		},
		{
			name: "foreign codex process reusing pid",
			argv: []string{
				"/usr/local/bin/codex",
				"--remote",
				"ws://127.0.0.1:8080/api/workspaces/LOCALMODE/terminal",
			},
			cwd: workspace,
			exe: fakeCodex,
		},
		{
			name: "different loom subcommand",
			argv: []string{"/usr/local/bin/loom", "serve", "--fleet-mode"},
			cwd:  workspace,
			exe:  fakeLoom,
		},
		{
			name: "different loom subcommand mentioning daemon later",
			argv: []string{"/usr/local/bin/loom", "serve", "daemon"},
			cwd:  workspace,
			exe:  fakeLoom,
		},
		{
			name: "daemon for another workspace",
			argv: []string{"/usr/local/bin/loom", "daemon"},
			cwd:  t.TempDir(),
			exe:  fakeLoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			procRoot := t.TempDir()
			procDir := filepath.Join(procRoot, strconv.Itoa(pid))
			if err := os.MkdirAll(procDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(procDir, "cmdline"),
				[]byte(strings.Join(tt.argv, "\x00")+"\x00"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(tt.cwd, filepath.Join(procDir, "cwd")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(tt.exe, filepath.Join(procDir, "exe")); err != nil {
				t.Fatal(err)
			}

			out, err := runWorkspaceDaemonPIDCheck(t, workspace, procRoot, fakeBin)
			if tt.wantPID {
				if err != nil {
					t.Fatalf("workspace daemon pid check: %v\n%s", err, out)
				}
				if got := strings.TrimSpace(out); got != strconv.Itoa(pid) {
					t.Fatalf("workspace daemon pid = %q, want %d", got, pid)
				}
				return
			}
			if err == nil {
				t.Fatalf("foreign process was accepted as workspace daemon: %q", strings.TrimSpace(out))
			}
		})
	}
}

func TestWorkspaceDaemonPIDFailsClosedWithoutProcessIdentity(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".loom"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workspace, ".loom", "daemon.pid"),
		[]byte(strconv.Itoa(os.Getpid())+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if out, err := runWorkspaceDaemonPIDCheck(t, workspace, t.TempDir(), t.TempDir()); err == nil {
		t.Fatalf("missing process identity was accepted as workspace daemon: %q", strings.TrimSpace(out))
	}
}

func TestWorkspaceDaemonPIDRejectsMalformedPID(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".loom"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{"12x34\n", "12 34\n", "0\n", "-1\n"} {
		t.Run(strings.TrimSpace(malformed), func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(workspace, ".loom", "daemon.pid"), []byte(malformed), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := runWorkspaceDaemonPIDCheck(t, workspace, t.TempDir(), t.TempDir()); err == nil {
				t.Fatalf("malformed pid %q was accepted: %q", malformed, strings.TrimSpace(out))
			}
		})
	}
}

func TestStopWorkspaceDaemonsLeavesReusedForeignProcessAlive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("local-mode daemon manager validates process identity through Linux /proc")
	}

	foreign := exec.Command("/bin/sleep", "30") //nolint:norawexec // real foreign process for PID-reuse cleanup regression
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = foreign.Process.Kill()
		_ = foreign.Wait()
	})

	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".loom"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workspace, ".loom", "daemon.pid"),
		[]byte(strconv.Itoa(foreign.Process.Pid)+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	fakeBin := t.TempDir()
	fakeLoom := filepath.Join(fakeBin, "loom")
	if err := os.WriteFile(fakeLoom, []byte(`#!/bin/sh
if [ "$1" = "workspace" ] && [ "$2" = "list" ]; then
  printf '[{"path":"%s"}]\n' "$FAKE_WORKSPACE_PATH"
  exit 0
fi
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runStopWorkspaceDaemons(t, workspace, fakeBin)
	if err != nil {
		t.Fatalf("stop workspace daemons: %v\n%s", err, out)
	}
	if err := foreign.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("foreign reused PID %d was killed by cleanup: %v", foreign.Process.Pid, err)
	}
}

func TestLocalModeEntrypointProvenanceCreatesAndAcceptsMarker(t *testing.T) {
	configDir := t.TempDir()
	want := provenanceMarker{
		CheckoutID: "checkout-a",
		SourceRoot: "/workspace/checkouts/a",
		Project:    "loomcli-local-mode-a",
	}

	if out, err := runProvenanceCheck(t, configDir, want); err != nil {
		t.Fatalf("create provenance marker: %v\n%s", err, out)
	}

	markerPath := filepath.Join(configDir, ".local-mode-provenance.json")
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read provenance marker: %v", err)
	}
	var got provenanceMarker
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode provenance marker: %v", err)
	}
	if got != want {
		t.Fatalf("provenance marker mismatch: got %+v want %+v", got, want)
	}
	info, err := os.Stat(markerPath)
	if err != nil {
		t.Fatalf("stat provenance marker: %v", err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("provenance marker mode = %o, want 600", gotMode)
	}

	if out, err := runProvenanceCheck(t, configDir, want); err != nil {
		t.Fatalf("accept matching provenance marker: %v\n%s", err, out)
	}
}

func TestLocalModeEntrypointProvenanceRejectsUnmarkedData(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "legacy-state.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runProvenanceCheck(t, configDir, provenanceMarker{
		CheckoutID: "checkout-a",
		SourceRoot: "/workspace/checkouts/a",
		Project:    "loomcli-local-mode-a",
	})
	if err == nil {
		t.Fatalf("unmarked nonempty data unexpectedly accepted:\n%s", out)
	}
	if !strings.Contains(out, "existing local-mode data has no checkout provenance marker") {
		t.Fatalf("unmarked-data failure was not explicit:\n%s", out)
	}
}

func TestLocalModeEntrypointProvenanceRejectsMismatch(t *testing.T) {
	base := provenanceMarker{
		CheckoutID: "checkout-a",
		SourceRoot: "/workspace/checkouts/a",
		Project:    "loomcli-local-mode-a",
	}
	tests := map[string]provenanceMarker{
		"checkout": {CheckoutID: "checkout-b", SourceRoot: base.SourceRoot, Project: base.Project},
		"root":     {CheckoutID: base.CheckoutID, SourceRoot: "/workspace/checkouts/b", Project: base.Project},
		"project":  {CheckoutID: base.CheckoutID, SourceRoot: base.SourceRoot, Project: "loomcli-local-mode-b"},
	}

	for name, mismatch := range tests {
		t.Run(name, func(t *testing.T) {
			configDir := t.TempDir()
			if out, err := runProvenanceCheck(t, configDir, base); err != nil {
				t.Fatalf("create provenance marker: %v\n%s", err, out)
			}
			out, err := runProvenanceCheck(t, configDir, mismatch)
			if err == nil {
				t.Fatalf("mismatched provenance unexpectedly accepted:\n%s", out)
			}
			if !strings.Contains(out, "local-mode volume provenance mismatch") {
				t.Fatalf("mismatch failure was not explicit:\n%s", out)
			}
		})
	}
}

func TestLocalModeEntrypointRestartJournalPreservesAndInvalidatesProofEpoch(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "run.json")
	recoveryPath := manifestPath + ".recovery"
	manifest := runManifest{
		CheckoutID:   "checkout-a",
		SourceRoot:   "/workspace/checkouts/a",
		Project:      "loomcli-local-mode-a",
		RunID:        "restart-proof",
		StartedAt:    "2026-07-15T06:00:00Z",
		Workspace:    "LOCALMODE",
		Backend:      "codex",
		PlanTaskID:   "LOCALMODE-10",
		CodeTaskID:   "LOCALMODE-11",
		PlanTaskName: "Local mode planner dogfood [run:restart-proof]",
		CodeTaskName: "Local mode coder dogfood [run:restart-proof]",
	}
	writeRunManifest(t, manifestPath, manifest)

	out, err := runManifestPreparation(t, manifestPath, manifest)
	if err != nil {
		t.Fatalf("prepare matching restart manifest: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != manifest.StartedAt {
		t.Fatalf("preserved started_at = %q, want %q", strings.TrimSpace(out), manifest.StartedAt)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("live manifest remained readable during restart: err=%v", err)
	}
	assertRunManifest(t, recoveryPath, manifest)

	// Simulate another interruption before startup can publish a replacement
	// live manifest. The verifier-invisible journal must survive and preserve
	// the original threshold and exact task IDs.
	out, err = runManifestPreparation(t, manifestPath, manifest)
	if err != nil {
		t.Fatalf("resume interrupted restart journal: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != manifest.StartedAt {
		t.Fatalf("resumed started_at = %q, want %q", strings.TrimSpace(out), manifest.StartedAt)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("live manifest reappeared during interrupted restart: err=%v", err)
	}
	assertRunManifest(t, recoveryPath, manifest)

	mismatch := manifest
	mismatch.CheckoutID = "checkout-b"
	out, err = runManifestPreparation(t, manifestPath, mismatch)
	if err == nil {
		t.Fatalf("mismatched restart manifest unexpectedly accepted:\n%s", out)
	}
	if !strings.Contains(out, "stale proof has been invalidated") {
		t.Fatalf("mismatch failure was not actionable:\n%s", out)
	}
	if _, statErr := os.Stat(manifestPath); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched manifest remained readable: err=%v", statErr)
	}
	if _, statErr := os.Stat(recoveryPath); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched recovery journal remained readable: err=%v", statErr)
	}
}

func TestLocalModeEntrypointNewRunStartsFreshProofEpoch(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "run.json")
	previous := runManifest{
		CheckoutID:   "checkout-a",
		SourceRoot:   "/workspace/checkouts/a",
		Project:      "loomcli-local-mode-a",
		RunID:        "previous-proof",
		StartedAt:    "2026-07-15T06:00:00Z",
		Workspace:    "LOCALMODE",
		Backend:      "codex",
		PlanTaskID:   "LOCALMODE-10",
		CodeTaskID:   "LOCALMODE-11",
		PlanTaskName: "Local mode planner dogfood [run:previous-proof]",
		CodeTaskName: "Local mode coder dogfood [run:previous-proof]",
	}
	writeRunManifest(t, manifestPath, previous)

	current := previous
	current.RunID = "current-proof"
	current.PlanTaskName = "Local mode planner dogfood [run:current-proof]"
	current.CodeTaskName = "Local mode coder dogfood [run:current-proof]"
	out, err := runManifestPreparation(t, manifestPath, current)
	if err != nil {
		t.Fatalf("prepare fresh run manifest: %v\n%s", err, out)
	}
	if !strings.Contains(out, "starting fresh proof epoch current-proof after previous-proof") {
		t.Fatalf("fresh proof epoch was not reported:\n%s", out)
	}
	if strings.Contains(out, previous.StartedAt) {
		t.Fatalf("fresh proof epoch inherited prior started_at:\n%s", out)
	}
	for _, path := range []string{manifestPath, manifestPath + ".recovery"} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("old proof artifact %s remained readable: err=%v", path, statErr)
		}
	}
}

func TestLocalModeEntrypointTitleLookupUsesFleetSearchBeyondFirstPage(t *testing.T) {
	t.Parallel()
	const (
		runTitle      = "Local mode planner dogfood [run:lookup proof + symbols?]"
		deferredTitle = "Backlog grooming placeholder"
	)
	issues := make([]map[string]any, 0, 252)
	for i := 0; i < 250; i++ {
		issues = append(issues, map[string]any{
			"id": fmt.Sprintf("LOCALMODE-%d", i+1), "title": fmt.Sprintf("Persisted task %03d", i+1),
			"type": "task", "status": "open",
		})
	}
	issues = append(issues,
		map[string]any{"id": "LOCALMODE-251", "title": runTitle, "type": "task", "status": "open"},
		map[string]any{"id": "LOCALMODE-252", "title": deferredTitle, "type": "task", "status": "deferred"},
	)

	var searchRequests atomic.Int32
	var listRequests atomic.Int32
	fleetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("fleet method = %s, want GET", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/api/v1/LOCALMODE/issues/search":
			searchRequests.Add(1)
			query := r.URL.Query().Get("q")
			if query == "" {
				t.Error("fleet search request omitted q")
				http.Error(w, "missing q", http.StatusBadRequest)
				return
			}
			if legacy := r.URL.Query().Get("query"); legacy != "" {
				t.Errorf("fleet search used unsupported query parameter: %q", legacy)
			}
			filtered := make([]map[string]any, 0, 1)
			for _, issue := range issues {
				if issue["title"] == query {
					filtered = append(filtered, issue)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{"issues": filtered, "count": len(filtered)}); err != nil {
				t.Errorf("encode fleet search response: %v", err)
			}
		case "/api/v1/LOCALMODE/issues":
			// Match fleet-db list semantics closely enough to prove the old
			// /issues?query= adapter cannot see either target beyond page one.
			listRequests.Add(1)
			page := issues
			if len(page) > 200 {
				page = page[:200]
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{"issues": page, "count": len(page)}); err != nil {
				t.Errorf("encode fleet list response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fleetServer.Close)
	fleetIssueBackend, err := fleetbackend.New(fleetbackend.Config{
		BaseURL: fleetServer.URL, WorkspaceID: "LOCALMODE", HTTPClient: fleetServer.Client(),
	})
	if err != nil {
		t.Fatalf("create fleet backend: %v", err)
	}

	svc := webservice.NewIssueServiceWithBackend(nil, nil, nil, func(context.Context) backend.IssueBackend {
		return fleetIssueBackend
	})
	mux := http.NewServeMux()
	issuehandlers.NewIssueModule(svc, nil).Register(mux)
	loomServer := httptest.NewServer(mux)
	t.Cleanup(loomServer.Close)
	loomURL, err := url.Parse(loomServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	lookup := func(title, issueType, wantID string) {
		t.Helper()
		cmd := exec.Command("bash", "local-mode-entrypoint", "--find-issue-title-only", title, issueType) //nolint:norawexec -- exercise entrypoint -> WebUI service -> Fleet adapter
		cmd.Env = filteredEnvironment(
			"HOME", "LOOM_WORKSPACE", "LOOM_LOCAL_MODE_API_PORT",
			"LOCAL_MODE_CHECKOUT_ID", "LOCAL_MODE_SOURCE_ROOT", "LOCAL_MODE_COMPOSE_PROJECT", "LOCAL_MODE_RUN_ID",
		)
		for key, value := range map[string]string{
			"HOME": t.TempDir(), "LOOM_WORKSPACE": "LOCALMODE", "LOOM_LOCAL_MODE_API_PORT": loomURL.Port(),
			"LOCAL_MODE_CHECKOUT_ID": "checkout-a", "LOCAL_MODE_SOURCE_ROOT": "/workspace/checkouts/a",
			"LOCAL_MODE_COMPOSE_PROJECT": "loomcli-local-mode-a", "LOCAL_MODE_RUN_ID": "lookup-proof",
		} {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		out, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			t.Fatalf("lookup %q through fleet search: %v\n%s", title, cmdErr, out)
		}
		if got := strings.TrimSpace(string(out)); got != wantID {
			t.Fatalf("lookup %q = %q, want %q", title, got, wantID)
		}
	}

	lookup(runTitle, "task", "LOCALMODE-251")
	lookup(deferredTitle, "task", "LOCALMODE-252")
	if gotSearch, gotList := searchRequests.Load(), listRequests.Load(); gotSearch != 2 || gotList != 0 {
		t.Fatalf("fleet requests: search=%d list=%d, want search=2 list=0", gotSearch, gotList)
	}
}

func assertRunManifest(t *testing.T, path string, want runManifest) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run manifest %s: %v", path, err)
	}
	var got runManifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode run manifest %s: %v", path, err)
	}
	if got != want {
		t.Fatalf("run manifest %s mismatch: got %+v want %+v", path, got, want)
	}
}

func writeRunManifest(t *testing.T, path string, manifest runManifest) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runManifestPreparation(t *testing.T, manifestPath string, manifest runManifest) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "local-mode-entrypoint", "--prepare-run-manifest-only") //nolint:norawexec -- exercise the real container entrypoint contract
	cmd.Env = filteredEnvironment(
		"HOME",
		"LOOM_BACKEND",
		"LOOM_LOCAL_MODE_BACKEND",
		"LOOM_WORKSPACE",
		"LOCAL_MODE_CHECKOUT_ID",
		"LOCAL_MODE_SOURCE_ROOT",
		"LOCAL_MODE_COMPOSE_PROJECT",
		"LOCAL_MODE_RUN_ID",
		"LOCAL_MODE_RUN_MANIFEST",
	)
	values := map[string]string{
		"HOME":                       t.TempDir(),
		"LOOM_LOCAL_MODE_BACKEND":    manifest.Backend,
		"LOOM_WORKSPACE":             manifest.Workspace,
		"LOCAL_MODE_CHECKOUT_ID":     manifest.CheckoutID,
		"LOCAL_MODE_SOURCE_ROOT":     manifest.SourceRoot,
		"LOCAL_MODE_COMPOSE_PROJECT": manifest.Project,
		"LOCAL_MODE_RUN_ID":          manifest.RunID,
		"LOCAL_MODE_RUN_MANIFEST":    manifestPath,
	}
	for key, value := range values {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runProvenanceCheck(t *testing.T, configDir string, marker provenanceMarker) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "local-mode-entrypoint", "--check-provenance-only") //nolint:norawexec -- exercise the real container entrypoint contract
	cmd.Env = filteredEnvironment(
		"HOME",
		"LOOM_CONFIG_DIR",
		"LOCAL_MODE_CHECKOUT_ID",
		"LOCAL_MODE_SOURCE_ROOT",
		"LOCAL_MODE_COMPOSE_PROJECT",
		"LOCAL_MODE_RUN_ID",
	)
	values := map[string]string{
		"HOME":                       t.TempDir(),
		"LOOM_CONFIG_DIR":            configDir,
		"LOCAL_MODE_CHECKOUT_ID":     marker.CheckoutID,
		"LOCAL_MODE_SOURCE_ROOT":     marker.SourceRoot,
		"LOCAL_MODE_COMPOSE_PROJECT": marker.Project,
		"LOCAL_MODE_RUN_ID":          "provenance-unit-test",
	}
	for key, value := range values {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runWorkspaceDaemonPIDCheck(t *testing.T, workspace, procRoot, fakeBin string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "local-mode-entrypoint", "--workspace-daemon-pid-only", workspace, procRoot) //nolint:norawexec -- exercise the real container entrypoint contract
	cmd.Env = filteredEnvironment(
		"HOME",
		"PATH",
		"LOCAL_MODE_CHECKOUT_ID",
		"LOCAL_MODE_SOURCE_ROOT",
		"LOCAL_MODE_COMPOSE_PROJECT",
		"LOCAL_MODE_RUN_ID",
	)
	for key, value := range map[string]string{
		"HOME":                       t.TempDir(),
		"PATH":                       fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"LOCAL_MODE_CHECKOUT_ID":     "checkout-daemon-pid",
		"LOCAL_MODE_SOURCE_ROOT":     "/workspace/checkouts/daemon-pid",
		"LOCAL_MODE_COMPOSE_PROJECT": "loomcli-daemon-pid",
		"LOCAL_MODE_RUN_ID":          "daemon-pid-proof",
	} {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runStopWorkspaceDaemons(t *testing.T, workspace, fakeBin string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "local-mode-entrypoint", "--stop-workspace-daemons-only") //nolint:norawexec -- exercise the real cleanup path
	cmd.Env = filteredEnvironment(
		"HOME",
		"PATH",
		"FAKE_WORKSPACE_PATH",
		"LOCAL_MODE_CHECKOUT_ID",
		"LOCAL_MODE_SOURCE_ROOT",
		"LOCAL_MODE_COMPOSE_PROJECT",
		"LOCAL_MODE_RUN_ID",
	)
	for key, value := range map[string]string{
		"HOME":                       t.TempDir(),
		"PATH":                       fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_WORKSPACE_PATH":        workspace,
		"LOCAL_MODE_CHECKOUT_ID":     "checkout-daemon-cleanup",
		"LOCAL_MODE_SOURCE_ROOT":     "/workspace/checkouts/daemon-cleanup",
		"LOCAL_MODE_COMPOSE_PROJECT": "loomcli-daemon-cleanup",
		"LOCAL_MODE_RUN_ID":          "daemon-cleanup-proof",
	} {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
