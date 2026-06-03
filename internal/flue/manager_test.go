package flue

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCheckNodeVersion(t *testing.T) {
	cases := []struct {
		ver string
		ok  bool
	}{
		{"v22.18.0", true},
		{"v22.18.5", true},
		{"v22.19.0", true},
		{"v24.13.1", true},
		{"v23.0.0", true},
		{"v22.17.9", false},
		{"v22.0.0", false},
		{"v20.11.0", false},
		{"v18.0.0", false},
		{"22.18.0", true}, // no leading v
		{"garbage", false},
		{"v22", false},
	}
	for _, c := range cases {
		err := checkNodeVersion(c.ver)
		if c.ok && err != nil {
			t.Errorf("checkNodeVersion(%q) = %v, want nil", c.ver, err)
		}
		if !c.ok && err == nil {
			t.Errorf("checkNodeVersion(%q) = nil, want error", c.ver)
		}
	}
}

func TestParseNodeVersion(t *testing.T) {
	major, minor, err := parseNodeVersion("v24.13.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if major != 24 || minor != 13 {
		t.Fatalf("parseNodeVersion = (%d,%d), want (24,13)", major, minor)
	}
	if _, _, err := parseNodeVersion("v7"); err == nil {
		t.Error("expected error for malformed version")
	}
}

func TestComputeTemplateHashStableAndNonEmpty(t *testing.T) {
	h1, err := computeTemplateHash()
	if err != nil {
		t.Fatalf("computeTemplateHash: %v", err)
	}
	if h1 == "" {
		t.Fatal("template hash is empty")
	}
	h2, err := computeTemplateHash()
	if err != nil {
		t.Fatalf("computeTemplateHash (2nd): %v", err)
	}
	if h1 != h2 {
		t.Fatalf("template hash not stable: %q vs %q", h1, h2)
	}
}

func TestScaffoldTemplateWritesProjectFiles(t *testing.T) {
	dst := t.TempDir()
	if err := scaffoldTemplate(dst); err != nil {
		t.Fatalf("scaffoldTemplate: %v", err)
	}
	want := []string{
		"package.json",
		"package-lock.json",
		"flue.config.ts",
		filepath.Join(".flue", "app.ts"),
		filepath.Join(".flue", "workflows", "agent.ts"),
		filepath.Join(".flue", "agents", "lead.ts"),
		filepath.Join(".flue", "connectors", "daytona.ts"),
	}
	for _, rel := range want {
		if !fileExists(filepath.Join(dst, rel)) {
			t.Errorf("expected scaffolded file %s to exist", rel)
		}
	}
}

func TestRuntimeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := readRuntime(dir); err == nil {
		t.Error("expected error reading missing runtime")
	}
	in := projectRuntime{
		TemplateVersion: "abc123",
		NodeVersion:     "v24.13.1",
		PkgManager:      "npm",
		InstalledAt:     time.Now().UTC().Truncate(time.Second),
		BuiltAt:         time.Now().UTC().Truncate(time.Second),
	}
	if err := writeRuntime(dir, in); err != nil {
		t.Fatalf("writeRuntime: %v", err)
	}
	out, err := readRuntime(dir)
	if err != nil {
		t.Fatalf("readRuntime: %v", err)
	}
	if out.TemplateVersion != in.TemplateVersion || out.NodeVersion != in.NodeVersion || out.PkgManager != in.PkgManager {
		t.Fatalf("runtime round-trip mismatch: got %+v want %+v", out, in)
	}
}

func TestDetectPkgManagerEnvOverride(t *testing.T) {
	t.Setenv(EnvPkgManager, "npm")
	if got := detectPkgManager(); got != "npm" {
		t.Fatalf("detectPkgManager with override = %q, want npm", got)
	}
	t.Setenv(EnvPkgManager, "pnpm")
	if got := detectPkgManager(); got != "pnpm" {
		t.Fatalf("detectPkgManager with override = %q, want pnpm", got)
	}
}

func TestDefaultManagerRespectsProjectDirOverride(t *testing.T) {
	// DefaultManager memoizes via sync.Once, so exercise the resolution
	// logic directly rather than relying on call order across tests.
	custom := t.TempDir()
	m := &Manager{projectDir: custom, managed: false}
	if m.ProjectDir() != custom {
		t.Fatalf("ProjectDir() = %q, want %q", m.ProjectDir(), custom)
	}
}
