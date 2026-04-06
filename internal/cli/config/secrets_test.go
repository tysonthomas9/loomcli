package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- EnvSecretBackend tests ---

func TestEnvSecretBackend_Resolves(t *testing.T) {
	t.Setenv("LOOM_SECRET_MY_KEY", "secret-value")
	b := &EnvSecretBackend{}
	val, found, err := b.Resolve("my-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if val != "secret-value" {
		t.Errorf("got %q, want %q", val, "secret-value")
	}
}

func TestEnvSecretBackend_NotFound(t *testing.T) {
	t.Parallel()
	os.Unsetenv("LOOM_SECRET_MISSING_VAR")
	b := &EnvSecretBackend{}
	_, found, err := b.Resolve("missing-var")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false")
	}
}

func TestEnvSecretBackend_EmptyValue(t *testing.T) {
	t.Setenv("LOOM_SECRET_EMPTY_KEY", "")
	b := &EnvSecretBackend{}
	val, found, err := b.Resolve("empty-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true for explicitly set empty env var")
	}
	if val != "" {
		t.Errorf("got %q, want empty string", val)
	}
}

func TestEnvSecretBackend_NameConversion(t *testing.T) {
	t.Setenv("LOOM_SECRET_DB_HOST_NAME", "localhost")
	b := &EnvSecretBackend{}
	val, found, err := b.Resolve("db-host.name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || val != "localhost" {
		t.Errorf("got found=%v val=%q, want found=true val=%q", found, val, "localhost")
	}
}

func TestToEnvName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"api-key", "API_KEY"},
		{"db.host", "DB_HOST"},
		{"simple", "SIMPLE"},
		{"a-b.c", "A_B_C"},
	}
	for _, tc := range tests {
		got := toEnvName(tc.in)
		if got != tc.want {
			t.Errorf("toEnvName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- FileSecretBackend tests ---

func TestFileSecretBackend_Resolves(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "test-key")
	if err := os.WriteFile(secretPath, []byte("file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}

	b := &FileSecretBackend{dir: dir}
	val, found, err := b.Resolve("test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if val != "file-secret" {
		t.Errorf("got %q, want %q", val, "file-secret")
	}
}

func TestFileSecretBackend_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b := &FileSecretBackend{dir: dir}
	_, found, err := b.Resolve("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false")
	}
}

func TestFileSecretBackend_PathTraversal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b := &FileSecretBackend{dir: dir}

	for _, name := range []string{"../etc/passwd", "foo/bar", "foo\\bar"} {
		_, _, err := b.Resolve(name)
		if err == nil {
			t.Errorf("expected error for path traversal name %q", name)
		}
		if err != nil && !strings.Contains(err.Error(), "path traversal") {
			t.Errorf("error should mention path traversal for %q: %v", name, err)
		}
	}
}

func TestFileSecretBackend_TrimsNewline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "key"), []byte("value\nextra\n"), 0600); err != nil {
		t.Fatal(err)
	}

	b := &FileSecretBackend{dir: dir}
	val, found, err := b.Resolve("key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || val != "value" {
		t.Errorf("got found=%v val=%q, want found=true val=%q", found, val, "value")
	}
}

// --- OnePasswordBackend tests ---

func TestOnePasswordBackend_NotAvailable(t *testing.T) {
	t.Parallel()
	b := &OnePasswordBackend{opAvailable: false}
	_, found, err := b.Resolve("op://vault/item/field")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false when op is not available")
	}
}

func TestOnePasswordBackend_NonOpURI(t *testing.T) {
	t.Parallel()
	b := &OnePasswordBackend{opAvailable: true}
	_, found, err := b.Resolve("plain-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false for non-op:// name")
	}
}

func TestOnePasswordBackend_Success(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)
	deps.ExecCtx = &funcExecContextRunner{fn: func(_ context.Context, dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "op-secret-value\n", Stderr: "", Err: nil}
	}}

	b := &OnePasswordBackend{opAvailable: true, deps: deps}
	val, found, err := b.Resolve("op://vault/item/field")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || val != "op-secret-value" {
		t.Errorf("got found=%v val=%q, want found=true val=%q", found, val, "op-secret-value")
	}
}

func TestOnePasswordBackend_Error(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)
	deps.ExecCtx = &funcExecContextRunner{fn: func(_ context.Context, dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "", Stderr: "not signed in\n", Err: os.ErrPermission}
	}}

	b := &OnePasswordBackend{opAvailable: true, deps: deps}
	_, _, err := b.Resolve("op://vault/item/field")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("error should contain stderr: %v", err)
	}
}

// --- SecretResolver chain tests ---

