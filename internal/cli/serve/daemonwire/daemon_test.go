package daemonwire

import (
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

// stubWsListFn is a no-op wsListFn used for tests. The resolver does not
// invoke it, but BuildWorkspaceDaemonResolver requires the argument.
func stubWsListFn() (map[string]string, error) {
	return nil, nil
}

func TestBuildWorkspaceDaemonResolver_ValidWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	const wsID = "ws-abc"

	cfgFn := func(id string) (*ops.WorkspaceData, error) {
		if id != wsID {
			t.Errorf("cfgFn called with %q, want %q", id, wsID)
		}
		return &ops.WorkspaceData{
			ID:   wsID,
			Name: "test-workspace",
			Path: tmpDir,
		}, nil
	}

	resolver := BuildWorkspaceDaemonResolver(cfgFn, stubWsListFn)
	paths, err := resolver(wsID)
	if err != nil {
		t.Fatalf("resolver returned unexpected error: %v", err)
	}
	if paths == nil {
		t.Fatal("resolver returned nil paths")
	}

	if paths.SocketPath == "" {
		t.Errorf("SocketPath is empty")
	}
	if paths.StatePath == "" {
		t.Errorf("StatePath is empty")
	}
	if paths.ConfigPath == "" {
		t.Errorf("ConfigPath is empty")
	}
	if paths.WorkDir == "" {
		t.Errorf("WorkDir is empty")
	}

	if !strings.HasSuffix(paths.SocketPath, "daemon.sock") {
		t.Errorf("SocketPath %q does not end with daemon.sock", paths.SocketPath)
	}
	if !strings.HasSuffix(paths.StatePath, "daemon-agents.json") {
		t.Errorf("StatePath %q does not end with daemon-agents.json", paths.StatePath)
	}
	if !strings.HasSuffix(paths.ConfigPath, "loom.yaml") {
		t.Errorf("ConfigPath %q does not end with loom.yaml", paths.ConfigPath)
	}
	if paths.WorkDir != tmpDir {
		t.Errorf("WorkDir = %q, want %q", paths.WorkDir, tmpDir)
	}
}

func TestBuildWorkspaceDaemonResolver_NotFound(t *testing.T) {
	const wsID = "ws-missing"
	backingErr := errors.New("workspace not in config")

	cfgFn := func(id string) (*ops.WorkspaceData, error) {
		return nil, backingErr
	}

	resolver := BuildWorkspaceDaemonResolver(cfgFn, stubWsListFn)
	paths, err := resolver(wsID)
	if err == nil {
		t.Fatalf("expected error, got nil (paths=%+v)", paths)
	}
	if paths != nil {
		t.Errorf("expected nil paths on error, got %+v", paths)
	}
	if !strings.Contains(err.Error(), wsID) {
		t.Errorf("error %q does not contain wsID %q", err.Error(), wsID)
	}
}

func TestBuildWorkspaceDaemonResolver_EmptyID(t *testing.T) {
	var called bool
	cfgFn := func(id string) (*ops.WorkspaceData, error) {
		called = true
		return &ops.WorkspaceData{Path: "/unused"}, nil
	}

	resolver := BuildWorkspaceDaemonResolver(cfgFn, stubWsListFn)
	paths, err := resolver("")
	if err == nil {
		t.Fatalf("expected error for empty wsID, got nil (paths=%+v)", paths)
	}
	if paths != nil {
		t.Errorf("expected nil paths on error, got %+v", paths)
	}
	if called {
		t.Errorf("cfgFn should not be invoked for empty wsID")
	}
}

func TestBuildWorkspaceDaemonResolver_NilWorkspaceData(t *testing.T) {
	const wsID = "ws-nil"
	cfgFn := func(id string) (*ops.WorkspaceData, error) {
		return nil, nil
	}

	resolver := BuildWorkspaceDaemonResolver(cfgFn, stubWsListFn)
	paths, err := resolver(wsID)
	if err == nil {
		t.Fatalf("expected error for nil workspace data, got nil (paths=%+v)", paths)
	}
	if paths != nil {
		t.Errorf("expected nil paths on error, got %+v", paths)
	}
}

func TestBuildWorkspaceDaemonResolver_NilConfigFn(t *testing.T) {
	// When wsConfigByIDFn is nil, the builder returns nil so callers can
	// detect missing wiring at construction time (same pattern as the other Build* functions).
	resolver := BuildWorkspaceDaemonResolver(nil, stubWsListFn)
	if resolver != nil {
		t.Errorf("expected nil resolver when wsConfigByIDFn is nil, got non-nil")
	}
}

func TestBuildWorkspaceDaemonResolver_MultipleWorkspaces(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	workspaces := map[string]*ops.WorkspaceData{
		"ws-a": {ID: "ws-a", Name: "alpha", Path: dirA},
		"ws-b": {ID: "ws-b", Name: "beta", Path: dirB},
	}

	cfgFn := func(id string) (*ops.WorkspaceData, error) {
		ws, ok := workspaces[id]
		if !ok {
			return nil, errors.New("not found")
		}
		return ws, nil
	}

	resolver := BuildWorkspaceDaemonResolver(cfgFn, stubWsListFn)

	cases := []struct {
		wsID    string
		wantDir string
	}{
		{"ws-a", dirA},
		{"ws-b", dirB},
	}

	results := make([]*webui.WorkspaceDaemonPaths, 0, len(cases))
	for _, tc := range cases {
		paths, err := resolver(tc.wsID)
		if err != nil {
			t.Fatalf("resolver(%q) returned error: %v", tc.wsID, err)
		}
		if paths == nil {
			t.Fatalf("resolver(%q) returned nil paths", tc.wsID)
		}
		if paths.WorkDir != tc.wantDir {
			t.Errorf("resolver(%q).WorkDir = %q, want %q", tc.wsID, paths.WorkDir, tc.wantDir)
		}
		if !strings.HasSuffix(paths.SocketPath, "daemon.sock") {
			t.Errorf("resolver(%q).SocketPath %q does not end with daemon.sock", tc.wsID, paths.SocketPath)
		}
		if !strings.HasSuffix(paths.StatePath, "daemon-agents.json") {
			t.Errorf("resolver(%q).StatePath %q does not end with daemon-agents.json", tc.wsID, paths.StatePath)
		}
		if !strings.HasSuffix(paths.ConfigPath, "loom.yaml") {
			t.Errorf("resolver(%q).ConfigPath %q does not end with loom.yaml", tc.wsID, paths.ConfigPath)
		}
		if !strings.HasPrefix(paths.ConfigPath, tc.wantDir) {
			t.Errorf("resolver(%q).ConfigPath %q does not start with %q", tc.wsID, paths.ConfigPath, tc.wantDir)
		}
		results = append(results, paths)
	}

	// Assert results are distinct across workspaces.
	if results[0].WorkDir == results[1].WorkDir {
		t.Errorf("expected distinct WorkDirs, both = %q", results[0].WorkDir)
	}
	if results[0].SocketPath == results[1].SocketPath {
		t.Errorf("expected distinct SocketPaths, both = %q", results[0].SocketPath)
	}
	if results[0].StatePath == results[1].StatePath {
		t.Errorf("expected distinct StatePaths, both = %q", results[0].StatePath)
	}
	if results[0].ConfigPath == results[1].ConfigPath {
		t.Errorf("expected distinct ConfigPaths, both = %q", results[0].ConfigPath)
	}
}
