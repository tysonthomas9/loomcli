package taskrunapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const sessionE2ERunnerScript = `
import { TaskRunClient } from %q;

const runner = TaskRunClient.fromEnv(process.env);
const invocation = await runner.agent.exec({
  invocationKey: "agent",
  backend: "codex",
  model: "gpt-5",
  argv: [process.execPath, "-e", "console.log(JSON.stringify({type: 'completed', usage: {tokens: 7, cost: 0.02}}))"],
  transcript: "stream-json",
});
if (invocation.exitCode !== 0 || !invocation.session.closed || invocation.usage?.tokens !== 7) {
  console.error(JSON.stringify(invocation));
  process.exit(2);
}
await runner.completeRun({ completionId: "completion-task-run-session-e2e", exitCode: 0 });
console.log(JSON.stringify({ status: "completed", exit_code: 0 }));
`

// TestBridgeRunnerAgentExecViaServeSurface exercises the process form through
// the real serve taskrunapi module, authenticated only by the bridge-issued
// task-run lease tuple. It proves open -> artifact -> close completes before
// the terminal TaskRun completion revokes the lease.
func TestBridgeRunnerAgentExecViaServeSurface(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found in PATH; skipping runner SDK e2e")
	}
	runnerJS, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "sdk", "runner.js"))
	if err != nil {
		t.Fatalf("resolve sdk/runner.js: %v", err)
	}
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-session-e2e", TaskID: "TASK-1",
		Status: domain.TaskRunRunning, NodeID: "node-1", LeaseID: "lease-1", FencingToken: 42,
	}); err != nil {
		t.Fatalf("create task run: %v", err)
	}
	mux := http.NewServeMux()
	NewModule(Config{Store: st}).Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	script := filepath.Join(t.TempDir(), "agent-exec-e2e.mjs")
	if err := os.WriteFile(script, []byte(fmt.Sprintf(sessionE2ERunnerScript, "file://"+filepath.ToSlash(runnerJS))), 0o600); err != nil {
		t.Fatalf("write runner script: %v", err)
	}
	result, err := (driverpkg.HostBridgeTaskExecutor{
		Store: st, WorktreePath: t.TempDir(), Command: []string{node, script}, APIBaseURL: server.URL,
	}).ExecuteTask(ctx, driverpkg.TaskExecRequest{
		WorkspaceKey: "WS", TaskRunID: "task-run-session-e2e", TaskID: "TASK-1",
		ProviderProfile: "custom-e2e", NodeID: "node-1", LeaseID: "lease-1",
		LeaseToken: "lease-token-e2e", FencingToken: 42,
	})
	if err != nil || result.Status != domain.TaskRunCompleted {
		t.Fatalf("ExecuteTask = %+v err=%v, want completed", result, err)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "task-run-session-e2e-a1-agent")
	if err != nil {
		t.Fatalf("get agent session: %v", err)
	}
	if session.Status != domain.AgentSessionCompleted || session.Metadata[store.SessionMetadataUsageTokens] != "7" || session.Metadata[store.SessionMetadataUsageCostUSD] != "0.02" {
		t.Fatalf("session = %+v, want completed usage", session)
	}
	artifact, err := st.Artifacts().Get(ctx, "WS", "transcript-task-run-session-e2e-a1-agent")
	if err != nil || artifact.DurableStatus != "finalized" {
		t.Fatalf("transcript artifact = %+v err=%v, want finalized", artifact, err)
	}
}