type mockSecretBackend struct {
	name      string
	secrets   map[string]string
	callCount int
}

func (m *mockSecretBackend) Name() string { return m.name }
func (m *mockSecretBackend) Resolve(name string) (string, bool, error) {
	m.callCount++
	if val, ok := m.secrets[name]; ok {
		return val, true, nil
	}
	return "", false, nil
}

func TestSecretResolver_ChainPriority(t *testing.T) {
	t.Parallel()
	b1 := &mockSecretBackend{name: "first", secrets: map[string]string{"key": "from-first"}}
	b2 := &mockSecretBackend{name: "second", secrets: map[string]string{"key": "from-second"}}

	r := &SecretResolver{
		backends: []SecretBackend{b1, b2},
		cache:    make(map[string]string),
	}

	val, err := r.Resolve("key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "from-first" {
		t.Errorf("got %q, want %q", val, "from-first")
	}
	if b2.callCount != 0 {
		t.Error("second backend should not have been called")
	}
}

func TestSecretResolver_Fallback(t *testing.T) {
	t.Parallel()
	b1 := &mockSecretBackend{name: "first", secrets: map[string]string{}}
	b2 := &mockSecretBackend{name: "second", secrets: map[string]string{"key": "from-second"}}

	r := &SecretResolver{
		backends: []SecretBackend{b1, b2},
		cache:    make(map[string]string),
	}

	val, err := r.Resolve("key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "from-second" {
		t.Errorf("got %q, want %q", val, "from-second")
	}
}

func TestSecretResolver_Caching(t *testing.T) {
	t.Parallel()
	b := &mockSecretBackend{name: "mock", secrets: map[string]string{"key": "cached-val"}}
	r := &SecretResolver{
		backends: []SecretBackend{b},
		cache:    make(map[string]string),
	}

	// First call
	val1, _ := r.Resolve("key")
	// Second call — should hit cache
	val2, _ := r.Resolve("key")

	if val1 != val2 {
		t.Errorf("cache returned different values: %q vs %q", val1, val2)
	}
	if b.callCount != 1 {
		t.Errorf("backend called %d times, want 1 (cache miss only)", b.callCount)
	}
}

func TestSecretResolver_NotFound(t *testing.T) {
	t.Parallel()
	b := &mockSecretBackend{name: "mock", secrets: map[string]string{}}
	r := &SecretResolver{
		backends: []SecretBackend{b},
		cache:    make(map[string]string),
	}

	_, err := r.Resolve("missing")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mention secret name: %v", err)
	}
}

// --- ResolveAllInString tests ---

func TestResolveAllInString_Single(t *testing.T) {
	t.Parallel()
	b := &mockSecretBackend{name: "mock", secrets: map[string]string{"api-key": "sk-123"}}
	r := &SecretResolver{backends: []SecretBackend{b}, cache: make(map[string]string)}

	got, err := r.ResolveAllInString("token=$secret:api-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "token=sk-123" {
		t.Errorf("got %q, want %q", got, "token=sk-123")
	}
}

