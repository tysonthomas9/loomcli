package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// createMockBackendBinary creates a shell script that acts as a mock
// loom-backend-<name> executable in the given directory.
func createMockBackendBinary(t *testing.T, dir, name string) string {
	t.Helper()
	binName := "loom-backend-" + name
	binPath := filepath.Join(dir, binName)
	script := "#!/bin/sh\necho \"$@\"\n"
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create mock binary %s: %v", binPath, err)
	}
	return binPath
}

// createNonExecutableFile creates a non-executable file matching the
// loom-backend-<name> naming convention.
func createNonExecutableFile(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, "loom-backend-"+name)
	if err := os.WriteFile(path, []byte("not executable"), 0644); err != nil {
		t.Fatalf("failed to create non-executable file: %v", err)
	}
}

func TestDiscoverExternalBackends_FindsExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test helpers not supported on Windows")
	}
	resetBackendState(t)

	dir := t.TempDir()
	createMockBackendBinary(t, dir, "foo")
	createMockBackendBinary(t, dir, "bar")

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", dir)

	DiscoverExternalBackends()

	names := ListBackends()
	if len(names) != 2 {
		t.Fatalf("expected 2 backends, got %d: %v", len(names), names)
	}
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["foo"] || !found["bar"] {
		t.Fatalf("expected foo and bar, got %v", names)
	}
}

func TestDiscoverExternalBackends_SkipsNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bit checks not meaningful on Windows")
	}
	resetBackendState(t)

	dir := t.TempDir()
	createNonExecutableFile(t, dir, "broken")

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", dir)

	DiscoverExternalBackends()

	names := ListBackends()
	if len(names) != 0 {
		t.Fatalf("expected 0 backends (non-executable skipped), got %v", names)
	}
}

func TestDiscoverExternalBackends_SkipsBuiltinConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test helpers not supported on Windows")
	}
	resetBackendState(t)

	// Register a built-in backend first
	builtin := &mockBackend{name: "mybackend"}
	RegisterBackend(builtin)

	dir := t.TempDir()
	createMockBackendBinary(t, dir, "mybackend")

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", dir)

	DiscoverExternalBackends()

	// The built-in should still be the registered one, not the external
	backendMu.RLock()
	got := backends["mybackend"]
	backendMu.RUnlock()
	if got != builtin {
		t.Fatal("expected built-in backend to take priority over external")
	}
}

func TestDiscoverExternalBackends_FirstPathWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test helpers not supported on Windows")
	}
	resetBackendState(t)

	dir1 := t.TempDir()
	dir2 := t.TempDir()
	path1 := createMockBackendBinary(t, dir1, "dup")
	createMockBackendBinary(t, dir2, "dup")

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", dir1+string(os.PathListSeparator)+dir2)

	DiscoverExternalBackends()

	backendMu.RLock()
	b, ok := backends["dup"]
	backendMu.RUnlock()
	if !ok {
		t.Fatal("expected 'dup' backend to be registered")
	}
	ext, ok := b.(*ExternalBackend)
	if !ok {
		t.Fatal("expected ExternalBackend type")
	}
	if ext.binPath != path1 {
		t.Fatalf("expected first PATH dir to win, got binPath=%q, want %q", ext.binPath, path1)
	}
}

func TestDiscoverExternalBackends_SkipsEmptyName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test helpers not supported on Windows")
	}
	resetBackendState(t)

	dir := t.TempDir()
	// Create bare "loom-backend-" (empty name after prefix)
	barePath := filepath.Join(dir, "loom-backend-")
	if err := os.WriteFile(barePath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to create bare file: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", dir)

	DiscoverExternalBackends()

	names := ListBackends()
	if len(names) != 0 {
		t.Fatalf("expected 0 backends (empty name skipped), got %v", names)
	}
}

func TestDiscoverExternalBackends_SkipsBadDirs(t *testing.T) {
	resetBackendState(t)

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", "/nonexistent/dir/that/should/not/exist")

	// Should not panic or error
	DiscoverExternalBackends()

	names := ListBackends()
	if len(names) != 0 {
		t.Fatalf("expected 0 backends, got %v", names)
	}
}

func TestDiscoverExternalBackends_EmptyPath(t *testing.T) {
	resetBackendState(t)

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", "")

	// Should not panic or error
	DiscoverExternalBackends()

	names := ListBackends()
	if len(names) != 0 {
		t.Fatalf("expected 0 backends, got %v", names)
	}
}

func TestDiscoverExternalBackends_SkipsDirectories(t *testing.T) {
	resetBackendState(t)

	dir := t.TempDir()
	// Create a directory named loom-backend-dirbackend
	subdir := filepath.Join(dir, "loom-backend-dirbackend")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", dir)

	DiscoverExternalBackends()

	names := ListBackends()
	if len(names) != 0 {
		t.Fatalf("expected 0 backends (directories skipped), got %v", names)
	}
}

func TestExternalBackend_Name(t *testing.T) {
	eb := &ExternalBackend{name: "aider", binPath: "/usr/bin/loom-backend-aider"}
	if got := eb.Name(); got != "aider" {
		t.Fatalf("expected 'aider', got %q", got)
	}
}

func TestExternalBackend_InvokeInteractive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test helpers not supported on Windows")
	}

	// Create a mock script that records its arguments to a file
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\n"
	binPath := filepath.Join(dir, "loom-backend-test")
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create mock binary: %v", err)
	}

	eb := &ExternalBackend{name: "test", binPath: binPath}
	err := eb.InvokeInteractive(dir, "hello world", "agent1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read args file: %v", err)
	}
	got := string(data)
	if got != "invoke --interactive hello world\n" {
		t.Fatalf("expected 'invoke --interactive hello world', got %q", got)
	}
}

