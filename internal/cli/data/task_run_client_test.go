package data

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func clearTaskRunDataEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		envTaskRunAPIURL, envTaskRunID, envTaskID, envTaskRunNodeID,
		envTaskRunLeaseID, envTaskRunLeaseToken, envRunnerLeaseToken,
		envTaskRunFencingToken, envDriverWorkspace, "LOOM_WORKSPACE",
	} {
		t.Setenv(name, "")
	}
}

func setTaskRunDataEnv(t *testing.T, apiURL string) {
	t.Helper()
	clearTaskRunDataEnv(t)
	t.Setenv(envTaskRunAPIURL, apiURL)
	t.Setenv(envTaskRunID, "task-run-1")
	t.Setenv(envTaskID, "TASK-1")
	t.Setenv(envTaskRunNodeID, "node-1")
	t.Setenv(envTaskRunLeaseID, "lease-1")
	t.Setenv(envTaskRunLeaseToken, "lease-secret")
	t.Setenv(envTaskRunFencingToken, "42")
	t.Setenv(envDriverWorkspace, "WS")
}

func preserveUpdateFlagState(t *testing.T) {
	t.Helper()
	previous := map[string]bool{}
	updateCmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		previous[flag.Name] = flag.Changed
		flag.Changed = false
	})
	t.Cleanup(func() {
		updateCmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
			flag.Changed = previous[flag.Name]
		})
	})
}

func TestTaskRunDataEnvironmentFailsClosed(t *testing.T) {
	clearTaskRunDataEnv(t)
	client, active, err := taskRunDataClientFromEnv()
	if err != nil || active || client != nil {
		t.Fatalf("outside TaskRun = client=%v active=%v err=%v, want inactive", client, active, err)
	}

	t.Setenv(envTaskRunID, "task-run-1")
	client, active, err = taskRunDataClientFromEnv()
	if !active || err == nil || client != nil {
		t.Fatalf("partial TaskRun env = client=%v active=%v err=%v, want fail closed", client, active, err)
	}
	if !strings.Contains(err.Error(), envTaskRunAPIURL) {
		t.Fatalf("partial env error = %q, want missing API URL", err)
	}
}

func TestTaskRunDataCommandScopeAllowlistCoversEveryRunnableLeaf(t *testing.T) {
	clearTaskRunDataEnv(t)
	t.Setenv(envTaskRunID, "task-run-1")

	runnable := runnableDataCommandLeaves(dataRootCmd)
	if len(runnable) == 0 {
		t.Fatal("data command tree has no runnable leaves")
	}
	for _, command := range runnable {
		command := command
		t.Run(command.CommandPath(), func(t *testing.T) {
			err := EnforceTaskRunCommandScope(command)
			allowed := command == showCmd || command == updateCmd
			if allowed && err != nil {
				t.Fatalf("allowed TaskRun data command rejected: %v", err)
			}
			if !allowed && (err == nil || !strings.Contains(err.Error(), "task-run data mode only permits")) {
				t.Fatalf("unsupported TaskRun data command error = %v, want scoped rejection", err)
			}
		})
	}
}

func TestTaskRunDataCommandScopeBlocksGenericBackendBeforeCall(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		setTaskRunDataEnv(t, "http://127.0.0.1:1")
		err := EnforceTaskRunCommandScope(closeCmd)
		if err == nil {
			_, err = captureDataStdout(t, func() error {
				return closeCmd.RunE(closeCmd, []string{"OTHER"})
			})
		}
		if err == nil || !strings.Contains(err.Error(), "task-run data mode only permits") {
			t.Fatalf("close escape error = %v, want TaskRun scope rejection", err)
		}
		if len(stub.calls) != 0 {
			t.Fatalf("generic backend calls = %#v, want none", stub.calls)
		}
	})
}

func runnableDataCommandLeaves(root *cobra.Command) []*cobra.Command {
	var leaves []*cobra.Command
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		if command.Run != nil || command.RunE != nil {
			leaves = append(leaves, command)
		}
		for _, child := range command.Commands() {
			visit(child)
		}
	}
	visit(root)
	return leaves
}

