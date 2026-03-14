package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandEnvVars_SetVariable(t *testing.T) {
	t.Setenv("TEST_EXPAND_VAR", "hello")
	got, err := ExpandEnvVars("${TEST_EXPAND_VAR}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestExpandEnvVars_UnsetVariable(t *testing.T) {
	os.Unsetenv("TEST_EXPAND_MISSING")
	_, err := ExpandEnvVars("${TEST_EXPAND_MISSING}")
	if err == nil {
		t.Fatal("expected error for unset variable")
	}
	if !strings.Contains(err.Error(), "TEST_EXPAND_MISSING") {
		t.Errorf("error should mention variable name: %v", err)
	}
}

func TestExpandEnvVars_DefaultWithSet(t *testing.T) {
	t.Setenv("TEST_EXPAND_DEF", "actual")
	got, err := ExpandEnvVars("${TEST_EXPAND_DEF:-fallback}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "actual" {
		t.Errorf("got %q, want %q", got, "actual")
	}
}

func TestExpandEnvVars_DefaultWithUnset(t *testing.T) {
	os.Unsetenv("TEST_EXPAND_DEF2")
	got, err := ExpandEnvVars("${TEST_EXPAND_DEF2:-fallback}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

func TestExpandEnvVars_EmptyDefault(t *testing.T) {
	os.Unsetenv("TEST_EXPAND_EMPTYDEF")
	got, err := ExpandEnvVars("${TEST_EXPAND_EMPTYDEF:-}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestExpandEnvVars_ErrorMessageWithSet(t *testing.T) {
	t.Setenv("TEST_EXPAND_ERR", "value")
	got, err := ExpandEnvVars("${TEST_EXPAND_ERR:?must be set}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "value" {
		t.Errorf("got %q, want %q", got, "value")
	}
}

func TestExpandEnvVars_ErrorMessageWithUnset(t *testing.T) {
	os.Unsetenv("TEST_EXPAND_ERR2")
	_, err := ExpandEnvVars("${TEST_EXPAND_ERR2:?must be set}")
	if err == nil {
		t.Fatal("expected error for unset variable with :?")
	}
	if !strings.Contains(err.Error(), "must be set") {
		t.Errorf("error should contain custom message: %v", err)
	}
}

func TestExpandEnvVars_EmptySetPlain(t *testing.T) {
	t.Setenv("TEST_EMPTY_PLAIN", "")
	got, err := ExpandEnvVars("${TEST_EMPTY_PLAIN}")
	if err != nil {
		t.Fatalf("unexpected error for empty-but-set variable: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestExpandEnvVars_EmptySetDefault(t *testing.T) {
	t.Setenv("TEST_EMPTY_DEF", "")
	got, err := ExpandEnvVars("${TEST_EMPTY_DEF:-fallback}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

func TestExpandEnvVars_EmptySetError(t *testing.T) {
	t.Setenv("TEST_EMPTY_ERR", "")
	_, err := ExpandEnvVars("${TEST_EMPTY_ERR:?must be set}")
	if err == nil {
		t.Fatal("expected error for empty-but-set variable with :?")
	}
	if !strings.Contains(err.Error(), "must be set") {
		t.Errorf("error should contain custom message: %v", err)
	}
}

func TestExpandEnvVars_MultipleExpansions(t *testing.T) {
	t.Setenv("TEST_A", "foo")
	t.Setenv("TEST_B", "bar")
	got, err := ExpandEnvVars("${TEST_A}-${TEST_B}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "foo-bar" {
		t.Errorf("got %q, want %q", got, "foo-bar")
	}
}

func TestExpandEnvVars_MixedLiteralAndExpansion(t *testing.T) {
	t.Setenv("TEST_MIX", "world")
	got, err := ExpandEnvVars("hello-${TEST_MIX}-suffix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello-world-suffix" {
		t.Errorf("got %q, want %q", got, "hello-world-suffix")
	}
}

func TestExpandEnvVars_EscapedDollar(t *testing.T) {
	got, err := ExpandEnvVars("price is $$5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "price is $5" {
		t.Errorf("got %q, want %q", got, "price is $5")
	}
}

func TestExpandEnvVars_NoExpansion(t *testing.T) {
	got, err := ExpandEnvVars("no vars here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "no vars here" {
		t.Errorf("got %q, want %q", got, "no vars here")
	}
}

func TestExpandEnvVars_EmptyInput(t *testing.T) {
	got, err := ExpandEnvVars("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestExpandEnvVars_UnclosedBrace(t *testing.T) {
	_, err := ExpandEnvVars("${UNCLOSED")
	if err == nil {
		t.Fatal("expected error for unclosed ${")
	}
	if !strings.Contains(err.Error(), "unclosed") {
		t.Errorf("error should mention unclosed: %v", err)
	}
}

func TestExpandEnvVars_EmptyVarName(t *testing.T) {
	_, err := ExpandEnvVars("${}")
	if err == nil {
		t.Fatal("expected error for empty variable name")
	}
}

func TestExpandEnvVars_NestedRef(t *testing.T) {
	_, err := ExpandEnvVars("${${INNER}}")
	if err == nil {
		t.Fatal("expected error for nested variable reference")
	}
	if !strings.Contains(err.Error(), "nested") {
		t.Errorf("error should mention nested: %v", err)
	}
}

func TestExpandEnvVars_DefaultWithColon(t *testing.T) {
	os.Unsetenv("TEST_COLON_DEF")
	got, err := ExpandEnvVars("${TEST_COLON_DEF:-host:5432}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "host:5432" {
		t.Errorf("got %q, want %q", got, "host:5432")
	}
}

func TestExpandEnvVars_BareDollar(t *testing.T) {
	got, err := ExpandEnvVars("cost $5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cost $5" {
		t.Errorf("got %q, want %q", got, "cost $5")
	}
}

func TestExpandConfigBytes_FullYAML(t *testing.T) {
	t.Setenv("TEST_REDIS", "redis://localhost:6379")
	t.Setenv("TEST_KEY", "secret123")

	input := []byte(`backend: claude
daemon:
  redis_url: ${TEST_REDIS}
  api_key: ${TEST_KEY}
  max_agents: 5
  log_dir: /var/log
`)

	got, err := ExpandConfigBytes(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := string(got)
	if !strings.Contains(s, "redis://localhost:6379") {
		t.Errorf("expected expanded redis URL in output:\n%s", s)
	}
	if !strings.Contains(s, "secret123") {
		t.Errorf("expected expanded api key in output:\n%s", s)
	}
	// Verify non-string values preserved
	if !strings.Contains(s, "5") {
		t.Errorf("expected numeric value preserved in output:\n%s", s)
	}
}

func TestExpandConfigBytes_Empty(t *testing.T) {
	got, err := ExpandConfigBytes([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty output, got %q", got)
	}
}

func TestExpandConfigBytes_NoVars(t *testing.T) {
	input := []byte("key: value\nnum: 42\n")
	got, err := ExpandConfigBytes(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(got), "value") {
		t.Errorf("expected value preserved in output:\n%s", got)
	}
}

func TestExpandConfigBytes_ErrorIncludesLine(t *testing.T) {
	os.Unsetenv("TEST_MISSING_CFG")
	input := []byte("ok: true\nbad: ${TEST_MISSING_CFG}\n")
	_, err := ExpandConfigBytes(input)
	if err == nil {
		t.Fatal("expected error for missing variable")
	}
	if !strings.Contains(err.Error(), "line") {
		t.Errorf("error should include line number: %v", err)
	}
}

func TestExpandConfigBytes_NestedStructures(t *testing.T) {
	t.Setenv("TEST_NESTED_VAL", "expanded")
	input := []byte(`top:
  nested:
    - ${TEST_NESTED_VAL}
    - literal
  map:
    key: ${TEST_NESTED_VAL}
`)
	got, err := ExpandConfigBytes(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(got)
	if strings.Count(s, "expanded") != 2 {
		t.Errorf("expected 'expanded' twice in output:\n%s", s)
	}
}

func TestLoadConfig_EnvVarExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)
	t.Setenv("TEST_BACKEND_VAL", "openai")

	configContent := `backend: ${TEST_BACKEND_VAL}
workspaces: {}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Backend != "openai" {
		t.Errorf("Backend = %q, want %q", cfg.Backend, "openai")
	}
}

func TestLoadProjectFile_EnvVarExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TEST_PF_BACKEND", "anthropic")

	content := `backend: ${TEST_PF_BACKEND}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "loom.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	pf, err := LoadProjectFile(tmpDir)
	if err != nil {
		t.Fatalf("LoadProjectFile() error = %v", err)
	}
	if pf.Backend != "anthropic" {
		t.Errorf("Backend = %q, want %q", pf.Backend, "anthropic")
	}
}

func TestDaemonSettings_NewFields_Overlay(t *testing.T) {
	dst := &DaemonSettings{}
	src := &DaemonSettings{
		RedisURL: "redis://host:6379",
		APIKey:   "key123",
		JWTKey:   "jwt456",
	}
	overlayDaemonSettings(dst, src)
	if dst.RedisURL != "redis://host:6379" {
		t.Errorf("RedisURL = %q, want %q", dst.RedisURL, "redis://host:6379")
	}
	if dst.APIKey != "key123" {
		t.Errorf("APIKey = %q, want %q", dst.APIKey, "key123")
	}
	if dst.JWTKey != "jwt456" {
		t.Errorf("JWTKey = %q, want %q", dst.JWTKey, "jwt456")
	}
}

func TestDaemonSettings_NewFields_OverlayEmpty(t *testing.T) {
	dst := &DaemonSettings{
		RedisURL: "original",
		APIKey:   "original",
		JWTKey:   "original",
	}
	src := &DaemonSettings{} // empty should not override
	overlayDaemonSettings(dst, src)
	if dst.RedisURL != "original" {
		t.Errorf("RedisURL = %q, want %q", dst.RedisURL, "original")
	}
	if dst.APIKey != "original" {
		t.Errorf("APIKey = %q, want %q", dst.APIKey, "original")
	}
	if dst.JWTKey != "original" {
		t.Errorf("JWTKey = %q, want %q", dst.JWTKey, "original")
	}
}
