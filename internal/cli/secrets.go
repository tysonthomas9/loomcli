package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// SecretBackend resolves a named secret from a specific source.
type SecretBackend interface {
	Name() string
	Resolve(name string) (value string, found bool, err error)
}

// EnvSecretBackend resolves secrets from LOOM_SECRET_<UPPER_NAME> env vars.
type EnvSecretBackend struct{}

func (b *EnvSecretBackend) Name() string { return "env" }

func (b *EnvSecretBackend) Resolve(name string) (string, bool, error) {
	envName := "LOOM_SECRET_" + toEnvName(name)
	val, ok := os.LookupEnv(envName)
	if !ok {
		return "", false, nil
	}
	return val, true, nil
}

// toEnvName converts a secret name to an env var suffix:
// uppercase, hyphens and dots become underscores.
func toEnvName(name string) string {
	s := strings.ToUpper(name)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	return s
}

// FileSecretBackend resolves secrets from ~/.loom/secrets/<name> files.
type FileSecretBackend struct {
	dir string // e.g. ~/.loom/secrets
}

func NewFileSecretBackend() *FileSecretBackend {
	return &FileSecretBackend{dir: filepath.Join(GetConfigDir(), "secrets")}
}

func (b *FileSecretBackend) Name() string { return "file" }

func (b *FileSecretBackend) Resolve(name string) (string, bool, error) {
	// Skip names that are op:// URIs — they're handled by OnePasswordBackend
	if strings.HasPrefix(name, "op://") {
		return "", false, nil
	}
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", false, fmt.Errorf("secret name %q contains path traversal characters", name)
	}

	path := filepath.Join(b.dir, name)

	// Verify resolved path stays within the secrets directory (symlink protection)
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		absDir := b.dir
		if evalDir, evalErr := filepath.EvalSymlinks(b.dir); evalErr == nil {
			absDir = evalDir
		}
		if !strings.HasPrefix(resolved, absDir+string(filepath.Separator)) && resolved != absDir {
			return "", false, fmt.Errorf("secret name %q resolves outside secrets directory", name)
		}
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304 — path is validated above: no "..", "/", or "\" in name; symlinks checked
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading secret file %s: %w", path, err)
	}

	// Check permissions — warn but still read (advisory, matches ssh behavior)
	if info, statErr := os.Stat(path); statErr == nil {
		mode := info.Mode().Perm()
		if mode != 0600 {
			fmt.Fprintf(os.Stderr, "Warning: secret file %s has mode %04o, expected 0600\n", path, mode)
		}
	}

	// Read first line only, trim trailing newline
	val := string(data)
	if idx := strings.IndexByte(val, '\n'); idx >= 0 {
		val = val[:idx]
	}

	return val, true, nil
}

// redactOpURI masks the field component of an op:// URI for safe error reporting.
// It returns "op://vault/item/***" if the URI has at least vault/item/field parts,
// or "op://***" if parsing fails.
func redactOpURI(uri string) string {
	trimmed := strings.TrimPrefix(uri, "op://")
	if trimmed == uri {
		// Not an op:// URI at all
		return "op://***"
	}
	parts := strings.SplitN(trimmed, "/", 3)
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" {
		return "op://***"
	}
	return "op://" + parts[0] + "/" + parts[1] + "/***"
}

// OnePasswordBackend resolves op:// URIs via the 1Password CLI.
type OnePasswordBackend struct {
	opAvailable bool
}

func NewOnePasswordBackend() *OnePasswordBackend {
	_, err := exec.LookPath("op")
	return &OnePasswordBackend{opAvailable: err == nil}
}

func (b *OnePasswordBackend) Name() string { return "1password" }

func (b *OnePasswordBackend) Resolve(name string) (string, bool, error) {
	if !b.opAvailable {
		return "", false, nil
	}
	if !strings.HasPrefix(name, "op://") {
		return "", false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := execCommandContext(ctx, "", "op", "read", name)
	if result.Err != nil {
		return "", false, fmt.Errorf("op read %s: %s", redactOpURI(name), strings.TrimSpace(result.Stderr))
	}
	return strings.TrimSpace(result.Stdout), true, nil
}

// execCommandContext executes a command with context support (swappable for tests).
var execCommandContext = defaultExecCommandContext

func defaultExecCommandContext(ctx context.Context, dir, name string, args ...string) CommandResult {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204 — caller controls command name
	cmd.Dir = dir

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Err:    err,
	}
}

// secretNamePattern matches $secret:<name> references.
var secretNamePattern = regexp.MustCompile(`\$secret:([a-zA-Z0-9_./-]+)`)

// SecretResolver chains backends and caches resolved secrets.
type SecretResolver struct {
	backends []SecretBackend
	cache    map[string]string
	mu       sync.Mutex
}

// NewSecretResolver creates a resolver with the default backend chain.
func NewSecretResolver() *SecretResolver {
	return &SecretResolver{
		backends: []SecretBackend{
			&EnvSecretBackend{},
			NewFileSecretBackend(),
			NewOnePasswordBackend(),
		},
		cache: make(map[string]string),
	}
}

// Resolve resolves a single secret by name, checking cache first.
func (r *SecretResolver) Resolve(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if val, ok := r.cache[name]; ok {
		return val, nil
	}

	for _, b := range r.backends {
		val, found, err := b.Resolve(name)
		if err != nil {
			return "", fmt.Errorf("backend %s: %w", b.Name(), err)
		}
		if found {
			r.cache[name] = val
			return val, nil
		}
	}

	tried := make([]string, len(r.backends))
	for i, b := range r.backends {
		tried[i] = b.Name()
	}
	return "", fmt.Errorf("secret %q not found (tried: %s)", name, strings.Join(tried, ", "))
}

// ResolveAllInString replaces all $secret:name references in s.
func (r *SecretResolver) ResolveAllInString(s string) (string, error) {
	var lastErr error
	result := secretNamePattern.ReplaceAllStringFunc(s, func(match string) string {
		if lastErr != nil {
			return match
		}
		name := secretNamePattern.FindStringSubmatch(match)[1]
		val, err := r.Resolve(name)
		if err != nil {
			lastErr = err
			return match
		}
		return val
	})
	if lastErr != nil {
		return "", lastErr
	}
	return result, nil
}

// MaskSecrets replaces known secret values in s with [REDACTED].
// Skips values shorter than 3 chars to avoid false positives.
// Sorts replacements by length descending to avoid partial masking.
func (r *SecretResolver) MaskSecrets(s string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.cache) == 0 {
		return s
	}

	// Collect values to mask, sorted by length descending
	values := make([]string, 0, len(r.cache))
	for _, v := range r.cache {
		if len(v) >= 3 {
			values = append(values, v)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		return len(values[i]) > len(values[j])
	})

	for _, v := range values {
		s = strings.ReplaceAll(s, v, "[REDACTED]")
	}
	return s
}

// Snapshot returns a copy of the resolved secrets cache.
func (r *SecretResolver) Snapshot() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	snap := make(map[string]string, len(r.cache))
	for k, v := range r.cache {
		snap[k] = v
	}
	return snap
}
