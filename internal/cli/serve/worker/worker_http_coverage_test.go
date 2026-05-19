package worker

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
)

func TestRegisterWorkerSendsPayloadAndDecodesToken(t *testing.T) {
	var gotAuth string
	var gotReq workerRegistration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/workers/register" {
			t.Fatalf("path = %s, want register endpoint", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(workerRegistration{
			WorkerID:  "worker-1",
			Workspace: gotReq.Workspace,
			Agent:     gotReq.Agent,
			Backend:   gotReq.Backend,
			Token:     "server-token",
		})
	}))
	defer server.Close()

	reg, err := registerWorker(server.URL, "client-token", workerRegistration{
		Workspace: "WS",
		Agent:     "nova",
		Backend:   "codex",
	})
	if err != nil {
		t.Fatalf("registerWorker: %v", err)
	}
	if gotAuth != "Bearer client-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotReq.Workspace != "WS" || gotReq.Agent != "nova" || gotReq.Backend != "codex" {
		t.Fatalf("request = %+v, want workspace/agent/backend", gotReq)
	}
	if reg.WorkerID != "worker-1" || reg.Token != "server-token" {
		t.Fatalf("response = %+v, want decoded registration", reg)
	}
}

func TestRegisterWorkerErrorBranches(t *testing.T) {
	statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer statusServer.Close()
	if _, err := registerWorker(statusServer.URL, "", workerRegistration{}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("status error = %v, want 403", err)
	}

	badJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not-json")
	}))
	defer badJSONServer.Close()
	if _, err := registerWorker(badJSONServer.URL, "", workerRegistration{}); err == nil || !strings.Contains(err.Error(), "decode registration response") {
		t.Fatalf("decode error = %v, want decode registration response", err)
	}

	if _, err := registerWorker(":// bad-url", "", workerRegistration{}); err == nil || !strings.Contains(err.Error(), "create request") {
		t.Fatalf("bad URL error = %v, want create request", err)
	}
}

func TestDeregisterWorkerSendsDeleteAndLogsFailures(t *testing.T) {
	var gotMethod, gotAuth, gotPath string
	successServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer successServer.Close()

	deregisterWorker(successServer.URL, "server-token", "worker-9")
	if gotMethod != http.MethodDelete || gotPath != "/api/internal/workers/worker-9" {
		t.Fatalf("request method/path = %s %s, want DELETE worker endpoint", gotMethod, gotPath)
	}
	if gotAuth != "Bearer server-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}

	var logBuf strings.Builder
	oldWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	deregisterWorker(":// bad-url", "", "worker-9")
	if !strings.Contains(logBuf.String(), "failed to create deregistration request") {
		t.Fatalf("log = %q, want create request warning", logBuf.String())
	}

	logBuf.Reset()
	failureServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failureServer.Close()
	deregisterWorker(failureServer.URL, "", "worker-9")
	if !strings.Contains(logBuf.String(), "deregistration returned 500") {
		t.Fatalf("log = %q, want status warning", logBuf.String())
	}
}

func TestWorkerHelpersConfigureInterfacesAndCleanup(t *testing.T) {
	reg := &workerRegistration{WorkerID: "worker-2"}
	oldOutput := log.Writer()
	lockBridge, eventEmitter, logForwarder := setupWorkerInterfaces(reg, "auth-token")
	t.Cleanup(func() { log.SetOutput(oldOutput) })

	if lockBridge.ControlPlaneURL != workerControlPlane || lockBridge.WorkerID != "worker-2" || lockBridge.Token != "auth-token" {
		t.Fatalf("lock bridge = %+v, want worker interface config", lockBridge)
	}
	if eventEmitter.ControlPlaneURL != workerControlPlane || eventEmitter.WorkerID != "worker-2" || eventEmitter.Token != "auth-token" {
		t.Fatalf("event emitter = %+v, want worker interface config", eventEmitter)
	}
	if logForwarder.workerID != "worker-2" || logForwarder.token != "auth-token" {
		t.Fatalf("log forwarder = %+v, want worker interface config", logForwarder)
	}
	if err := logForwarder.Close(); err != nil {
		t.Fatalf("Close log forwarder: %v", err)
	}

	oldControlPlane := workerControlPlane
	workerControlPlane = "http://127.0.0.1:1"
	t.Cleanup(func() { workerControlPlane = oldControlPlane })
	cleanupWorkerResources(NewLogForwarder("http://127.0.0.1:1", "worker-3", ""), &automode.HTTPEventEmitter{
		ControlPlaneURL: "http://127.0.0.1:1",
		WorkerID:        "worker-3",
	}, "", "worker-3")
}

func TestPrintWorkerBannerAndResolveNamedWorkspaceFromConfig(t *testing.T) {
	oldControlPlane, oldWorkspace, oldAgent, oldBackend := workerControlPlane, workerWorkspace, workerAgent, workerBackend
	workerControlPlane = "http://control"
	workerWorkspace = "WS"
	workerAgent = "nova"
	workerBackend = "codex"
	t.Cleanup(func() {
		workerControlPlane, workerWorkspace, workerAgent, workerBackend = oldControlPlane, oldWorkspace, oldAgent, oldBackend
	})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	printWorkerBanner("/tmp/worktree")
	_ = w.Close()
	os.Stdout = oldStdout
	t.Cleanup(func() { _ = r.Close() })
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll stdout: %v", err)
	}
	if got := string(out); !strings.Contains(got, "LOOM WORKER") || !strings.Contains(got, "http://control") || !strings.Contains(got, "/tmp/worktree") {
		t.Fatalf("banner output = %q, want worker details", got)
	}
}

func TestValidateWorkerFlagsSuccess(t *testing.T) {
	oldControlPlane, oldWorkspace, oldAgent, oldBackend := workerControlPlane, workerWorkspace, workerAgent, workerBackend
	workerControlPlane = "http://control"
	workerWorkspace = "WS"
	workerAgent = "nova"
	workerBackend = ""
	t.Setenv("LOOM_WORKER_TOKEN", "env-token")
	t.Cleanup(func() {
		workerControlPlane, workerWorkspace, workerAgent, workerBackend = oldControlPlane, oldWorkspace, oldAgent, oldBackend
	})

	token, worktreePath := validateWorkerFlags()
	if token != "env-token" {
		t.Fatalf("token = %q, want env-token", token)
	}
	if worktreePath == "" {
		t.Fatalf("worktreePath is empty")
	}
}

func TestRegisterAndGetTokenPrefersServerToken(t *testing.T) {
	oldControlPlane, oldWorkspace, oldAgent, oldBackend := workerControlPlane, workerWorkspace, workerAgent, workerBackend
	workerWorkspace = "WS"
	workerAgent = "nova"
	workerBackend = "codex"
	t.Cleanup(func() {
		workerControlPlane, workerWorkspace, workerAgent, workerBackend = oldControlPlane, oldWorkspace, oldAgent, oldBackend
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(workerRegistration{WorkerID: "worker-4", Token: "server-token"})
	}))
	defer server.Close()
	workerControlPlane = server.URL

	reg, token := registerAndGetToken("client-token")
	if reg.WorkerID != "worker-4" || token != "server-token" {
		t.Fatalf("reg=%+v token=%q, want server token", reg, token)
	}
}

func TestWorkerRegistrationInterfacesAreAssignable(t *testing.T) {
	var _ cli.LockBridge = (*cli.HTTPLockBridge)(nil)
	ch := setupWorkerShutdown()
	select {
	case <-ch:
		t.Fatalf("shutdown channel closed without a signal")
	case <-time.After(10 * time.Millisecond):
	}
}
