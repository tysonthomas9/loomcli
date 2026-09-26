package agent

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sandbox"
)

func TestSandboxSupervisedLoomInvocation_BuiltInRole(t *testing.T) {
	spec := SandboxExecSpec{Worktree: "coder", Role: "task", IsBuiltinRole: true}
	got := sandboxSupervisedLoomInvocation(spec, sandbox.Config{Backend: "playground"})
	want := "/sandbox/loom 'task' '/sandbox/repo' --auto --backend 'playground'"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	// The container has no daemon/IPC socket/host transcript — the host
	// supervisor manages lifecycle via the openshell exec process.
	if strings.Contains(got, "--daemon-mode") {
		t.Error("in-container loom must NOT use --daemon-mode")
	}
}

func TestSandboxSupervisedLoomInvocation_CustomRole(t *testing.T) {
	spec := SandboxExecSpec{Worktree: "researcher", Role: "deep", PromptFile: "p.md", TaskFilter: "label:research"}
	got := sandboxSupervisedLoomInvocation(spec, sandbox.Config{})
	for _, want := range []string{"agent '/sandbox/repo'", "--prompt 'p.md'", "--task-filter 'label:research'", "--auto"} {
		if !strings.Contains(got, want) {
			t.Errorf("invocation %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "--backend") {
		t.Error("no backend given => no --backend flag")
	}
}

func TestSandboxSupervisedBootstrap_ScopedFleetEnv(t *testing.T) {
	spec := SandboxExecSpec{Worktree: "coder", Role: "task", IsBuiltinRole: true}
	cred := sandboxExecCred{
		URL: "http://host.docker.internal:18099", Key: "sk-dev",
		Actor: "sandbox:WS1:coder:1", Workspace: "WS1",
	}
	script := sandboxSupervisedBootstrap(spec, "feature/x", "http://host.docker.internal:9418/r.git", cred, sandbox.Config{Backend: "playground"})

	for _, want := range []string{
		"git clone --branch 'feature/x' --single-branch 'http://host.docker.internal:9418/r.git' /sandbox/repo\n",
		"export LOOM_FLEET_DB_URL='http://host.docker.internal:18099'\n",
		"export LOOM_FLEET_DB_API_KEY='sk-dev'\n",
		"export LOOM_FLEET_DB_ACTOR='sandbox:WS1:coder:1'\n",
		"export LOOM_WORKSPACE='WS1'\n",
		"git push origin 'feature/x'\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("bootstrap missing %q\n--- script ---\n%s", want, script)
		}
	}
	// Agent reaches fleet-db directly; loom-serve must not be in the picture.
	if strings.Contains(script, "LOOM_SERVER_URL") {
		t.Error("bootstrap must not export LOOM_SERVER_URL (agent uses fleet-db directly)")
	}
}
