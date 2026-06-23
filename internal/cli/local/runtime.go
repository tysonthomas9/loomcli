package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/webui/frontendassets"
)

const (
	runtimeFileName      = "runtime.json"
	terminalHostFileName = "terminal-host.json"
	logsDirName          = "logs"
)

type runtimeInfo struct {
	Version          int       `json:"version"`
	Status           string    `json:"status"`
	PID              int       `json:"pid"`
	ServePID         int       `json:"serve_pid,omitempty"`
	DataDir          string    `json:"data_dir"`
	URL              string    `json:"url,omitempty"`
	Port             int       `json:"port,omitempty"`
	Executable       string    `json:"executable,omitempty"`
	BinaryHash       string    `json:"binary_hash,omitempty"`
	Build            string    `json:"build,omitempty"`
	FrontendHash     string    `json:"frontend_hash,omitempty"`
	FleetDBRedisHash string    `json:"fleetdb_redis_hash,omitempty"`
	ClaimsPaused     bool      `json:"claims_paused,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Error            string    `json:"error,omitempty"`
}

type runtimeStatus struct {
	Runtime *runtimeInfo `json:"runtime,omitempty"`
	Healthy bool         `json:"healthy"`
	Error   string       `json:"error,omitempty"`
}

type terminalHostInfo struct {
	Version         int       `json:"version"`
	ProtocolVersion int       `json:"protocol_version"`
	Status          string    `json:"status"`
	PID             int       `json:"pid"`
	DataDir         string    `json:"data_dir"`
	SocketPath      string    `json:"socket_path"`
	Build           string    `json:"build,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Healthy         bool      `json:"healthy"`
	Error           string    `json:"error,omitempty"`
}

