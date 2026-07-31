package daytonahost

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

func TestRunDaytonaProviderHostFakeServerSecretContainment(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	server := filepath.Join(dir, "server.mjs")
	const script = `
const sentinels = ['daytona-secret-sentinel', 'github-secret-sentinel'];
const encoded = sentinels.map((secret) => Buffer.from(secret, 'utf8').toString('base64'));
const ambient = JSON.stringify({ argv: process.argv, env: process.env });
if ([...sentinels, ...encoded].some((secret) => ambient.includes(secret))) {
  process.send({ type: 'error', error: { message: 'credential reached argv or env' } });
  process.exit(1);
}
process.send({ version: 1, type: 'ready' });
process.on('message', (message) => {
  const input = message && message.input;
  if (!input || input.credentials.daytona !== encoded[0] || input.credentials.github !== encoded[1]) {
    process.send({ type: 'error', error: { message: 'credential missing from private IPC input' } });
    process.exit(1);
    return;
  }
  process.send({
    version: 1,
    type: 'result',
    requestId: message.requestId,
    result: {
      schemaVersion: 'daytona-task-run-execution.v1',
      status: 'completed',
      exitCode: 0,
      logs: 'provider execution completed',
      usage: {},
      sandbox: {
        provider: 'daytona',
        id: 'opaque-sandbox',
        workDir: '/home/daytona',
        cwd: '/tmp/loom-daytona-task-repo',
        repoRef: 'abc123'
      }
    }
  }, () => process.exit(0));
});
`
	if err := os.WriteFile(server, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	command := execution.DaytonaProviderCommand{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1", DriverRunID: "driver-1",
		Intent: execution.DaytonaProviderIntent{
			SchemaVersion: execution.DaytonaProviderSchemaV1,
			RepositoryURL: "https://github.com/octocat/Hello-World.git",
			TaskPrompt:    "Make a focused change.",
			Backend:       "codex",
			Delivery:      execution.DaytonaProviderDelivery{},
		},
	}
	result, err := Run(context.Background(), Options{
		ServerPath:        server,
		DaytonaSDKImport:  "/non-secret/daytona/esm/index.js",
		Command:           command,
		DaytonaCredential: []byte("daytona-secret-sentinel"),
		GitHubCredential:  []byte("github-secret-sentinel"),
	}, Runtime{
		NodePath:     "node",
		BaseEnv:      []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		InheritedEnv: os.Environ(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "completed" || result.Sandbox.ID != "opaque-sandbox" {
		t.Fatalf("result = %+v", result)
	}
}

func TestDaytonaProviderHostEnvNeverContainsProviderCredentials(t *testing.T) {
	daytona := []byte("daytona-secret-sentinel")
	github := []byte("github-secret-sentinel")
	env := daytonaProviderHostEnv([]string{
		"PATH=/usr/bin",
		"HOME=/tmp/home",
	}, []string{
		"DAYTONA_API_KEY=" + string(daytona),
		"GITHUB_TOKEN=" + string(github),
		"LOOM_TASK_RUN_LEASE_TOKEN=lease-secret",
		"DAYTONA_API_URL=https://app.daytona.io/api",
	}, Options{
		ServerPath:        "/tmp/server.mjs",
		DaytonaSDKImport:  "/tmp/daytona/esm/index.js",
		DaytonaCredential: daytona,
		GitHubCredential:  github,
	})
	joined := strings.Join(env, "\n")
	for _, secret := range []string{string(daytona), string(github), "lease-secret"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("provider host env contains secret %q: %s", secret, joined)
		}
	}
	if !strings.Contains(joined, "DAYTONA_API_URL=https://app.daytona.io/api") {
		t.Fatalf("provider host env dropped non-secret provider endpoint: %s", joined)
	}
}

func TestDecodeDaytonaProviderResultRejectsUnknownFields(t *testing.T) {
	raw := []byte(`{
		"schemaVersion":"daytona-task-run-execution.v1",
		"status":"completed",
		"exitCode":0,
		"usage":{},
		"sandbox":{"provider":"daytona"},
		"credentials":{"daytona":"secret"}
	}`)
	if _, err := decodeDaytonaProviderResult(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("decode unknown field error = %v, want strict rejection", err)
	}
}

func TestDaytonaProviderHostReceiptValidationAndLeakDetection(t *testing.T) {
	result := execution.DaytonaProviderResult{
		SchemaVersion: execution.DaytonaProviderSchemaV1,
		Status:        "completed",
		ExitCode:      0,
		Sandbox: execution.DaytonaSandboxReceipt{
			Provider: "daytona",
			ID:       "opaque",
			WorkDir:  "/home/daytona",
			CWD:      "/tmp/loom-daytona-task-repo",
			RepoRef:  "abc123",
		},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeDaytonaProviderResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDaytonaProviderHostResult(decoded); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}
	incomplete := decoded
	incomplete.Sandbox.CWD = ""
	if err := validateDaytonaProviderHostResult(incomplete); err == nil ||
		!strings.Contains(err.Error(), "materialization evidence") {
		t.Fatalf("incomplete materialization receipt error = %v", err)
	}
	if !containsProviderSecret([]byte(`{"logs":"daytona-secret-sentinel"}`), []byte("daytona-secret-sentinel")) {
		t.Fatal("credential leak detector missed exact credential bytes")
	}
	for _, forbidden := range []string{"payload: input", "LOOM_TASK_RUN_REQUEST_JSON", "DAYTONA_API_KEY"} {
		if strings.Contains(daytonaProviderHostLauncher, forbidden) {
			t.Fatalf("private launcher contains forbidden channel %q", forbidden)
		}
	}
	if !strings.Contains(daytonaProviderHostLauncher, "input,") {
		t.Fatal("private launcher does not invoke Flue through the input channel")
	}
}