func TestResolveAllInString_Multiple(t *testing.T) {
	t.Parallel()
	b := &mockSecretBackend{name: "mock", secrets: map[string]string{"a": "1", "b": "2"}}
	r := &SecretResolver{backends: []SecretBackend{b}, cache: make(map[string]string)}

	got, err := r.ResolveAllInString("$secret:a and $secret:b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1 and 2" {
		t.Errorf("got %q, want %q", got, "1 and 2")
	}
}

func TestResolveAllInString_NoReferences(t *testing.T) {
	t.Parallel()
	r := &SecretResolver{backends: nil, cache: make(map[string]string)}

	got, err := r.ResolveAllInString("no secrets here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "no secrets here" {
		t.Errorf("got %q, want %q", got, "no secrets here")
	}
}

func TestResolveAllInString_Error(t *testing.T) {
	t.Parallel()
	b := &mockSecretBackend{name: "mock", secrets: map[string]string{}}
	r := &SecretResolver{backends: []SecretBackend{b}, cache: make(map[string]string)}

	_, err := r.ResolveAllInString("$secret:missing")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- MaskSecrets tests ---

func TestMaskSecrets_Known(t *testing.T) {
	t.Parallel()
	r := &SecretResolver{backends: nil, cache: map[string]string{"key": "supersecret"}}

	got := r.MaskSecrets("the password is supersecret here")
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in output, got %q", got)
	}
	if strings.Contains(got, "supersecret") {
		t.Errorf("expected secret to be masked, got %q", got)
	}
}

func TestMaskSecrets_ShortValueSkipped(t *testing.T) {
	t.Parallel()
	r := &SecretResolver{backends: nil, cache: map[string]string{"key": "ab"}}

	got := r.MaskSecrets("value is ab here")
	if strings.Contains(got, "[REDACTED]") {
		t.Errorf("short values should not be masked, got %q", got)
	}
}

func TestMaskSecrets_LongerFirst(t *testing.T) {
	t.Parallel()
	r := &SecretResolver{backends: nil, cache: map[string]string{
		"short": "secret",
		"long":  "supersecret",
	}}

	got := r.MaskSecrets("supersecret contains secret")
	// "supersecret" should be masked first (longer), then "secret" in the rest
	if strings.Contains(got, "supersecret") {
		t.Errorf("longer secret should be masked first, got %q", got)
	}
}

func TestMaskSecrets_Empty(t *testing.T) {
	t.Parallel()
	r := &SecretResolver{backends: nil, cache: map[string]string{}}
	got := r.MaskSecrets("nothing to mask")
	if got != "nothing to mask" {
		t.Errorf("got %q, want %q", got, "nothing to mask")
	}
}

// --- Snapshot tests ---

func TestSnapshot_ReturnsCopy(t *testing.T) {
	t.Parallel()
	r := &SecretResolver{backends: nil, cache: map[string]string{"a": "1", "b": "2"}}

	snap := r.Snapshot()
	if len(snap) != 2 || snap["a"] != "1" || snap["b"] != "2" {
		t.Errorf("snapshot mismatch: %v", snap)
	}

	// Modifying snapshot should not affect original
	snap["a"] = "modified"
	if r.cache["a"] != "1" {
		t.Error("snapshot modification affected original cache")
	}
}

// --- ResolveSecretsInBytes tests ---

func TestResolveSecretsInBytes_FullYAML(t *testing.T) {
	t.Parallel()
	b := &mockSecretBackend{name: "mock", secrets: map[string]string{"db-pass": "s3cret"}}
	r := &SecretResolver{backends: []SecretBackend{b}, cache: make(map[string]string)}

	input := []byte("database:\n  password: $secret:db-pass\n  host: localhost\n")
	out, err := ResolveSecretsInBytes(input, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "s3cret") {
		t.Errorf("expected resolved secret in output, got:\n%s", out)
	}
	if strings.Contains(string(out), "$secret:") {
		t.Errorf("unresolved $secret: reference in output:\n%s", out)
	}
}

func TestResolveSecretsInBytes_NoReferences(t *testing.T) {
	t.Parallel()
	r := &SecretResolver{backends: nil, cache: make(map[string]string)}

	input := []byte("key: value\n")
	out, err := ResolveSecretsInBytes(input, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fast path: should return input unchanged
	if string(out) != string(input) {
		t.Errorf("expected passthrough, got:\n%s", out)
	}
}

func TestResolveSecretsInBytes_Empty(t *testing.T) {
	t.Parallel()
	r := &SecretResolver{backends: nil, cache: make(map[string]string)}

	out, err := ResolveSecretsInBytes(nil, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil, got %v", out)
	}
}

func TestResolveSecretsInBytes_ErrorIncludesLocation(t *testing.T) {
	t.Parallel()
	b := &mockSecretBackend{name: "mock", secrets: map[string]string{}}
	r := &SecretResolver{backends: []SecretBackend{b}, cache: make(map[string]string)}

	input := []byte("key: $secret:missing\n")
	_, err := ResolveSecretsInBytes(input, r)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "line") {
		t.Errorf("error should include line info: %v", err)
	}
}

func TestResolveSecretsInBytes_NestedMaps(t *testing.T) {
	t.Parallel()
	b := &mockSecretBackend{name: "mock", secrets: map[string]string{"key1": "val1", "key2": "val2"}}
	r := &SecretResolver{backends: []SecretBackend{b}, cache: make(map[string]string)}

	input := []byte("outer:\n  inner:\n    a: $secret:key1\n    b: $secret:key2\n  num: 42\n")
	out, err := ResolveSecretsInBytes(input, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "val1") || !strings.Contains(s, "val2") {
		t.Errorf("expected resolved secrets in output:\n%s", s)
	}
	if strings.Contains(s, "$secret:") {
		t.Errorf("unresolved references in output:\n%s", s)
	}
}

// --- Integration: config loading with secrets ---

func TestLoadConfig_SecretResolution(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	t.Setenv("LOOM_SECRET_TEST_BACKEND", "claude-code")

	configContent := `backend: $secret:test-backend
workspaces: {}
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Backend != "claude-code" {
		t.Errorf("got backend %q, want %q", cfg.Backend, "claude-code")
	}
}
