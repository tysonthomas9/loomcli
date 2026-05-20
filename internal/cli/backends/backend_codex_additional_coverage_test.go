package backends

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexEnvAndAuthAdditionalBranches(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	exeDir := filepath.Dir(exe)

	env := appendLoomExecutableDirToPath([]string{"A=1"})
	if len(env) == 0 || env[0] != "PATH="+exeDir {
		t.Fatalf("env without PATH = %v, want PATH prefixed with executable dir", env)
	}
	env = appendLoomExecutableDirToPath([]string{"PATH="})
	if len(env) != 1 || env[0] != "PATH="+exeDir {
		t.Fatalf("empty PATH env = %v, want executable dir", env)
	}
	env = appendLoomExecutableDirToPath([]string{"PATH=" + exeDir})
	if len(env) != 1 || env[0] != "PATH="+exeDir {
		t.Fatalf("existing PATH env = %v, want unchanged executable dir", env)
	}
	if !pathContainsDir(strings.Join([]string{"/tmp", exeDir}, string(os.PathListSeparator)), exeDir) {
		t.Fatalf("pathContainsDir did not find executable dir")
	}
	if pathContainsDir("/tmp", exeDir) {
		t.Fatalf("pathContainsDir found unexpected dir")
	}

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if got := codexAuthFilePath(); got != filepath.Join(home, "auth.json") {
		t.Fatalf("codexAuthFilePath = %q", got)
	}
	if hasCodexAuthFile() {
		t.Fatal("missing auth file reported as present")
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), nil, 0600); err != nil {
		t.Fatalf("write empty auth: %v", err)
	}
	if hasCodexAuthFile() {
		t.Fatal("empty auth file reported as present")
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"token":"ok"}`), 0600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if !hasCodexAuthFile() {
		t.Fatal("non-empty auth file was not detected")
	}
}

func TestCodexHealthCheckWithFakeBinaryAndAuthFile(t *testing.T) {
	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\necho codex 1.2.3\n"), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"token":"ok"}`), 0600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("CODEX_HOME", home)
	t.Setenv("OPENAI_API_KEY", "")

	hs := (&CodexBackend{}).HealthCheck()
	if !hs.Healthy || !hs.Installed || !hs.APIKeySet {
		t.Fatalf("health = %+v, want healthy installed with API key via auth file", hs)
	}
	if !strings.Contains(hs.Version, "1.2.3") {
		t.Fatalf("version = %q, want fake codex version", hs.Version)
	}
}
