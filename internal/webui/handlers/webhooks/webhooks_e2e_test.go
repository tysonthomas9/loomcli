//go:build e2e
// +build e2e

package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/netutil"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestE2E_GitHubWebhookDispatchesDriverRunWithEphemeralStack(t *testing.T) {
	e2e := newGitHubWebhookE2E(t)

	e2e.startFleetDB()
	e2e.seedWorkspace()
	e2e.startLoomServe()
	binding := e2e.registerGitHubDriver()

	first := e2e.postGitHubWebhook("e2e-delivery-1", http.StatusAccepted)
	firstRunID := first.primaryRunID(t)
	run := e2e.expectQueuedDriverRun(firstRunID)
	event := e2e.expectTriggerEvent(binding)
	e2e.expectTriggerDelivery(event, binding, run)
	e2e.expectRunEvents(run.RunID, "driver_run.create")

	second := e2e.postGitHubWebhook("e2e-delivery-1", http.StatusAccepted)
	if second.primaryRunID(t) != firstRunID {
		t.Fatalf("redelivery driver_run_id = %q, want original %q", second.primaryRunID(t), firstRunID)
	}
	e2e.expectIdempotentRedelivery(firstRunID)
}

func TestE2E_GitHubWebhookRunsDriverAgainstLiveGitHubPR(t *testing.T) {
	repo := strings.TrimSpace(os.Getenv("LOOM_E2E_GITHUB_REPO"))
	if repo == "" {
		t.Skip("set LOOM_E2E_GITHUB_REPO=owner/repo to run the live GitHub webhook driver E2E")
	}
	requireCommand(t, "gh")
	requireCommand(t, "git")
	requireCommand(t, "codex")
	requireCommand(t, "node")

	live := createLiveGitHubPR(t, repo)

	e2e := newGitHubWebhookE2E(t)
	e2e.enableDriverExecutor = true
	e2e.useHostHomeForDriver = true

	e2e.startFleetDB()
	e2e.seedWorkspace()
	e2e.writeLiveGitHubReviewDist()
	binding := e2e.registerLiveGitHubDriver(live)
	e2e.startLoomServe()

	deliveryID := "live-" + live.ID
	first := e2e.postGitHubWebhookPayload(deliveryID, live.WebhookPayload(), http.StatusAccepted)
	firstRunID := first.primaryRunID(t)
	run := e2e.waitForRunCompleted(firstRunID)
	if !strings.Contains(run.Summary, live.Repo+"#"+strconv.Itoa(live.Number)) {
		t.Fatalf("completed run summary = %q, want repo and PR number", run.Summary)
	}
	if !strings.Contains(run.Output["flue_stderr_tail"], "github-pr-review analyzed "+live.Repo+"#"+strconv.Itoa(live.Number)) {
		t.Fatalf("run output missing live GitHub verification marker: %+v", run.Output)
	}
	event := e2e.expectLiveTriggerEvent(binding, live, deliveryID)
	e2e.expectTriggerDelivery(event, binding, run)
	e2e.expectRunEvents(run.RunID, "driver_run.create", "driver_run.claim", "driver_run.finish")

	second := e2e.postGitHubWebhookPayload(deliveryID, live.WebhookPayload(), http.StatusAccepted)
	if second.primaryRunID(t) != firstRunID {
		t.Fatalf("redelivery driver_run_id = %q, want original %q", second.primaryRunID(t), firstRunID)
	}
	e2e.expectIdempotentLiveRedelivery(firstRunID)
}

type githubWebhookE2E struct {
	t *testing.T

	repoRoot    string
	fleetDBRepo string

	workspace string
	actor     string

	loomBin    string
	fleetDBBin string

	workDir   string
	configDir string

	fleetURL    string
	fleetAPIKey string
	loomURL     string

	enableDriverExecutor bool
	useHostHomeForDriver bool

	fleetClient *fleetdb.Client
	httpClient  *http.Client
}

type liveGitHubPR struct {
	ID          string
	Repo        string
	DefaultBase string
	Branch      string
	Number      int
	URL         string
	Title       string
	HeadRef     string
	BaseRef     string
	HeadSHA     string
	BaseSHA     string
}

// githubWebhookResponse pins the BREAKING router-v2 202 wire: deliveries[]
// only, no top-level driver_run_id / driver_run.
type githubWebhookResponse struct {
	Status         string                       `json:"status"`
	RouteKey       string                       `json:"route_key"`
	IdempotencyKey string                       `json:"idempotency_key"`
	Deliveries     []store.TriggerRouteDelivery `json:"deliveries"`
}

// primaryRunID returns the first delivery leg's run id — the exact-RouteKey
// owner's run, which is what these single-binding E2E flows track.
func (r githubWebhookResponse) primaryRunID(t *testing.T) string {
	t.Helper()
	if len(r.Deliveries) == 0 {
		t.Fatalf("webhook response has no deliveries: %+v", r)
	}
	return r.Deliveries[0].RunID
}

const githubPRAnalysisSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["summary", "risk", "findings"],
  "properties": {
    "summary": {
      "type": "string",
      "minLength": 1
    },
    "risk": {
      "type": "string",
      "enum": ["low", "medium", "high"]
    },
    "findings": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["severity", "description"],
        "properties": {
          "severity": {
            "type": "string",
            "enum": ["low", "medium", "high"]
          },
          "description": {
            "type": "string"
          }
        }
      }
    }
  }
}
`

func newGitHubWebhookE2E(t *testing.T) *githubWebhookE2E {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real GitHub webhook E2E under -short")
	}

	repoRoot := githubWebhookRepoRoot(t)
	workDir := filepath.Join(t.TempDir(), "webhook-repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("create webhook repo: %v", err)
	}

	e2e := &githubWebhookE2E{
		t:           t,
		repoRoot:    repoRoot,
		fleetDBRepo: githubWebhookSiblingRepo(t, repoRoot, "fleet-db", "FLEET_DB_REPO", "cmd/fleet-db"),
		workspace:   "GITHUBE2E",
		actor:       "github-webhook-e2e",
		workDir:     workDir,
		configDir:   filepath.Join(t.TempDir(), "loom-config"),
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		loomBin:     githubWebhookBuildGoBinary(t, repoRoot, "./cmd/loom", "loom"),
	}
	e2e.fleetDBBin = githubWebhookBuildGoBinary(t, e2e.fleetDBRepo, "./cmd/fleet-db", "fleet-db")
	return e2e
}

func (e *githubWebhookE2E) startFleetDB() {
	e.t.Helper()
	e.t.Setenv(bootstrap.EnvFleetDBBin, e.fleetDBBin)
	e.t.Setenv("FLEET_RATE_LIMIT_ENABLED", "false")
	e.t.Setenv("FLEETDB_ISSUE_DESIGN_STORAGE", "inline")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	e.t.Cleanup(cancel)

	dataDir := filepath.Join(e.t.TempDir(), "fleet-data")
	embedded, err := bootstrap.StartEmbedded(ctx, dataDir, githubWebhookQuietLogger())
	if err != nil {
		e.t.Fatalf("start embedded fleet-db: %v", err)
	}
	e.t.Cleanup(func() {
		if err := embedded.Stop(); err != nil {
			e.t.Logf("stop embedded fleet-db: %v", err)
		}
	})

	e.fleetURL = embedded.URL()
	e.fleetAPIKey, err = authority.ReadLocalFleetDBServiceCredential(filepath.Join(dataDir, "fleet-db", "auth"))
	if err != nil {
		e.t.Fatalf("read embedded FleetDB service credential: %v", err)
	}
	e.fleetClient, err = embedded.NewClient(fleetdb.Config{Actor: e.actor})
	if err != nil {
		e.t.Fatalf("create fleet-db client: %v", err)
	}
	e.t.Cleanup(func() {
		if err := e.fleetClient.Close(); err != nil {
			e.t.Logf("close fleet-db client: %v", err)
		}
	})
}

func (e *githubWebhookE2E) seedWorkspace() {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := e.fleetClient.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:  e.workspace,
		Name: "GitHub webhook E2E",
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		e.t.Fatalf("create workspace: %v", err)
	}
}

func (e *githubWebhookE2E) startLoomServe() {
	e.t.Helper()
	_, port, err := netutil.PickFreeLoopbackPort()
	if err != nil {
		e.t.Fatalf("pick loom port: %v", err)
	}
	e.loomURL = "http://127.0.0.1:" + strconv.Itoa(port)
	home := e.phase3HomeDir()
	if e.useHostHomeForDriver {
		if hostHome := strings.TrimSpace(os.Getenv("HOME")); hostHome != "" {
			home = hostHome
		}
	}
	driverExecutor := "0"
	if e.enableDriverExecutor {
		driverExecutor = "1"
	}

	cmd := exec.Command(e.loomBin, "serve",
		"--no-daemon",
		"--bind", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--frontend-url", "http://127.0.0.1:9",
	)
	cmd.Dir = e.workDir
	cmd.Env = githubWebhookEnv(map[string]string{
		"HOME":                         home,
		"LOOM_CONFIG_DIR":              e.configDir,
		"LOOM_WORKSPACE":               e.workspace,
		"LOOM_WORKSPACE_RUNTIME_DIR":   e.phase3RuntimeDir(),
		"LOOM_FLEET_DB_URL":            e.fleetURL,
		"LOOM_FLEET_URL":               "",
		"LOOM_SERVER_URL":              "",
		"LOOM_DISABLE_H2C":             "1",
		"LOOM_DRIVER_EXECUTOR":         driverExecutor,
		"LOOM_DRIVER_EXECUTOR_NODE_ID": "github-webhook-e2e-node",
		"LOOM_ISSUE_BACKEND":           "",
		bootstrap.EnvFleetDBBin:        e.fleetDBBin,
		bootstrap.EnvFleetDBAPIKey:     e.fleetAPIKey,
		bootstrap.EnvFleetDBActor:      e.actor,
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		e.t.Fatalf("start loom serve: %v", err)
	}
	e.t.Cleanup(func() {
		githubWebhookStopProcess(e.t, cmd)
		if e.t.Failed() {
			e.t.Logf("loom serve stdout:\n%s", strings.TrimSpace(stdout.String()))
			e.t.Logf("loom serve stderr:\n%s", strings.TrimSpace(stderr.String()))
		}
	})

	e.waitForLoomHealth(&stderr)
}

func (e *githubWebhookE2E) registerGitHubDriver() *domain.TriggerBinding {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const (
		driverID = "github-pr-review"
		version  = "github-pr-review-v1"
		route    = "github.pull_request.opened"
		secret   = "e2e-webhook-secret"
	)
	driver, err := e.fleetClient.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: e.workspace,
		DriverID:     driverID,
		Name:         driverID,
		OwnerType:    domain.DriverOwnerUser,
		OwnerRef:     e.actor,
		Status:       domain.DriverStatusDraft,
		TrustLevel:   domain.DriverTrustUntrusted,
	})
	if err != nil {
		e.t.Fatalf("create driver: %v", err)
	}
	if driver.Revision != 1 {
		e.t.Fatalf("created driver revision = %d, want 1", driver.Revision)
	}
	_, err = e.fleetClient.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     e.workspace,
		VersionID:        version,
		DriverID:         driverID,
		Version:          1,
		SourceRef:        "e2e://github-pr-review",
		SourceDigest:     "sha256:e2e-src",
		BundleRef:        ".loom/drivers/github-pr-review/github-pr-review-v1",
		BundleDigest:     "sha256:e2e-bundle",
		Runtime:          "flue-node",
		Manifest:         map[string]string{workflowcatalog.ManifestTrustLevelKey: string(domain.DriverTrustUntrusted)},
		ValidationStatus: domain.DriverVersionValidationPassed,
		CreatedBy:        e.actor,
	})
	if err != nil {
		e.t.Fatalf("create driver version: %v", err)
	}
	approved, err := e.fleetClient.WorkflowCatalog().ApproveVersion(ctx, e.workspace, driverID, version, 1)
	if err != nil || approved == nil || approved.CommittedRevision != 2 {
		e.t.Fatalf("approve driver version = %+v, %v", approved, err)
	}
	activated, err := e.fleetClient.WorkflowCatalog().ActivateVersion(ctx, e.workspace, driverID, version, 2)
	if err != nil || activated == nil || activated.CommittedRevision != 3 {
		e.t.Fatalf("activate driver version = %+v, %v", activated, err)
	}

	binding, err := e.fleetClient.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:     e.workspace,
		BindingID:        "binding-github-pr-opened",
		Name:             "pr-review",
		SourceKind:       "github",
		RouteKey:         route,
		DriverID:         driverID,
		DriverVersionID:  version,
		TargetEntrypoint: "run",
		WebhookSecret:    secret,
		Enabled:          true,
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		e.t.Fatalf("create trigger binding: %v", err)
	}
	if binding == nil {
		binding, err = e.fleetClient.TriggerBindings().GetByRouteKey(ctx, e.workspace, route)
		if err != nil {
			e.t.Fatalf("load existing trigger binding: %v", err)
		}
	}
	if binding.WebhookSecret != "" {
		e.t.Fatalf("trigger binding leaked webhook_secret on read: %+v", binding)
	}
	resolved, err := e.fleetClient.TriggerBindings().ResolveWebhookSecret(ctx, e.workspace, binding.BindingID)
	if err != nil {
		e.t.Fatalf("resolve webhook secret: %v", err)
	}
	if resolved != secret {
		e.t.Fatalf("resolved webhook secret = %q, want %q", resolved, secret)
	}
	return binding
}

func (e *githubWebhookE2E) writeLiveGitHubReviewDist() {
	e.t.Helper()
	dist := filepath.Join(e.workDir, "dist")
	if err := os.RemoveAll(dist); err != nil {
		e.t.Fatalf("remove live driver dist: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		e.t.Fatalf("create live driver dist: %v", err)
	}
	server := `import { execFile, spawn } from 'node:child_process';

