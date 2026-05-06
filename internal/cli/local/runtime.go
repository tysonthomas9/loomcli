package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

const (
	runtimeFileName = "runtime.json"
	logsDirName     = "logs"
)

type runtimeInfo struct {
	Version      int       `json:"version"`
	Status       string    `json:"status"`
	PID          int       `json:"pid"`
	ServePID     int       `json:"serve_pid,omitempty"`
	DataDir      string    `json:"data_dir"`
	URL          string    `json:"url,omitempty"`
	Port         int       `json:"port,omitempty"`
	ClaimsPaused bool      `json:"claims_paused,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Error        string    `json:"error,omitempty"`
}

type runtimeStatus struct {
	Runtime *runtimeInfo `json:"runtime,omitempty"`
	Healthy bool         `json:"healthy"`
	Error   string       `json:"error,omitempty"`
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

func serveLogPath(dataDir string) string {
	return filepath.Join(dataDir, logsDirName, "loom-serve.log")
}

func serviceLogPath(dataDir string) string {
	return filepath.Join(dataDir, logsDirName, "loom-local-service.log")
}

func daemonLogPath(dataDir string) string {
	return filepath.Join(dataDir, logsDirName, "loom-daemon.log")
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

func processRunning(pid int) bool {
	return pid > 0 && lockfile.IsProcessRunning(pid)
}

func localEnv(dataDir string, port int) []string {
	url := "http://127.0.0.1:" + strconv.Itoa(port)
	env := append(os.Environ(),
		"LOOM_CONFIG_DIR="+dataDir,
		"LOOM_ISSUE_BACKEND=fleetdb",
		"LOOM_SERVER_URL=",
		"LOOM_WORKSPACE_RUNTIME_DIR="+dataDir,
		"LOOM_WEBUI_URL="+url,
		"LOOM_WORKSPACE=",
		"LOOM_FLEET_DB_URL=",
		"LOOM_FLEET_URL=",
	)
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
	if dir := os.Getenv("LOOM_FRONTEND_DIR"); hasFrontendIndex(dir) {
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
		if hasFrontendIndex(candidate) {
			return candidate
		}
	}
	return ""
}

func hasFrontendIndex(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}
