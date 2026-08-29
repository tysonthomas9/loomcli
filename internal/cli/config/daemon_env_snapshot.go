package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
)

// SnapshotFileName is the daemon env snapshot's basename, written next to the
// daemon PID file.
const SnapshotFileName = "daemon-env.json"

// daemonEnvSnapshotVersion is the schema version written into every snapshot.
const daemonEnvSnapshotVersion = 1

// capturedEnvPrefixes are the only environment prefixes recorded in a snapshot.
// Deliberately narrow: FLEET_* belongs to the fleet-db app, and capturing the
// whole environment would turn a diagnostic into a dump of the operator's shell.
var capturedEnvPrefixes = []string{"LOOM_", "OTEL_"}

// secretEnvKeyPattern matches keys whose values must never be written out.
var secretEnvKeyPattern = regexp.MustCompile(`(?i)(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL)`)

// DaemonEnvSnapshot is the daemon's own statement of the configuration it
// resolved at startup. Written by the daemon, read by `loom doctor`.
type DaemonEnvSnapshot struct {
	Version   int                 `json:"version"`
	PID       int                 `json:"pid"`
	Workspace string              `json:"workspace"`
	Binary    string              `json:"binary"`
	Cwd       string              `json:"cwd"`
	StartedAt time.Time           `json:"started_at"`
	Env       map[string]EnvValue `json:"env"`
}

// EnvValue is either a plain value or, for a secret-looking key, only a
// fingerprint. Never both.
type EnvValue struct {
	Value       string `json:"value,omitempty"`
	Redacted    bool   `json:"redacted,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"` // sha256(value)[:8], hex
}

// Plain returns the recorded value for key, or "" when the key is absent or
// redacted. Callers that need to act on a value (not merely compare it) must
// use this rather than reading Env directly, so a secret can never leak into a
// code path that prints it.
func (s *DaemonEnvSnapshot) Plain(key string) string {
	if s == nil {
		return ""
	}
	v, ok := s.Env[key]
	if !ok || v.Redacted {
		return ""
	}
	return v.Value
}

// IsSecretEnvKey reports whether a key's value must be fingerprinted instead of
// recorded.
func IsSecretEnvKey(key string) bool {
	return secretEnvKeyPattern.MatchString(key)
}

// FingerprintEnvValue returns the first 8 hex characters of sha256(value). It is
// the only thing ever recorded for a secret key: stable enough to detect
// value-level drift, useless for recovering the secret.
func FingerprintEnvValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

// ResolveDaemonEnvSnapshotPath returns the path to daemon-env.json for the given
// project directory. It mirrors ResolveDaemonStatePath: the file lives beside
// the daemon PID file, so the two live and die together.
func ResolveDaemonEnvSnapshotPath(projectDir string) string {
	config, err := LoadDaemonConfig(projectDir)
	if err != nil {
		return filepath.Join(projectDir, ".loom", SnapshotFileName)
	}
	pidFilePath := resolvePath(projectDir, config.Daemon.PIDFile)
	return filepath.Join(filepath.Dir(pidFilePath), SnapshotFileName)
}

// CaptureDaemonEnvSnapshot builds a snapshot from an environ slice (as returned
// by os.Environ). Only LOOM_/OTEL_ keys are kept; secret-looking keys are
// reduced to a fingerprint.
func CaptureDaemonEnvSnapshot(environ []string, workspace string) DaemonEnvSnapshot {
	env := make(map[string]EnvValue)
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !IsCapturedEnvKey(key) {
			continue
		}
		if IsSecretEnvKey(key) {
			env[key] = EnvValue{Redacted: true, Fingerprint: FingerprintEnvValue(value)}
			continue
		}
		env[key] = EnvValue{Value: value}
	}

	binary, _ := os.Executable()
	cwd, _ := os.Getwd()
	if workspace == "" {
		if v, ok := env["LOOM_WORKSPACE"]; ok {
			workspace = v.Value
		}
	}

	return DaemonEnvSnapshot{
		Version:   daemonEnvSnapshotVersion,
		PID:       os.Getpid(),
		Workspace: workspace,
		Binary:    binary,
		Cwd:       cwd,
		StartedAt: time.Now(),
		Env:       env,
	}
}

// IsCapturedEnvKey reports whether a key falls under one of the prefixes a
// snapshot records. Callers diffing a snapshot against a declared environment
// use it to restrict the declared side to the same prefixes.
func IsCapturedEnvKey(key string) bool {
	for _, prefix := range capturedEnvPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// WriteDaemonEnvSnapshot writes the snapshot atomically with 0600 permissions.
func WriteDaemonEnvSnapshot(path string, s DaemonEnvSnapshot) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal daemon env snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create snapshot dir: %w", err)
	}
	return atomicfile.WriteFile(path, append(data, '\n'), 0o600)
}

// LoadDaemonEnvSnapshot reads a snapshot from disk. A missing file yields an
// error satisfying os.IsNotExist so callers can treat it as "no daemon report".
func LoadDaemonEnvSnapshot(path string) (*DaemonEnvSnapshot, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from the loom project's own .loom/ directory
	if err != nil {
		return nil, err
	}
	var snap DaemonEnvSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if snap.Env == nil {
		snap.Env = make(map[string]EnvValue)
	}
	return &snap, nil
}

// SortedEnvKeys returns the snapshot's keys in sorted order.
func (s *DaemonEnvSnapshot) SortedEnvKeys() []string {
	if s == nil {
		return nil
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DisplayEnvValue renders a value for human output: secrets as a redacted
// fingerprint, everything else verbatim.
func DisplayEnvValue(v EnvValue) string {
	if v.Redacted {
		return fmt.Sprintf("<redacted sha256:%s>", v.Fingerprint)
	}
	return v.Value
}
