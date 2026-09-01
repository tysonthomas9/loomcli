package supervisor

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/metrics/spawnmetrics"
)

// newSpawnMetricsSupervisor builds a supervisor whose spawn path is inert
// except for the branch under test: no workspace (so materializeSkills is a
// no-op), no control plane, and a recorder writing into a temp dir. It returns
// the snapshot path so the test can read the counters back after Flush.
func newSpawnMetricsSupervisor(t *testing.T, backend string) (*Supervisor, string) {
	t.Helper()
	snapPath := spawnmetrics.SnapshotPath(t.TempDir())
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}, Backend: backend}
		},
		ProjectDir:    t.TempDir(),
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		EmitEvent:     func(events.Event) {},
		SpawnMetrics:  spawnmetrics.NewRecorder(snapPath),
	}
	return s, snapPath
}

// readSpawnRow flushes the recorder and returns the row matching role/status/class.
func readSpawnRow(t *testing.T, s *Supervisor, path, role, status string, class spawnmetrics.Class) spawnmetrics.SpawnRow {
	t.Helper()
	if err := s.SpawnMetrics.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	snap, err := spawnmetrics.Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	for _, row := range snap.Spawns {
		if row.Role == role && row.Status == status && row.ErrorClass == class {
			return row
		}
	}
	t.Fatalf("no row for role=%q status=%q class=%q in %+v", role, status, class, snap.Spawns)
	return spawnmetrics.SpawnRow{}
}

// stubLoomExecutablePath swaps the loom-binary resolver for the duration of a
// test. TestMain already points it at /bin/false; these tests need it to fail,
// or to name a path that does not exist.
func stubLoomExecutablePath(t *testing.T, fn func() (string, error)) {
	t.Helper()
	prev := loomExecutablePath
	loomExecutablePath = fn
	t.Cleanup(func() { loomExecutablePath = prev })
}

func TestSpawnAgent_BackendUnavailable_CountsFailure(t *testing.T) {
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: false}, nil
	})

	s, snapPath := newSpawnMetricsSupervisor(t, "codex")
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "test"},
		WorktreePath: t.TempDir(),
	}

	if err := s.spawnAgent(ap); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("spawnAgent error = %v, want ErrBackendUnavailable", err)
	}

	row := readSpawnRow(t, s, snapPath, "plan", "failure", spawnmetrics.ClassBackendUnavailable)
	if row.Count != 1 {
		t.Errorf("count = %d, want 1", row.Count)
	}
}

func TestSpawnAgent_BuildCommandFailure_CountsFailure(t *testing.T) {
	// buildCommand's first step resolves the loom binary; failing there is the
	// one build failure reachable without a half-built exec.Cmd. The role must
	// be a built-in one, or the command build fails on the missing prompt_file
	// instead and the test would pass for the wrong reason.
	stubLoomExecutablePath(t, func() (string, error) { return "", errors.New("no executable") })

	s, snapPath := newSpawnMetricsSupervisor(t, "")
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "test"},
		WorktreePath: t.TempDir(),
	}

	if err := s.spawnAgent(ap); err == nil {
		t.Fatal("spawnAgent must fail when the loom binary cannot be resolved")
	}

	row := readSpawnRow(t, s, snapPath, "plan", "failure", spawnmetrics.ClassBuildCommand)
	if row.Count != 1 {
		t.Errorf("count = %d, want 1", row.Count)
	}
}

func TestSpawnAgent_StartFailure_CountsFailure(t *testing.T) {
	// A path that builds a valid command but cannot exec: cmd.Start() fails
	// with ENOENT, which is the "start" branch and nothing earlier.
	missing := filepath.Join(t.TempDir(), "definitely-not-here")
	stubLoomExecutablePath(t, func() (string, error) { return missing, nil })

	s, snapPath := newSpawnMetricsSupervisor(t, "")
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "test"},
		WorktreePath: t.TempDir(),
	}
	cleanupAgentProcess(t, ap)

	if err := s.spawnAgent(ap); err == nil {
		t.Fatal("spawnAgent must fail when the command binary is absent")
	}

	row := readSpawnRow(t, s, snapPath, "task", "failure", spawnmetrics.ClassStart)
	if row.Count != 1 {
		t.Errorf("count = %d, want 1", row.Count)
	}
}

// TestSpawnAgent_Success_CountsSuccess is also the regression guard for the
// RecordSuccess call site: it must sit outside ap.Mu, and running this under
// -race with the lock still held would surface the ordering mistake.
func TestSpawnAgent_Success_CountsSuccess(t *testing.T) {
	// A real binary that execs and exits immediately: the spawn succeeded even
	// though the "agent" is over in milliseconds. Resolved through PATH because
	// true lives in /bin on Linux and /usr/bin on macOS.
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no true binary on PATH: %v", err)
	}
	stubLoomExecutablePath(t, func() (string, error) { return truePath, nil })

	s, snapPath := newSpawnMetricsSupervisor(t, "")
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "test"},
		WorktreePath: t.TempDir(),
	}
	cleanupAgentProcess(t, ap)

	before := time.Now().Unix()
	if err := s.spawnAgent(ap); err != nil {
		t.Fatalf("spawnAgent: %v", err)
	}

	row := readSpawnRow(t, s, snapPath, "task", "success", spawnmetrics.ClassNone)
	if row.Count != 1 {
		t.Errorf("count = %d, want 1", row.Count)
	}

	snap, loadErr := spawnmetrics.Load(snapPath)
	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	if snap.LastSuccessfulSpawnUnix < before || snap.LastSuccessfulSpawnUnix > time.Now().Unix()+1 {
		t.Errorf("LastSuccessfulSpawnUnix = %d, want within [%d, now]", snap.LastSuccessfulSpawnUnix, before)
	}
}

// TestSpawnAgent_NoBareRecordErr keeps every failure branch of spawnAgent
// counted. A new branch added with the plain span helper would be tagged on
// the trace but invisible to the metric, which is precisely the gap this
// wiring exists to close.
func TestSpawnAgent_NoBareRecordErr(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "spawn.go", nil, 0)
	if err != nil {
		t.Fatalf("parse spawn.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if ok && d.Name.Name == "spawnAgent" {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatal("spawnAgent not found in spawn.go")
	}

	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "recordErr" {
			t.Errorf("%s: bare recordErr in spawnAgent — use s.recordSpawnFailure so the failure is counted as well as traced",
				fset.Position(call.Pos()))
		}
		return true
	})
}

// TestCreateAgentSession_StampsRole covers the reader side of the metric: the
// session index must carry the role, not just the worktree name.
func TestCreateAgentSession_StampsRole(t *testing.T) {
	runtimeDir := t.TempDir()
	setWorkspaceRuntimeDir(t, runtimeDir)

	s, _ := newSpawnMetricsSupervisor(t, "")
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "integrator"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "test"},
		WorktreePath: t.TempDir(),
	}

	s.createAgentSession(ap, "")

	if ap.Session == nil {
		t.Fatal("createAgentSession did not create a session")
	}
	index, err := os.ReadFile(filepath.Join(runtimeDir, "sessions", "index.jsonl"))
	if err != nil {
		t.Fatalf("read index.jsonl: %v", err)
	}
	if want := `"role":"integrator"`; !strings.Contains(string(index), want) {
		t.Errorf("index.jsonl does not carry %s:\n%s", want, index)
	}
}

// setWorkspaceRuntimeDir points cli.GetWorkspaceRuntimeDir at dir for the test,
// resetting the process-wide cache on both sides so neither this test nor its
// neighbours see a stale value.
func setWorkspaceRuntimeDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)
}
