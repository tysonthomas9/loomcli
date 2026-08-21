package placement_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdkdaytona "github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	sdktypes "github.com/daytonaio/daytona/libs/sdk-go/pkg/types"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/placement"
	"github.com/tysonthomas9/loomcli/internal/placement/daytona"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestLiveLeadDoesRealRepoWork is the goal-acceptance chain: a lead placed in a
// real Daytona sandbox, booted with the codex backend against a real cloned
// repo, is given a task through its PTY and observed reading actual repo files.
//
// It requires, beyond the DAYTONA_API_KEY the other live tests need:
//   - LOOM_LEAD_WORK_TEST=1                 (explicit opt-in; this spends money
//     and uses the seeded ChatGPT account)
//   - LOOM_CODEX_AUTH_FILE=/path/auth.json  (codex OAuth/credentials to seed)
//   - LOOM_E2E_REPO_URL / LOOM_E2E_REPO_TOKEN (repo to clone; token for private)
//
// It always releases the sandbox.
func TestLiveLeadDoesRealRepoWork(t *testing.T) {
	if os.Getenv("LOOM_DAYTONA_LIVE_TEST") != "1" || os.Getenv("LOOM_LEAD_WORK_TEST") != "1" || os.Getenv("DAYTONA_API_KEY") == "" {
		t.Skip("live work test disabled")
	}
	authFile := strings.TrimSpace(os.Getenv("LOOM_CODEX_AUTH_FILE"))
	if authFile == "" {
		t.Skip("LOOM_CODEX_AUTH_FILE not set")
	}
	authJSON, err := os.ReadFile(authFile) //nolint:gosec // operator-supplied path, intentional
	if err != nil {
		t.Fatalf("read codex auth file: %v", err)
	}

	provider, err := daytona.New(daytona.Config{SnapshotName: "loom-lead-poc-v2"})
	if err != nil {
		t.Fatalf("daytona.New: %v", err)
	}
	st := memstore.New()
	key := []byte("0123456789abcdef0123456789abcdef")
	broker, err := placement.NewBroker(placement.Config{
		Store: st, Provider: provider, TokenKey: key, DeploymentID: "mac-work",
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	const ws, agent = "LIVE-WORK", "nova"
	repoURL := strings.TrimSpace(os.Getenv("LOOM_E2E_REPO_URL"))
	if repoURL == "" {
		repoURL = "https://github.com/tysonthomas9/loomcli"
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey: ws, Name: "repo", RemoteURL: repoURL, DefaultBranch: "v5",
	}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	var placedNodeID, placedSandboxID string
	t.Cleanup(func() {
		if placedNodeID == "" {
			return
		}
		cctx, ccancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer ccancel()
		node, getErr := broker.Get(cctx, ws, placedNodeID)
		if getErr != nil || node == nil || node.Placement == nil || node.Placement.State == domain.PlacementStateReleased {
			return
		}
		if _, relErr := broker.Release(cctx, ws, placedNodeID, placement.ReleaseFence{
			Generation: node.Placement.Generation, Force: true,
		}); relErr != nil {
			t.Errorf("CLEANUP: force release failed, sandbox %q may still bill: %v", placedSandboxID, relErr)
		} else {
			t.Logf("CLEANUP: force-released %s", placedNodeID)
		}
	})

	var gitToken func() (string, error)
	if tok := strings.TrimSpace(os.Getenv("LOOM_E2E_REPO_TOKEN")); tok != "" {
		gitToken = func() (string, error) { return tok, nil }
	}

	start := time.Now()
	res, err := broker.Provision(ctx, placement.ProvisionRequest{
		WorkspaceKey: ws,
		AgentName:    agent,
		SnapshotRef:  "loom-lead-poc-v2",
		Caps:         []string{placement.CapLeadSession},
		Resource:     placement.ResourceSize{VCPU: 2, MemGiB: 4},
		Backend:      "codex",
		GitToken:     gitToken,
		SeedFiles: []placement.SandboxFile{
			{Path: "/root/.codex/auth.json", Content: authJSON, Mode: "600"},
		},
		NetworkDomainAllowlist: []string{
			"app.daytona.io", "github.com", "registry.npmjs.org",
			"chatgpt.com", "auth.openai.com", "api.openai.com",
		},
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	placedNodeID = res.Node.NodeID
	placedSandboxID = res.Node.Placement.SandboxID
	t.Logf("provisioned in %s: node=%s sandbox=%s leadStarted=%v err=%q",
		time.Since(start).Round(time.Millisecond), placedNodeID, placedSandboxID, res.LeadStarted, res.LeadStartError)
	if !res.LeadStarted || res.LeadStartError != "" {
		t.Fatalf("lead did not boot: started=%v err=%q", res.LeadStarted, res.LeadStartError)
	}

	// Attach to the lead PTY the way the browser terminal does, and drive it.
	client, err := sdkdaytona.NewClientWithConfig(&sdktypes.DaytonaConfig{APIKey: os.Getenv("DAYTONA_API_KEY")})
	if err != nil {
		t.Fatalf("sdk client: %v", err)
	}
	sandbox, err := client.Get(ctx, placedSandboxID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	handle, err := sandbox.Process.ConnectPty(ctx, "lead")
	if err != nil {
		t.Fatalf("ConnectPty(lead): %v", err)
	}
	defer func() { _ = handle.Disconnect() }()
	if err := handle.WaitForConnection(ctx); err != nil {
		t.Fatalf("WaitForConnection: %v", err)
	}

	var mu sync.Mutex
	var buf strings.Builder
	go func() {
		for chunk := range handle.DataChan() {
			mu.Lock()
			buf.Write(chunk)
			mu.Unlock()
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return stripControl(buf.String())
	}

	// Wait until codex settles: the lead's own startup turn runs first (it may
	// survey the backlog), so hold until the transcript stops growing.
	waitIdle := func(quiet, max time.Duration) {
		last := ""
		stableSince := time.Now()
		hardStop := time.Now().Add(max)
		for time.Now().Before(hardStop) {
			time.Sleep(3 * time.Second)
			cur := snapshot()
			if cur != last {
				last = cur
				stableSince = time.Now()
				continue
			}
			if time.Since(stableSince) >= quiet {
				return
			}
		}
	}
	waitIdle(12*time.Second, 3*time.Minute)

	// A task answerable only by reading the actual clone -- no fleet-db, no
	// network. Clear the composer first (Esc), then send the text and a
	// DISCRETE Enter: a newline folded into the paste is treated as a literal
	// line break by the codex TUI, not a submit.
	const marker = "REPO-WORK-PROOF"
	task := "Please run this exact shell command in your working directory and then tell me its output: " +
		"printf '" + marker + "=%s\\n' \"$(head -1 README.md | tr -d '#' | xargs)\"; echo ENTRIES:; ls -1"
	if err := handle.SendInput([]byte{0x1b}); err != nil { // Esc: drop any queued composer text
		t.Fatalf("SendInput esc: %v", err)
	}
	time.Sleep(1 * time.Second)
	if err := handle.SendInput([]byte(task)); err != nil {
		t.Fatalf("SendInput task: %v", err)
	}
	time.Sleep(2 * time.Second)
	if err := handle.SendInput([]byte("\r")); err != nil { // discrete Enter -> submit
		t.Fatalf("SendInput enter: %v", err)
	}

	// Poll for evidence the lead executed the command against the real checkout.
	deadline := time.Now().Add(4 * time.Minute)
	var got string
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Second)
		got = snapshot()
		if strings.Contains(got, marker+"=") && strings.Contains(got, "ENTRIES:") &&
			(strings.Contains(got, "go.mod") || strings.Contains(got, "internal")) {
			break
		}
	}
	t.Logf("=== lead PTY transcript (%d bytes) ===\n%s", len(got), tail(got, 5000))
	if !strings.Contains(got, marker+"=") {
		t.Fatalf("no %s= line: the lead did not run the README command", marker)
	}
	if !strings.Contains(got, "go.mod") && !strings.Contains(got, "internal") {
		t.Fatalf("lead did not list real repo entries (expected go.mod/internal in `ls -1`)")
	}
	t.Log("PROOF: the lead ran a shell command against the real cloned repo and returned its output")

	released, err := broker.Release(ctx, ws, placedNodeID, placement.ReleaseFence{
		Generation: res.Node.Placement.Generation, SandboxID: placedSandboxID,
	})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if released.Placement.State != domain.PlacementStateReleased {
		t.Errorf("state = %q, want released", released.Placement.State)
	}
	placedNodeID = ""
}

// stripControl removes ANSI escape sequences and control bytes so the terminal
// transcript is greppable.
func stripControl(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i++
			if i < len(s) && s[i] == '[' {
				i++
				for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
					i++
				}
				if i < len(s) {
					i++
				}
				continue
			}
			if i < len(s) && s[i] == ']' {
				for i < len(s) && s[i] != 0x07 {
					i++
				}
				if i < len(s) {
					i++
				}
				continue
			}
			continue
		}
		if s[i] == '\r' || s[i] >= 0x20 || s[i] == '\n' || s[i] == '\t' {
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String()
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...[head truncated]\n" + s[len(s)-n:]
}