function execFileText(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    execFile(command, args, { timeout: 30000, maxBuffer: 1024 * 1024 * 4, ...options }, (error, stdout, stderr) => {
      if (error) {
        error.stdout = stdout;
        error.stderr = stderr;
        reject(error);
        return;
      }
      resolve(stdout);
    });
  });
}

function runCodexAnalysis(prompt) {
  return new Promise((resolve, reject) => {
    const child = spawn('codex', [
      'exec',
      '--skip-git-repo-check',
      '--sandbox', 'read-only',
      '--ephemeral',
      '--color', 'never',
      '--output-schema', 'dist/assets/github-pr-analysis.schema.json',
      '-'
    ], {
      cwd: process.cwd(),
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    const timer = setTimeout(() => {
      child.kill('SIGTERM');
      setTimeout(() => child.kill('SIGKILL'), 1000).unref?.();
    }, 120000);
    child.stdout.on('data', (chunk) => { stdout += chunk.toString(); });
    child.stderr.on('data', (chunk) => { stderr += chunk.toString(); });
    child.on('error', (error) => {
      clearTimeout(timer);
      reject(error);
    });
    child.on('exit', (code, signal) => {
      clearTimeout(timer);
      if (code !== 0) {
        reject(new Error('codex exec failed code=' + code + (signal ? ' signal=' + signal : '') + '\n' + stderr + '\n' + stdout));
        return;
      }
      const lines = stdout.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
      for (let i = lines.length - 1; i >= 0; i -= 1) {
        if (!lines[i].startsWith('{')) continue;
        try {
          const parsed = JSON.parse(lines[i]);
          if (parsed && typeof parsed === 'object') {
            resolve(parsed);
            return;
          }
        } catch {}
      }
      reject(new Error('decode codex analysis: no JSON object line found\nstdout:\n' + stdout + '\nstderr:\n' + stderr));
    });
    child.stdin.end(prompt);
  });
}

function analysisPrompt(repo, number, pr, diff) {
  return ` + "`" + `You are a code review agent. Analyze this GitHub pull request for correctness risks.

Return concise JSON matching the requested schema. Do not ask follow-up questions. Do not modify files.

Repository: ${repo}
Pull request: #${number}
Title: ${pr.title || ''}
State: ${pr.state || ''}
Branch: ${pr.headRefName || ''} -> ${pr.baseRefName || ''}

Patch:
${diff.slice(0, 60000)}
` + "`" + `;
}

function sendResult(requestId, result) {
  process.send({ version: 1, type: 'result', requestId, result }, () => process.exit(0));
}

if (process.send) {
  process.send({ version: 1, type: 'ready', target: 'workflow', name: process.env.FLUE_CLI_NAME || 'github-pr-review' });
  process.on('message', async (message) => {
    const payload = message?.payload || {};
    const repo = payload.repository?.full_name || '';
    const number = payload.pull_request?.number || payload.number || 0;
    const requestId = message?.requestId || 'live-github-e2e';

    if (!repo || !number) {
      sendResult(requestId, { status: 'failed', summary: 'missing repository or pull request number', errorClass: 'invalid_payload' });
      return;
    }

    try {
      const pr = JSON.parse(await execFileText('gh', ['pr', 'view', String(number), '--repo', repo, '--json', 'number,title,headRefName,baseRefName,state']));
      if (pr.number !== Number(number)) {
        sendResult(requestId, { status: 'failed', summary: 'gh returned unexpected PR number ' + pr.number, errorClass: 'github_pr_mismatch' });
        return;
      }
      const diff = await execFileText('gh', ['pr', 'diff', String(number), '--repo', repo, '--patch']);
      const analysis = await runCodexAnalysis(analysisPrompt(repo, number, pr, diff));
      if (!analysis.summary || !analysis.risk) {
        sendResult(requestId, { status: 'failed', summary: 'codex analysis missing summary or risk', errorClass: 'codex_analysis_invalid' });
        return;
      }
      const findings = Array.isArray(analysis.findings) ? analysis.findings.length : 0;
      console.log('github-pr-review analyzed ' + repo + '#' + number + ' risk=' + analysis.risk + ' findings=' + findings + ': ' + analysis.summary);
      sendResult(requestId, {
        status: 'completed',
        summary: 'github-pr-review analyzed ' + repo + '#' + number + ' risk=' + analysis.risk + ' findings=' + findings,
      });
    } catch (error) {
      sendResult(requestId, { status: 'failed', summary: error?.stderr || error?.message || String(error), errorClass: 'github_pr_analysis_failed' });
    }
  });
}
`
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(server), 0o644); err != nil {
		e.t.Fatalf("write live driver server: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "github-pr-review.mjs"), []byte("export const marker = 'live-github-e2e';\n"), 0o644); err != nil {
		e.t.Fatalf("write live driver asset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "github-pr-analysis.schema.json"), []byte(githubPRAnalysisSchema), 0o644); err != nil {
		e.t.Fatalf("write live driver analysis schema: %v", err)
	}
}

func (e *githubWebhookE2E) registerLiveGitHubDriver(live liveGitHubPR) *domain.TriggerBinding {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	registered, err := driverpkg.RegisterFlueDriver(ctx, e.fleetClient, driverpkg.RegisterFlueOptions{
		WorkspaceKey: e.workspace,
		WorkDir:      e.workDir,
		DistPath:     "dist",
		DriverName:   "github-pr-review",
		DriverID:     "github-pr-review",
		WorkflowName: "github-pr-review",
		SourceRef:    "github-live-e2e://" + live.Repo + "#" + strconv.Itoa(live.Number),
		SourceDigest: "sha256:live-github-e2e-" + live.ID,
		CreatedBy:    e.actor,
		Activate:     true,
	})
	if err != nil {
		e.t.Fatalf("register live GitHub driver: %v", err)
	}

	binding, err := e.fleetClient.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:     e.workspace,
		BindingID:        "binding-live-github-pr-opened",
		Name:             "live-pr-review",
		SourceKind:       "github",
		RouteKey:         "github.pull_request.opened",
		DriverID:         registered.Driver.DriverID,
		DriverVersionID:  registered.Version.VersionID,
		TargetEntrypoint: "run",
		WebhookSecret:    "e2e-webhook-secret",
		Enabled:          true,
	})
	if err != nil {
		e.t.Fatalf("create live trigger binding: %v", err)
	}
	if binding.WebhookSecret != "" {
		e.t.Fatalf("live trigger binding leaked webhook_secret on read: %+v", binding)
	}
	return binding
}

func (e *githubWebhookE2E) postGitHubWebhook(deliveryID string, wantStatus int) githubWebhookResponse {
	e.t.Helper()
	body := []byte(`{"action":"opened","number":4242,"pull_request":{"number":4242},"repository":{"full_name":"acme/widgets"},"sender":{"login":"octocat"}}`)
	return e.postGitHubWebhookPayload(deliveryID, body, wantStatus)
}

func (e *githubWebhookE2E) postGitHubWebhookPayload(deliveryID string, body []byte, wantStatus int) githubWebhookResponse {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.loomURL+"/api/workspaces/"+e.workspace+"/webhooks/github", bytes.NewReader(body))
	if err != nil {
		e.t.Fatalf("create webhook request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(githubEventHeader, "pull_request")
	req.Header.Set(githubDeliveryHeader, deliveryID)
	req.Header.Set(githubSignatureHeader, githubSignature("e2e-webhook-secret", body))

	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("POST GitHub webhook: %v", err)
	}
	defer resp.Body.Close()

	var out githubWebhookResponse
	githubWebhookDecodeResponse(e.t, resp, wantStatus, &out)
	if wantStatus == http.StatusAccepted {
		if out.Status != "accepted" || out.RouteKey != "github.pull_request.opened" || len(out.Deliveries) == 0 {
			e.t.Fatalf("unexpected webhook response: %+v", out)
		}
		for i, leg := range out.Deliveries {
			if leg.RunID == "" || leg.DeliveryID == "" || leg.BindingID == "" {
				e.t.Fatalf("webhook response leg %d incomplete: %+v", i, out.Deliveries)
			}
		}
		if out.IdempotencyKey != "github:"+deliveryID {
			e.t.Fatalf("idempotency_key = %q, want github:%s", out.IdempotencyKey, deliveryID)
		}
	}
	return out
}

func (e *githubWebhookE2E) expectQueuedDriverRun(runID string) *domain.DriverRun {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	run, err := e.fleetClient.DriverRuns().Get(ctx, e.workspace, runID)
	if err != nil {
		e.t.Fatalf("get driver run %s: %v", runID, err)
	}
	if run.Status != domain.DriverRunQueued {
		e.t.Fatalf("driver run status = %s, want queued: %+v", run.Status, run)
	}
	if run.DriverID != "github-pr-review" || run.DriverVersionID != "github-pr-review-v1" || run.Entrypoint != "run" ||
		run.TriggerBindingID != "binding-github-pr-opened" {
		e.t.Fatalf("driver run pinned target = %+v", run)
	}
	if run.SourceKind != "github" || !strings.HasPrefix(run.SourceRef, "automation-event-") ||
		!strings.HasPrefix(run.IdempotencyKey, "automation-run-idempotency-") || run.IdempotencyKey == "github:e2e-delivery-1" {
		e.t.Fatalf("driver run source/idempotency = %+v", run)
	}
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(run.Payload, &payload); err != nil {
		e.t.Fatalf("decode driver run payload %s: %v", run.Payload, err)
	}
	if payload.Action != "opened" || payload.Repository.FullName != "acme/widgets" || payload.PullRequest.Number != 4242 {
		e.t.Fatalf("driver run payload = %+v", payload)
	}
	return run
}

func (e *githubWebhookE2E) expectTriggerEvent(binding *domain.TriggerBinding) *domain.TriggerEvent {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	events, err := e.fleetClient.TriggerEvents().List(ctx, e.workspace, store.TriggerEventFilter{
		SourceKind:       "github",
		TriggerBindingID: binding.BindingID,
		Limit:            10,
	})
	if err != nil {
		e.t.Fatalf("list trigger events: %v", err)
	}
	if len(events) != 1 {
		e.t.Fatalf("trigger event count = %d, want 1: %+v", len(events), events)
	}
	event := events[0]
	if event.SourceEventID != "e2e-delivery-1" || event.EventType != "pull_request" {
		e.t.Fatalf("trigger event identity = %+v", event)
	}
	if event.SourceKind != "github" || event.SignatureStatus != "verified" || event.RawPayloadDigest == "" {
		e.t.Fatalf("trigger event verification = %+v", event)
	}
	if event.SubjectRef != "acme/widgets#4242" || event.ActorRef != "octocat" {
		e.t.Fatalf("trigger event subject/actor = %+v", event)
	}
	return event
}

func (e *githubWebhookE2E) expectTriggerDelivery(event *domain.TriggerEvent, binding *domain.TriggerBinding, run *domain.DriverRun) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	deliveries, err := e.fleetClient.TriggerDeliveries().List(ctx, e.workspace, store.TriggerDeliveryFilter{
		TriggerEventID:   event.EventID,
		TriggerBindingID: binding.BindingID,
		Status:           domain.TriggerDeliveryDispatched,
		Limit:            10,
	})
	if err != nil {
		e.t.Fatalf("list trigger deliveries: %v", err)
	}
	if len(deliveries) != 1 {
		e.t.Fatalf("trigger delivery count = %d, want 1: %+v", len(deliveries), deliveries)
	}
	delivery := deliveries[0]
	if delivery.DriverRunID != run.RunID || delivery.TriggerEventID != event.EventID || delivery.TriggerBindingID != binding.BindingID {
		e.t.Fatalf("trigger delivery linkage = %+v, event=%s binding=%s run=%s", delivery, event.EventID, binding.BindingID, run.RunID)
	}
}

func (e *githubWebhookE2E) expectRunEvents(runID string, requiredActions ...string) {
	e.t.Helper()
	var page domain.PlatformEventsPage
	e.getLoomJSON("/api/workspaces/"+e.workspace+"/runs/"+runID+"/events?limit=50", http.StatusOK, &page)

	actions := map[string]bool{}
	for _, event := range page.Events {
		actions[event.Action] = true
	}
	for _, action := range requiredActions {
		if !actions[action] {
			e.t.Fatalf("run events missing %s; got %v", action, githubWebhookSortedKeys(actions))
		}
	}
}

func (e *githubWebhookE2E) waitForRunCompleted(runID string) *domain.DriverRun {
	e.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var run domain.DriverRun
		e.getLoomJSON("/api/workspaces/"+e.workspace+"/runs/"+runID, http.StatusOK, &run)
		switch run.Status {
		case domain.DriverRunCompleted:
			return &run
		case domain.DriverRunFailed, domain.DriverRunNeedsReview, domain.DriverRunCancelled:
			e.t.Fatalf("driver run %s reached terminal status %s: %+v", runID, run.Status, run)
		}
		time.Sleep(500 * time.Millisecond)
	}
	var run domain.DriverRun
	e.getLoomJSON("/api/workspaces/"+e.workspace+"/runs/"+runID, http.StatusOK, &run)
	e.t.Fatalf("driver run %s did not complete before deadline: %+v", runID, run)
	return nil
}

func (e *githubWebhookE2E) expectLiveTriggerEvent(binding *domain.TriggerBinding, live liveGitHubPR, deliveryID string) *domain.TriggerEvent {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	events, err := e.fleetClient.TriggerEvents().List(ctx, e.workspace, store.TriggerEventFilter{
		SourceKind:       "github",
		TriggerBindingID: binding.BindingID,
		Limit:            10,
	})
	if err != nil {
		e.t.Fatalf("list live trigger events: %v", err)
	}
	if len(events) != 1 {
		e.t.Fatalf("live trigger event count = %d, want 1: %+v", len(events), events)
	}
	event := events[0]
	if event.SourceEventID != deliveryID || event.EventType != "pull_request" {
		e.t.Fatalf("live trigger event identity = %+v", event)
	}
	if event.SourceKind != "github" || event.SignatureStatus != "verified" || event.RawPayloadDigest == "" {
		e.t.Fatalf("live trigger event verification = %+v", event)
	}
	wantSubject := live.Repo + "#" + strconv.Itoa(live.Number)
	if event.SubjectRef != wantSubject {
		e.t.Fatalf("live trigger event subject = %q, want %q", event.SubjectRef, wantSubject)
	}
	return event
}

func (e *githubWebhookE2E) expectIdempotentRedelivery(runID string) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	events, err := e.fleetClient.TriggerEvents().List(ctx, e.workspace, store.TriggerEventFilter{SourceKind: "github", Limit: 10})
	if err != nil {
		e.t.Fatalf("list trigger events after redelivery: %v", err)
	}
	deliveries, err := e.fleetClient.TriggerDeliveries().List(ctx, e.workspace, store.TriggerDeliveryFilter{Status: domain.TriggerDeliveryDispatched, Limit: 10})
	if err != nil {
		e.t.Fatalf("list trigger deliveries after redelivery: %v", err)
	}
	runs, err := e.fleetClient.DriverRuns().List(ctx, e.workspace, store.DriverRunFilter{DriverID: "github-pr-review", Limit: 10})
	if err != nil {
		e.t.Fatalf("list driver runs after redelivery: %v", err)
	}
	if len(events) != 1 || len(deliveries) != 1 || len(runs) != 1 {
		e.t.Fatalf("redelivery created duplicate state: events=%d deliveries=%d runs=%d", len(events), len(deliveries), len(runs))
	}
	if runs[0].RunID != runID {
		e.t.Fatalf("redelivery run = %s, want %s", runs[0].RunID, runID)
	}
}

func (e *githubWebhookE2E) expectIdempotentLiveRedelivery(runID string) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	events, err := e.fleetClient.TriggerEvents().List(ctx, e.workspace, store.TriggerEventFilter{SourceKind: "github", Limit: 10})
	if err != nil {
		e.t.Fatalf("list live trigger events after redelivery: %v", err)
	}
	deliveries, err := e.fleetClient.TriggerDeliveries().List(ctx, e.workspace, store.TriggerDeliveryFilter{Status: domain.TriggerDeliveryDispatched, Limit: 10})
	if err != nil {
		e.t.Fatalf("list live trigger deliveries after redelivery: %v", err)
	}
	runs, err := e.fleetClient.DriverRuns().List(ctx, e.workspace, store.DriverRunFilter{DriverID: "github-pr-review", Limit: 10})
	if err != nil {
		e.t.Fatalf("list live driver runs after redelivery: %v", err)
	}
	if len(events) != 1 || len(deliveries) != 1 || len(runs) != 1 {
		e.t.Fatalf("live redelivery created duplicate state: events=%d deliveries=%d runs=%d", len(events), len(deliveries), len(runs))
	}
	if runs[0].RunID != runID || runs[0].Status != domain.DriverRunCompleted {
		e.t.Fatalf("live redelivery run = %+v, want completed %s", runs[0], runID)
	}
}

func (e *githubWebhookE2E) getLoomJSON(path string, wantStatus int, out any) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.loomURL+path, nil)
	if err != nil {
		e.t.Fatalf("create GET %s: %v", path, err)
	}
	req.Header.Set("X-Actor", e.actor)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	githubWebhookDecodeResponse(e.t, resp, wantStatus, out)
}

func githubWebhookDecodeResponse(t *testing.T, resp *http.Response, wantStatus int, out any) {
	t.Helper()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read %s response: %v", resp.Request.URL.Path, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d body=%s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, wantStatus, string(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode %s response: %v\n%s", resp.Request.URL.Path, err, string(data))
		}
	}
}

func (e *githubWebhookE2E) waitForLoomHealth(stderr *bytes.Buffer) {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := e.httpClient.Get(e.loomURL + "/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatalf("loom serve did not become healthy at %s\nstderr:\n%s", e.loomURL, strings.TrimSpace(stderr.String()))
}

func createLiveGitHubPR(t *testing.T, repo string) liveGitHubPR {
	t.Helper()
	repoInfo := liveGitHubRepoView(t, repo)
	if repoInfo.IsArchived {
		t.Fatalf("live GitHub repo %s is archived", repoInfo.NameWithOwner)
	}
	id := "loom-e2e-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	branch := "loom-e2e/" + id
	cloneDir := filepath.Join(t.TempDir(), "hello-world")

	githubWebhookRunOutput(t, "", "gh", "repo", "clone", repoInfo.NameWithOwner, cloneDir, "--", "--depth", "1")
	githubWebhookRunOutput(t, cloneDir, "git", "config", "user.email", "loom-e2e@example.test")
	githubWebhookRunOutput(t, cloneDir, "git", "config", "user.name", "Loom GitHub E2E")
	githubWebhookRunOutput(t, cloneDir, "git", "checkout", "-b", branch)

	markerDir := filepath.Join(cloneDir, ".loom-e2e")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("create marker dir: %v", err)
	}
	markerPath := filepath.Join(markerDir, id+".txt")
	if err := os.WriteFile(markerPath, []byte("temporary Loom GitHub webhook E2E marker\n"+id+"\n"), 0o644); err != nil {
		t.Fatalf("write marker file: %v", err)
	}
	githubWebhookRunOutput(t, cloneDir, "git", "add", markerPath)
	githubWebhookRunOutput(t, cloneDir, "git", "commit", "-m", "Loom GitHub webhook E2E "+id)
	githubWebhookRunOutput(t, cloneDir, "git", "push", "-u", "origin", branch)

	live := liveGitHubPR{ID: id, Repo: repoInfo.NameWithOwner, DefaultBase: repoInfo.DefaultBranchRef.Name, Branch: branch}
	branchPushed := true
	t.Cleanup(func() {
		if live.Number > 0 {
			if _, err := githubWebhookRunOutputAllowError("", "gh", "pr", "close", strconv.Itoa(live.Number), "--repo", live.Repo, "--delete-branch", "--comment", "Closing temporary Loom GitHub webhook E2E PR."); err != nil {
				t.Logf("cleanup close PR %d failed: %v", live.Number, err)
			}
			branchPushed = false
		}
		if branchPushed {
			if _, err := githubWebhookRunOutputAllowError(cloneDir, "git", "push", "origin", "--delete", branch); err != nil {
				t.Logf("cleanup delete branch %s failed: %v", branch, err)
			}
		}
	})

	title := "Loom GitHub webhook E2E " + id
	body := "Temporary PR created by Loom's live GitHub webhook E2E. It should be closed automatically."
	prCreateOut := githubWebhookRunOutput(t, cloneDir, "gh", "pr", "create", "--repo", live.Repo, "--head", branch, "--base", live.DefaultBase, "--title", title, "--body", body)
	prRef := strings.TrimSpace(prCreateOut)
	if prRef == "" {
		prRef = branch
	}
	view := liveGitHubPRView(t, live.Repo, prRef)
	live.Number = view.Number
	live.URL = view.URL
	live.Title = view.Title
	live.HeadRef = view.HeadRefName
	live.BaseRef = view.BaseRefName
	live.HeadSHA = view.HeadRefOID
	live.BaseSHA = view.BaseRefOID
	if live.Number <= 0 || live.HeadRef != branch || live.BaseRef != live.DefaultBase {
		t.Fatalf("created live PR = %+v, want branch=%s base=%s", live, branch, live.DefaultBase)
	}
	return live
}

func (p liveGitHubPR) WebhookPayload() []byte {
	payload := map[string]any{
		"action": "opened",
		"number": p.Number,
		"pull_request": map[string]any{
			"number":   p.Number,
			"html_url": p.URL,
			"title":    p.Title,
			"head": map[string]any{
				"ref": p.HeadRef,
				"sha": p.HeadSHA,
			},
			"base": map[string]any{
				"ref": p.BaseRef,
				"sha": p.BaseSHA,
			},
		},
		"repository": map[string]any{
			"full_name":      p.Repo,
			"private":        true,
			"default_branch": p.DefaultBase,
		},
		"sender": map[string]any{
			"login": "loom-e2e",
		},
	}
	data, _ := json.Marshal(payload)
	return data
}

type liveGitHubRepoInfo struct {
	NameWithOwner    string `json:"nameWithOwner"`
	IsArchived       bool   `json:"isArchived"`
	DefaultBranchRef struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
}

type liveGitHubPRInfo struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
	HeadRefOID  string `json:"headRefOid"`
	BaseRefOID  string `json:"baseRefOid"`
}

func liveGitHubRepoView(t *testing.T, repo string) liveGitHubRepoInfo {
	t.Helper()
	out := githubWebhookRunOutput(t, "", "gh", "repo", "view", repo, "--json", "nameWithOwner,isArchived,defaultBranchRef")
	var info liveGitHubRepoInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("decode gh repo view: %v\n%s", err, out)
	}
	if info.NameWithOwner == "" || info.DefaultBranchRef.Name == "" {
		t.Fatalf("gh repo view returned incomplete repo info: %+v", info)
	}
	return info
}

func liveGitHubPRView(t *testing.T, repo, prRef string) liveGitHubPRInfo {
	t.Helper()
	out := githubWebhookRunOutput(t, "", "gh", "pr", "view", prRef, "--repo", repo, "--json", "number,url,title,headRefName,baseRefName,headRefOid,baseRefOid")
	var info liveGitHubPRInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("decode gh pr view: %v\n%s", err, out)
	}
	return info
}

func requireCommand(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not available: %v", name, err)
	}
}

func githubWebhookRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("resolve loom repo root from %s: go.mod not found", file)
		}
		dir = parent
	}
}

func githubWebhookSiblingRepo(t *testing.T, repoRoot, sibling, envName, marker string) string {
	t.Helper()
	if override := strings.TrimSpace(os.Getenv(envName)); override != "" {
		if _, err := os.Stat(filepath.Join(override, marker)); err != nil {
			t.Skipf("%s=%s does not contain %s: %v", envName, override, marker, err)
		}
		return override
	}
	path := filepath.Clean(filepath.Join(repoRoot, "..", sibling))
	if _, err := os.Stat(filepath.Join(path, marker)); err != nil {
		t.Skipf("%s repo not found at %s; set %s", sibling, path, envName)
	}
	return path
}

func githubWebhookBuildGoBinary(t *testing.T, repo, pkg, name string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	githubWebhookRun(t, repo, "go", "build", "-o", out, pkg)
	return out
}

func githubWebhookRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = githubWebhookEnv(map[string]string{
		"GOCACHE": githubWebhookGoCache(),
	})
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed in %s: %v\n%s", name, strings.Join(args, " "), dir, err, strings.TrimSpace(string(output)))
	}
}

func githubWebhookRunOutput(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	output, err := githubWebhookRunOutputAllowError(dir, name, args...)
	if err != nil {
		t.Fatalf("%s %s failed in %s: %v\n%s", name, strings.Join(args, " "), firstNonEmptyString(dir, "."), err, strings.TrimSpace(output))
	}
	return output
}

func githubWebhookRunOutputAllowError(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = githubWebhookEnv(map[string]string{
		"GOCACHE": githubWebhookGoCache(),
	})
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func githubWebhookGoCache() string {
	if cache := strings.TrimSpace(os.Getenv("GOCACHE")); cache != "" {
		return cache
	}
	return filepath.Join(os.TempDir(), "go-build-cache")
}

func githubWebhookQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func githubWebhookStopProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil || cmd.ProcessState != nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func githubWebhookEnv(overrides map[string]string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+len(overrides))
	for _, entry := range env {
		name := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			name = entry[:idx]
		}
		if _, ok := overrides[name]; ok {
			continue
		}
		out = append(out, entry)
	}
	for name, value := range overrides {
		out = append(out, name+"="+value)
	}
	return out
}

func githubWebhookSortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