// RuntimeSnapshot is the exported, JSON-stable view of the local runtime
// state used by higher-level workspace diagnostics.
type RuntimeSnapshot struct {
	Version          int       `json:"version"`
	Status           string    `json:"status"`
	PID              int       `json:"pid"`
	ServePID         int       `json:"serve_pid,omitempty"`
	DataDir          string    `json:"data_dir"`
	URL              string    `json:"url,omitempty"`
	Port             int       `json:"port,omitempty"`
	Executable       string    `json:"executable,omitempty"`
	BinaryHash       string    `json:"binary_hash,omitempty"`
	Build            string    `json:"build,omitempty"`
	FrontendHash     string    `json:"frontend_hash,omitempty"`
	FleetDBRedisHash string    `json:"fleetdb_redis_hash,omitempty"`
	ClaimsPaused     bool      `json:"claims_paused,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Error            string    `json:"error,omitempty"`
}

// RuntimeStatusSnapshot is the exported status payload shared by
// `loom local status` and workspace ops commands.
type RuntimeStatusSnapshot struct {
	Runtime *RuntimeSnapshot `json:"runtime,omitempty"`
	Healthy bool             `json:"healthy"`
	Error   string           `json:"error,omitempty"`
}

func runtimeSnapshot(info *runtimeInfo) *RuntimeSnapshot {
	if info == nil {
		return nil
	}
	return &RuntimeSnapshot{
		Version:          info.Version,
		Status:           info.Status,
		PID:              info.PID,
		ServePID:         info.ServePID,
		DataDir:          info.DataDir,
		URL:              info.URL,
		Port:             info.Port,
		Executable:       info.Executable,
		BinaryHash:       info.BinaryHash,
		Build:            info.Build,
		FrontendHash:     info.FrontendHash,
		FleetDBRedisHash: info.FleetDBRedisHash,
		ClaimsPaused:     info.ClaimsPaused,
		StartedAt:        info.StartedAt,
		UpdatedAt:        info.UpdatedAt,
		Error:            info.Error,
	}
}

// DefaultDataDir returns the local desktop runtime data directory after
// applying LOOM_DESKTOP_DATA_DIR / LOOM_CONFIG_DIR overrides.
func DefaultDataDir() (string, error) {
	return resolveDataDir("")
}

func resolveDataDir(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	if env := os.Getenv("LOOM_DESKTOP_DATA_DIR"); env != "" {
		return filepath.Abs(env)
	}
	if env := os.Getenv("LOOM_CONFIG_DIR"); env != "" {
		return filepath.Abs(env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Loom", "data"), nil
	}
	return filepath.Join(home, ".loom", "desktop"), nil
}

func ensureRuntimeDirs(dataDir string) error {
	for _, dir := range []string{dataDir, filepath.Join(dataDir, logsDirName)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}

func runtimePath(dataDir string) string {
	return filepath.Join(dataDir, runtimeFileName)
}

func terminalHostPath(dataDir string) string {
	return filepath.Join(dataDir, terminalHostFileName)
}

func terminalHostSocketPath(dataDir string) string {
	return filepath.Join(dataDir, "terminal-host.sock")
}

func serveLogPath(dataDir string) string {
	return filepath.Join(dataDir, logsDirName, "loom-serve.log")
}

// serveStartupLogTail returns up to maxBytes from the end of loom-serve.log,
// dropping any partial first line so the result begins at a clean line
// boundary. Returns "" when the log is missing, empty, or unreadable.
func serveStartupLogTail(dataDir string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	file, err := os.Open(serveLogPath(dataDir)) //nolint:gosec // log path derived from app data dir
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil {
		return ""
	}
	size := stat.Size()
	if size == 0 {
		return ""
	}
	var data []byte
	if size <= int64(maxBytes) {
		data, err = io.ReadAll(file)
		if err != nil {
			return ""
		}
	} else {
		start := size - int64(maxBytes)
		// Peek the byte preceding our window so we can tell whether `start`
		// already sits on a line boundary. If it does, we keep the whole
		// window; otherwise the first line is a fragment and we drop it.
		atBoundary := false
		if start > 0 {
			var prev [1]byte
			if _, err := file.ReadAt(prev[:], start-1); err != nil {
				return ""
			}
			atBoundary = prev[0] == '\n'
		}
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			return ""
		}
		data, err = io.ReadAll(file)
		if err != nil {
			return ""
		}
		if !atBoundary {
			if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
				data = data[idx+1:]
			} else {
				return ""
			}
		}
	}
	return strings.TrimRight(string(data), " \t\r\n")
}

func serviceLogPath(dataDir string) string {
	return filepath.Join(dataDir, logsDirName, "loom-local-service.log")
}

func daemonLogPath(dataDir string) string {
	return filepath.Join(dataDir, logsDirName, "loom-daemon.log")
}

func terminalHostLogPath(dataDir string) string {
	return filepath.Join(dataDir, logsDirName, "loom-terminal-host.log")
}

func readRuntime(dataDir string) (*runtimeInfo, error) {
	data, err := os.ReadFile(runtimePath(dataDir)) //nolint:gosec // user-selected Loom data dir
	if err != nil {
		return nil, err
	}
	var info runtimeInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse %s: %w", runtimePath(dataDir), err)
	}
	return &info, nil
}

func writeRuntime(dataDir string, info *runtimeInfo) error {
	if info == nil {
		return errors.New("runtime info is nil")
	}
	if err := ensureRuntimeDirs(dataDir); err != nil {
		return err
	}
	info.Version = 1
	info.DataDir = dataDir
	info.UpdatedAt = time.Now().UTC()
	if info.StartedAt.IsZero() {
		info.StartedAt = info.UpdatedAt
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime: %w", err)
	}
	path := runtimePath(dataDir)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil { //nolint:gosec // user-private runtime metadata
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

func readTerminalHostInfo(dataDir string) (*terminalHostInfo, error) {
	data, err := os.ReadFile(terminalHostPath(dataDir)) //nolint:gosec // user-selected Loom data dir
	if err != nil {
		return nil, err
	}
	var info terminalHostInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse %s: %w", terminalHostPath(dataDir), err)
	}
	return &info, nil
}

func writeTerminalHostInfo(dataDir string, info *terminalHostInfo) error {
	if info == nil {
		return errors.New("terminal host info is nil")
	}
	if err := ensureRuntimeDirs(dataDir); err != nil {
		return err
	}
	info.Version = 1
	info.DataDir = dataDir
	info.UpdatedAt = time.Now().UTC()
	if info.StartedAt.IsZero() {
		info.StartedAt = info.UpdatedAt
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal terminal host runtime: %w", err)
	}
	path := terminalHostPath(dataDir)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil { //nolint:gosec // user-private runtime metadata
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

type executableIdentity struct {
	Path         string
	Hash         string
	Build        string
	FrontendHash string
}

func currentExecutableIdentity() executableIdentity {
	exe, err := os.Executable()
	if err != nil {
		return executableIdentity{Build: cli.Build, FrontendHash: currentFrontendHash()}
	}
	hash := executableHash(exe)
	return executableIdentity{
		Path:         exe,
		Hash:         hash,
		Build:        cli.Build,
		FrontendHash: currentFrontendHash(),
	}
}

func executableHash(path string) string {
	file, err := os.Open(path) //nolint:gosec // hashing the current Loom executable
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return ""
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func applyExecutableIdentity(info *runtimeInfo, identity executableIdentity) {
	if info == nil {
		return
	}
	info.Executable = identity.Path
	info.BinaryHash = identity.Hash
	info.Build = identity.Build
	info.FrontendHash = identity.FrontendHash
}

func runtimeMatchesExecutable(info *runtimeInfo, identity executableIdentity) bool {
	if info == nil {
		return false
	}
	if identity.Hash == "" {
		// If we cannot fingerprint the current binary, avoid restarting a
		// healthy runtime just because the host filesystem denied the hash read.
		return true
	}
	if info.BinaryHash != identity.Hash {
		return false
	}
	if identity.FrontendHash == "" {
		// Same conservative fallback as the binary hash: a transient read
		// failure of the current bundle must not force-stop a healthy runtime.
		return true
	}
	return info.FrontendHash == identity.FrontendHash
}

func currentFrontendHash() string {
	dir := bundledFrontendDir()
	if dir == "" {
		return ""
	}
	hash, err := frontendassets.HashDir(dir)
	if err != nil {
		return ""
	}
	return hash
}

func currentFleetDBRedisHash(dataDir string) (string, error) {
	settings, err := localsettings.Load(dataDir)
	if err != nil {
		return "", err
	}
	return localsettings.RuntimeHash(settings.FleetDBRedis), nil
}

func runtimeMatchesFleetDBRedisSettings(info *runtimeInfo, currentHash string) bool {
	if info == nil {
		return false
	}
	return info.FleetDBRedisHash == currentHash
}

func checkRuntimeHealth(ctx context.Context, url string) error {
	if url == "" {
		return errors.New("runtime URL is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/health", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/api/health returned %d", resp.StatusCode)
	}
	return nil
}

func waitForRuntime(ctx context.Context, url string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if err := checkRuntimeHealth(ctx, url); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// runtimeReadyResponse mirrors webui/handlers/health.RuntimeReadyResponse on
// the wire. Kept local to avoid a CLI → webui import cycle.
type runtimeReadyResponse struct {
	Ready     bool   `json:"ready"`
	Mode      string `json:"mode"`
	Workspace string `json:"workspace"`
	Reason    string `json:"reason,omitempty"`
}

// WaitForWorkspaceReady polls /api/workspaces/{workspaceKey}/readyz
// until it returns 200 or the context is canceled. On timeout, the returned
// error includes the last decoded reason so operators see the actual cause
// (e.g. "workspace not registered: LOOM") rather than a bare
// "context deadline exceeded".
func WaitForWorkspaceReady(ctx context.Context, baseURL, workspaceKey string) error {
	if baseURL == "" {
		return errors.New("runtime URL is empty")
	}
	if workspaceKey == "" {
		return errors.New("workspace key is empty")
	}
	endpoint := baseURL + "/api/workspaces/" + url.PathEscape(workspaceKey) + "/readyz"
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastReason string
	for {
		reason, ok, err := probeWorkspaceReady(ctx, endpoint)
		if ok {
			return nil
		}
		if reason != "" {
			lastReason = reason
		} else if err != nil {
			lastReason = err.Error()
		}
		select {
		case <-ctx.Done():
			if lastReason != "" {
				return fmt.Errorf("workspace %q runtime not ready: %s", workspaceKey, lastReason)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// probeWorkspaceReady performs a single GET against the workspace readiness
// endpoint. Returns (reason, ready, transportErr). reason is decoded from the
// JSON body when available, or a synthesized "HTTP N" string otherwise.
func probeWorkspaceReady(ctx context.Context, endpoint string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", true, nil
	}
	var decoded runtimeReadyResponse
	if jsonErr := json.NewDecoder(resp.Body).Decode(&decoded); jsonErr == nil && decoded.Reason != "" {
		return decoded.Reason, false, nil
	}
	return fmt.Sprintf("HTTP %d", resp.StatusCode), false, nil
}

func processRunning(pid int) bool {
	return pid > 0 && lockfile.IsProcessRunning(pid)
}

func localEnv(dataDir string, port int) []string {
	url := "http://127.0.0.1:" + strconv.Itoa(port)
	env := append(os.Environ(),
		"LOOM_CONFIG_DIR="+dataDir,
		"LOOM_LOCAL_RUNTIME=desktop",
		"LOOM_ISSUE_BACKEND=fleetdb",
		"LOOM_SERVER_URL=",
		"LOOM_WORKSPACE_RUNTIME_DIR="+dataDir,
		"LOOM_WEBUI_URL="+url,
		"LOOM_WORKSPACE=",
		"LOOM_FLEET_DB_URL=",
		"LOOM_FLEET_URL=",
	)
	if socket := terminalHostSocketPath(dataDir); socket != "" {
		env = append(env, "LOOM_TERMINAL_HOST_SOCKET="+socket)
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		env = append(env, "PATH="+desktopRuntimePath(exeDir, os.Getenv("PATH")))
	}
	if os.Getenv("FLEET_DB_BIN") == "" {
		if fleetDBBin := bundledExecutable("fleet-db"); fleetDBBin != "" {
			env = append(env, "FLEET_DB_BIN="+fleetDBBin)
		}
	}
	if os.Getenv("LOOM_FRONTEND_DIR") == "" {
		if frontendDir := bundledFrontendDir(); frontendDir != "" {
			env = append(env, "LOOM_FRONTEND_DIR="+frontendDir)
		}
	}
	return env
}

func desktopRuntimePath(exeDir, currentPath string) string {
	seen := map[string]struct{}{}
	parts := make([]string, 0, 8)
	addPath := func(path string) {
		for _, part := range filepath.SplitList(path) {
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			parts = append(parts, part)
		}
	}
	addPath(exeDir)
	addPath(currentPath)
	addPath("/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")
	return strings.Join(parts, string(os.PathListSeparator))
}

func bundledExecutable(baseName string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for _, name := range []string{baseName, baseName + ".exe"} {
		candidate := filepath.Join(dir, name)
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	prefix := baseName + "-"
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		candidate := filepath.Join(dir, entry.Name())
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0111 != 0
}

func bundledFrontendDir() string {
	if dir := os.Getenv("LOOM_FRONTEND_DIR"); frontendassets.HasIndex(dir) {
		return dir
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exeDir := filepath.Dir(exe)
	for _, candidate := range []string{
		filepath.Join(exeDir, "webui"),
		filepath.Join(exeDir, "..", "Resources", "webui"),
		filepath.Join(exeDir, "..", "Resources", "resources", "webui"),
	} {
		if frontendassets.HasIndex(candidate) {
			return candidate
		}
	}
	return ""
}
