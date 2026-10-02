// Package realloom runs the real pieces of a Loom agent test in an owned
// /tmp sandbox: the pinned OpenCode build as its own user, a freshly built
// loom binary, and a `loom serve` child process on an owned fleet-db. Tests
// that set LOOM_REAL_OPENCODE=1 use it; nothing here touches the user's
// OpenCode, ~/.loom or a running stack.
package realloom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/netutil"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Skip skips t unless LOOM_REAL_OPENCODE=1.
func Skip(t *testing.T) {
	t.Helper()
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
	}
}

// OpenCodeBin is the pinned OpenCode build (LOOM_OPENCODE_BIN overrides).
func OpenCodeBin() string {
	if bin := os.Getenv("LOOM_OPENCODE_BIN"); bin != "" {
		return bin
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
}

// Sandbox is an owned /tmp dir for one OpenCode user, with a git repo at
// repo/ and a service config on a free loopback port. Cleanup stops the
// OpenCode service registered there and removes the dir.
type Sandbox struct {
	Dir  string
	Repo string
	Head string // the repo's one commit
}

// NewSandbox makes a Sandbox whose OpenCode uses the OpenAI-compatible
// model at modelURL.
func NewSandbox(t *testing.T, modelURL string) Sandbox {
	t.Helper()
	sbx, err := os.MkdirTemp("/tmp", "loom-real-")
	if err == nil {
		sbx, err = filepath.EvalSymlinks(sbx) // OpenCode reports resolved paths
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var reg struct{ PID int }
		if b, err := os.ReadFile(filepath.Join(sbx, "state/opencode/service.json")); err == nil && json.Unmarshal(b, &reg) == nil && reg.PID > 0 {
			_ = syscall.Kill(reg.PID, syscall.SIGTERM)
			for end := time.Now().Add(10 * time.Second); syscall.Kill(reg.PID, 0) == nil && time.Now().Before(end); {
				time.Sleep(50 * time.Millisecond)
			}
			_ = syscall.Kill(reg.PID, syscall.SIGKILL)
		}
		_ = os.RemoveAll(sbx)
	})
	for _, d := range []string{"home", "tmp", "config/opencode", "repo"} {
		if err := os.MkdirAll(filepath.Join(sbx, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, port, err := netutil.PickFreeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	WriteFile(t, filepath.Join(sbx, "config/opencode/service.json"), fmt.Sprintf(`{"port":%d}`, port))
	WriteFile(t, filepath.Join(sbx, "config/opencode/opencode.json"), fmt.Sprintf(`{"provider":{"fake":{"name":"Fake",
		"npm":"@ai-sdk/openai-compatible","options":{"baseURL":%q,"apiKey":"x"},
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}}}}},"model":"fake/m","small_model":"fake/m"}`, modelURL+"/v1"))
	repo := filepath.Join(sbx, "repo")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t",
		"commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // G204: git on the sandbox's own repo.
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output() //nolint:gosec // G204: git on the sandbox's own repo.
	if err != nil {
		t.Fatal(err)
	}
	return Sandbox{Dir: sbx, Repo: repo, Head: strings.TrimSpace(string(head))}
}

// Env is the environment of the sandbox's OpenCode user.
func (s Sandbox) Env() []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.Dir + "/home", "TMPDIR=" + s.Dir + "/tmp/",
		"XDG_DATA_HOME=" + s.Dir + "/data", "XDG_CONFIG_HOME=" + s.Dir + "/config",
		"XDG_STATE_HOME=" + s.Dir + "/state", "XDG_CACHE_HOME=" + s.Dir + "/cache", "OPENCODE_DISABLE_MODELS_FETCH=1"}
}

// BuildLoom builds cmd/loom into the sandbox and returns its path.
func (s Sandbox) BuildLoom(t *testing.T) string {
	t.Helper()
	loom := filepath.Join(s.Dir, "loom-bin")
	if out, err := exec.Command("go", "build", "-o", loom, "github.com/tysonthomas9/loomcli/cmd/loom").CombinedOutput(); err != nil { //nolint:gosec // G204: builds into the sandbox.
		t.Fatalf("build loom: %v %s", err, out)
	}
	return loom
}

// Workspace is the fleet-db workspace Serve creates.
const Workspace = "WS"

// Serve runs `loom serve` from loom on a free loopback port, on an owned
// fleet-db holding Workspace, with Loom's data in the sandbox, until the
// test ends. It returns serve's base URL once healthy. A failed test logs
// serve's output.
func (s Sandbox) Serve(t *testing.T, loom string) string {
	t.Helper()
	ctx := context.Background()
	fleet, err := bootstrap.StartEmbedded(ctx, filepath.Join(s.Dir, "fleet"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fleet.Stop() })
	fc, err := fleetdb.New(fleetdb.Config{BaseURL: fleet.URL(), Actor: "loom-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fc.Close() }()
	if _, err := fc.Workspaces().Create(ctx, store.WorkspaceCreate{Key: Workspace, Name: Workspace}); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	serve := exec.Command(loom, "serve", "--no-daemon", "--bind", "127.0.0.1", "--port", strconv.Itoa(port)) //nolint:gosec // G204: the loom binary this test built.
	logs := &syncBuffer{}
	serve.Dir, serve.Stdout, serve.Stderr = s.Repo, logs, logs
	serve.Env = append(s.Env(), "LOOM_OPENCODE_BIN="+OpenCodeBin(), "LOOM_CONFIG_DIR="+s.Dir+"/loom",
		"LOOM_WORKSPACE="+Workspace, "LOOM_FLEET_DB_URL="+fleet.URL(), "LOOM_FLEET_DB_ACTOR=loom-test",
		"LOOM_DRIVER_EXECUTOR=0", "LOOM_ISSUE_BRIDGE_DISABLED=1", "LOOM_DISABLE_H2C=1")
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = serve.Process.Signal(syscall.SIGTERM)
		_ = serve.Wait()
		if t.Failed() {
			t.Logf("loom serve output:\n%s", logs.String())
		}
	})
	for end := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if resp, err := http.Get(base + "/health"); err == nil { //nolint:gosec,noctx // G107: serve's own loopback URL.
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base
			}
		}
		if time.Now().After(end) {
			t.Fatalf("loom serve never became healthy:\n%s", logs.String())
		}
	}
}

// WriteFile writes content to path, mode 0600.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// syncBuffer is a bytes.Buffer safe for serve's stdout and stderr at once.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