func TestExternalBackend_InvokeNonInteractive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test helpers not supported on Windows")
	}

	// Create a mock script that reads stdin and writes it to a file
	dir := t.TempDir()
	stdinFile := filepath.Join(dir, "stdin.txt")
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\ncat > " + stdinFile + "\n"
	binPath := filepath.Join(dir, "loom-backend-test")
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create mock binary: %v", err)
	}

	eb := &ExternalBackend{name: "test", binPath: binPath}
	shutdown := make(chan struct{})
	err := eb.InvokeNonInteractive(dir, "test prompt", "agent2", shutdown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check args
	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read args file: %v", err)
	}
	if got := string(argsData); got != "invoke --non-interactive\n" {
		t.Fatalf("expected 'invoke --non-interactive', got %q", got)
	}

	// Check stdin was passed
	stdinData, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatalf("failed to read stdin file: %v", err)
	}
	if got := string(stdinData); got != "test prompt" {
		t.Fatalf("expected 'test prompt' via stdin, got %q", got)
	}
}

func TestExternalBackend_EnvPassthrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test helpers not supported on Windows")
	}

	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	script := "#!/bin/sh\nenv > " + envFile + "\n"
	binPath := filepath.Join(dir, "loom-backend-test")
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create mock binary: %v", err)
	}

	eb := &ExternalBackend{name: "test", binPath: binPath}
	workDir := t.TempDir()
	err := eb.InvokeInteractive(workDir, "hello", "myagent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("failed to read env file: %v", err)
	}
	envStr := string(data)

	if !contains(envStr, "LOOM_WORKTREE_PATH="+workDir) {
		t.Error("expected LOOM_WORKTREE_PATH to be set in subprocess environment")
	}
	if !contains(envStr, "BD_ACTOR=myagent") {
		t.Error("expected BD_ACTOR to be set in subprocess environment")
	}
}

func TestExternalBackend_Meta_Fallback(t *testing.T) {
	// When binPath doesn't exist / meta subcommand fails, should return fallback
	eb := &ExternalBackend{name: "broken", binPath: "/nonexistent/loom-backend-broken"}
	meta := eb.Meta()
	if meta.DisplayName != "broken" {
		t.Errorf("expected fallback DisplayName 'broken', got %q", meta.DisplayName)
	}
	if meta.BinaryName != "loom-backend-broken" {
		t.Errorf("expected fallback BinaryName 'loom-backend-broken', got %q", meta.BinaryName)
	}
}

func TestExternalBackend_HealthCheck_Fallback(t *testing.T) {
	// When binPath doesn't exist / health subcommand fails, should return unhealthy
	eb := &ExternalBackend{name: "broken", binPath: "/nonexistent/loom-backend-broken"}
	hs := eb.HealthCheck()
	if hs.Healthy {
		t.Error("expected unhealthy status when binary doesn't exist")
	}
	if !hs.Installed {
		t.Error("expected Installed=true (binary was found during discovery)")
	}
	if hs.Message == "" {
		t.Error("expected a non-empty message describing the failure")
	}
}

func TestExternalBackend_Meta_ParsesJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test helpers not supported on Windows")
	}

	dir := t.TempDir()
	script := `#!/bin/sh
echo '{"display_name":"TestPlugin","version":"1.0.0","description":"A test plugin","url":"https://example.com","binary_name":"loom-backend-testplugin"}'
`
	binPath := filepath.Join(dir, "loom-backend-testplugin")
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create mock binary: %v", err)
	}

	eb := &ExternalBackend{name: "testplugin", binPath: binPath}
	meta := eb.Meta()
	if meta.DisplayName != "TestPlugin" {
		t.Errorf("expected DisplayName 'TestPlugin', got %q", meta.DisplayName)
	}
	if meta.Version != "1.0.0" {
		t.Errorf("expected Version '1.0.0', got %q", meta.Version)
	}
}

func TestExternalBackend_HealthCheck_ParsesJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test helpers not supported on Windows")
	}

	dir := t.TempDir()
	script := `#!/bin/sh
echo '{"Healthy":true,"Installed":true,"Version":"2.0","APIKeySet":true,"Message":"ready"}'
`
	binPath := filepath.Join(dir, "loom-backend-testplugin")
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create mock binary: %v", err)
	}

	eb := &ExternalBackend{name: "testplugin", binPath: binPath}
	hs := eb.HealthCheck()
	if !hs.Healthy {
		t.Error("expected Healthy=true")
	}
	if !hs.Installed {
		t.Error("expected Installed=true")
	}
	if hs.Version != "2.0" {
		t.Errorf("expected Version '2.0', got %q", hs.Version)
	}
	if hs.Message != "ready" {
		t.Errorf("expected Message 'ready', got %q", hs.Message)
	}
}

func TestIsRegistered(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockBackend{name: "claude"})

	if !IsRegistered("claude") {
		t.Error("expected IsRegistered('claude') to return true")
	}
	if IsRegistered("nonexistent") {
		t.Error("expected IsRegistered('nonexistent') to return false")
	}
}

// contains checks if haystack contains needle (line-based env check).
func contains(haystack, needle string) bool {
	for _, line := range splitLines(haystack) {
		if line == needle {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