func TestTaskRunDataCommandsUseLeaseFacadeAndStayExactTaskDesignOnly(t *testing.T) {
	var getCalls, updateCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/WS/task-run/task-get", func(w http.ResponseWriter, r *http.Request) {
		getCalls.Add(1)
		assertTaskRunDataHeaders(t, r)
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode task-get: %v", err)
		}
		if input["taskId"] != "TASK-1" {
			t.Errorf("task-get body = %v, want exact task", input)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task": map[string]any{"id": "TASK-1", "title": "Plan this work", "status": "in_progress"},
		})
	})
	mux.HandleFunc("POST /api/workspaces/WS/task-run/task-design-update", func(w http.ResponseWriter, r *http.Request) {
		updateCalls.Add(1)
		assertTaskRunDataHeaders(t, r)
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode design update: %v", err)
		}
		requestID, _ := input["requestId"].(string)
		if !strings.HasPrefix(requestID, "task-run-design-update:") || input["design"] != "# Plan" || input["designFormat"] != "markdown" {
			t.Errorf("design update body = %v, want owner-derived design-only payload", input)
		}
		if _, present := input["taskId"]; present || len(input) != 3 {
			t.Errorf("design update body widened: %v", input)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"taskId": "TASK-1"})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	withDataClientState(t, func() {
		setTaskRunDataEnv(t, server.URL)
		preserveUpdateFlagState(t)
		serverURL = "http://127.0.0.1:1" // must be ignored inside a TaskRun
		outputFormat = "json"

		showOut, err := captureDataStdout(t, func() error {
			return showCmd.RunE(showCmd, []string{"TASK-1"})
		})
		if err != nil || !strings.Contains(showOut, `"id": "TASK-1"`) {
			t.Fatalf("task-run show output=%q err=%v", showOut, err)
		}
		if getCalls.Load() != 1 {
			t.Fatalf("task-get calls = %d, want 1", getCalls.Load())
		}

		updateDesign = "# Plan"
		updateDesignFormat = "markdown"
		setTestFlagChanged(t, updateCmd.Flags(), "design", true)
		setTestFlagChanged(t, updateCmd.Flags(), "design-format", true)
		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"TASK-1"})
		}); err != nil {
			t.Fatalf("task-run design update: %v", err)
		}
		if updateCalls.Load() != 1 {
			t.Fatalf("task-design-update calls = %d, want 1", updateCalls.Load())
		}

		setTestFlagChanged(t, updateCmd.Flags(), "status", true)
		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"TASK-1"})
		}); err == nil || !strings.Contains(err.Error(), "rejected --status") {
			t.Fatalf("status update error = %v, want design-only rejection", err)
		}
		if updateCalls.Load() != 1 {
			t.Fatalf("rejected status reached facade: calls=%d", updateCalls.Load())
		}

		if _, err := captureDataStdout(t, func() error {
			return showCmd.RunE(showCmd, []string{"TASK-2"})
		}); err == nil || !strings.Contains(err.Error(), "restricted to TASK-1") {
			t.Fatalf("foreign show error = %v, want exact-task rejection", err)
		}
		if getCalls.Load() != 1 {
			t.Fatalf("foreign task reached facade: calls=%d", getCalls.Load())
		}
	})
}

func TestTaskRunDataDesignRequestIdentityReplaysAfterLostResponse(t *testing.T) {
	var mu sync.Mutex
	var requestIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertTaskRunDataHeaders(t, r)
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode design update: %v", err)
		}
		if _, present := input["taskId"]; present {
			t.Errorf("design update exposed caller-selected task ID: %v", input)
		}
		requestID, _ := input["requestId"].(string)
		mu.Lock()
		requestIDs = append(requestIDs, requestID)
		call := len(requestIDs)
		mu.Unlock()
		if call == 1 {
			writeTaskRunDataTestError(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"taskId": "TASK-1", "actionId": "receipt"})
	}))
	t.Cleanup(server.Close)
	setTaskRunDataEnv(t, server.URL)
	client, active, err := taskRunDataClientFromEnv()
	if err != nil || !active {
		t.Fatalf("construct client: active=%v err=%v", active, err)
	}
	if err := client.updateDesign(context.Background(), "TASK-1", "# Plan", nil); err == nil {
		t.Fatal("lost first response unexpectedly succeeded")
	}
	markdown := "markdown"
	if err := client.updateDesign(context.Background(), "TASK-1", "# Plan", &markdown); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if err := client.updateDesign(context.Background(), "TASK-1", "# Plan\n", &markdown); err != nil {
		t.Fatalf("changed design: %v", err)
	}
	mu.Lock()
	got := append([]string(nil), requestIDs...)
	mu.Unlock()
	if len(got) != 3 || got[0] == "" || got[0] != got[1] || got[2] == got[1] {
		t.Fatalf("request IDs = %q, want exact retry replay and changed-design identity", got)
	}
}

func writeTaskRunDataTestError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": code, "retryable": true},
	})
}

func assertTaskRunDataHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("Authorization") != "Bearer lease-secret" ||
		r.Header.Get(taskRunIDHeader) != "task-run-1" ||
		r.Header.Get(taskRunNodeIDHeader) != "node-1" ||
		r.Header.Get(taskRunLeaseIDHeader) != "lease-1" ||
		r.Header.Get(taskRunFencingTokenHeader) != "42" {
		t.Errorf("task-run identity headers incomplete")
	}
}

func TestTaskRunDataClientRejectsForeignTaskBeforeHTTP(t *testing.T) {
	setTaskRunDataEnv(t, "http://127.0.0.1:1")
	client, active, err := taskRunDataClientFromEnv()
	if err != nil || !active {
		t.Fatalf("construct client: active=%v err=%v", active, err)
	}
	if _, err := client.getTask(context.Background(), "TASK-2"); err == nil {
		t.Fatal("foreign task get unexpectedly succeeded")
	}
}
