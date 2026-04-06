package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseCfg returns a minimal serviceConfig for testing.
func baseCfg() serviceConfig {
	return serviceConfig{
		Name:             "loom-serve",
		Description:      "Loom agent management server",
		BinaryPath:       "/usr/local/bin/loom",
		WorkingDirectory: "/home/user/project",
		Port:             8080,
		BindAddr:         "127.0.0.1",
		NoWebUI:          false,
		LogDir:           "/home/user/project/.loom/logs",
		ExtraEnv:         nil,
	}
}

// --- systemd template tests ---

func TestRenderSystemdTemplate(t *testing.T) {
	cfg := baseCfg()
	out, err := renderTemplate(systemdTemplate, cfg)
	if err != nil {
		t.Fatalf("renderTemplate(systemd) error: %v", err)
	}

	checks := []struct {
		label    string
		contains string
	}{
		{"ExecStart binary", "ExecStart=/usr/local/bin/loom serve"},
		{"port flag", "--port 8080"},
		{"bind flag", "--bind 127.0.0.1"},
		{"WorkingDirectory", "WorkingDirectory=/home/user/project"},
		{"Description", "Description=Loom agent management server"},
		{"SyslogIdentifier", "SyslogIdentifier=loom-serve"},
		{"Restart", "Restart=on-failure"},
		{"WantedBy", "WantedBy=default.target"},
	}
	for _, c := range checks {
		if !strings.Contains(out, c.contains) {
			t.Errorf("%s: output missing %q\n---output---\n%s", c.label, c.contains, out)
		}
	}

	// --no-webui should NOT appear
	if strings.Contains(out, "--no-webui") {
		t.Errorf("--no-webui should not appear when NoWebUI=false")
	}
}

func TestRenderSystemdTemplate_NoWebUI(t *testing.T) {
	cfg := baseCfg()
	cfg.NoWebUI = true

	out, err := renderTemplate(systemdTemplate, cfg)
	if err != nil {
		t.Fatalf("renderTemplate(systemd) error: %v", err)
	}

	if !strings.Contains(out, "--no-webui") {
		t.Errorf("expected --no-webui in ExecStart line, got:\n%s", out)
	}
}

func TestRenderSystemdTemplate_ExtraEnv(t *testing.T) {
	cfg := baseCfg()
	cfg.ExtraEnv = []string{"FOO=bar", "SECRET=hunter2"}

	out, err := renderTemplate(systemdTemplate, cfg)
	if err != nil {
		t.Fatalf("renderTemplate(systemd) error: %v", err)
	}

	if !strings.Contains(out, `Environment="FOO=bar"`) {
		t.Errorf("missing Environment FOO=bar in output:\n%s", out)
	}
	if !strings.Contains(out, `Environment="SECRET=hunter2"`) {
		t.Errorf("missing Environment SECRET=hunter2 in output:\n%s", out)
	}
}

func TestRenderSystemdTemplate_NoExtraEnv(t *testing.T) {
	cfg := baseCfg()
	cfg.ExtraEnv = nil

	out, err := renderTemplate(systemdTemplate, cfg)
	if err != nil {
		t.Fatalf("renderTemplate(systemd) error: %v", err)
	}

	if strings.Contains(out, "Environment=") {
		t.Errorf("unexpected Environment= line when ExtraEnv is empty:\n%s", out)
	}
}

// --- launchd template tests ---

func TestRenderLaunchdTemplate(t *testing.T) {
	cfg := baseCfg()
	cfg.Name = "com.loom.serve"

	out, err := renderTemplate(launchdTemplate, cfg)
	if err != nil {
		t.Fatalf("renderTemplate(launchd) error: %v", err)
	}

	checks := []struct {
		label    string
		contains string
	}{
		{"XML header", `<?xml version="1.0"`},
		{"plist DOCTYPE", "<!DOCTYPE plist"},
		{"Label", "<string>com.loom.serve</string>"},
		{"BinaryPath in ProgramArguments", "<string>/usr/local/bin/loom</string>"},
		{"serve argument", "<string>serve</string>"},
		{"port value", "<string>8080</string>"},
		{"bind value", "<string>127.0.0.1</string>"},
		{"WorkingDirectory", "<string>/home/user/project</string>"},
		{"RunAtLoad", "<true/>"},
		{"stdout log path", "<string>/home/user/project/.loom/logs/loom-serve.stdout.log</string>"},
		{"stderr log path", "<string>/home/user/project/.loom/logs/loom-serve.stderr.log</string>"},
	}
	for _, c := range checks {
		if !strings.Contains(out, c.contains) {
			t.Errorf("%s: output missing %q\n---output---\n%s", c.label, c.contains, out)
		}
	}

	// --no-webui should NOT appear
	if strings.Contains(out, "--no-webui") {
		t.Errorf("--no-webui should not appear when NoWebUI=false")
	}
}

func TestRenderLaunchdTemplate_NoWebUI(t *testing.T) {
	cfg := baseCfg()
	cfg.Name = "com.loom.serve"
	cfg.NoWebUI = true

	out, err := renderTemplate(launchdTemplate, cfg)
	if err != nil {
		t.Fatalf("renderTemplate(launchd) error: %v", err)
	}

	if !strings.Contains(out, "<string>--no-webui</string>") {
		t.Errorf("expected <string>--no-webui</string> in ProgramArguments, got:\n%s", out)
	}
}

func TestRenderLaunchdTemplate_ExtraEnv(t *testing.T) {
	cfg := baseCfg()
	cfg.Name = "com.loom.serve"
	cfg.ExtraEnv = []string{"API_KEY=abc123", "DEBUG=true"}

	out, err := renderTemplate(launchdTemplate, cfg)
	if err != nil {
		t.Fatalf("renderTemplate(launchd) error: %v", err)
	}

	// Should contain EnvironmentVariables dict with the keys and values
	if !strings.Contains(out, "<key>EnvironmentVariables</key>") {
		t.Errorf("missing EnvironmentVariables dict:\n%s", out)
	}
	if !strings.Contains(out, "<key>API_KEY</key>") {
		t.Errorf("missing key API_KEY:\n%s", out)
	}
	if !strings.Contains(out, "<string>abc123</string>") {
		t.Errorf("missing value abc123:\n%s", out)
	}
	if !strings.Contains(out, "<key>DEBUG</key>") {
		t.Errorf("missing key DEBUG:\n%s", out)
	}
	if !strings.Contains(out, "<string>true</string>") {
		t.Errorf("missing value true:\n%s", out)
	}
}

// --- Flag validation tests ---

func TestFlagValidation_MutuallyExclusive(t *testing.T) {
	// Save and restore global state
	origInstall := installServiceInstall
	origUninstall := installServiceUninstall
	origEnv := installServiceEnv
	t.Cleanup(func() {
		installServiceInstall = origInstall
		installServiceUninstall = origUninstall
		installServiceEnv = origEnv
	})

	installServiceInstall = true
	installServiceUninstall = true
	installServiceEnv = nil

	err := runInstallService(nil, nil)
	if err == nil {
		t.Fatal("expected error for mutually exclusive --install and --uninstall, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should mention 'mutually exclusive', got: %v", err)
	}
}

func TestFlagValidation_EnvFormat(t *testing.T) {
	// Save and restore global state
	origInstall := installServiceInstall
	origUninstall := installServiceUninstall
	origEnv := installServiceEnv
	t.Cleanup(func() {
		installServiceInstall = origInstall
		installServiceUninstall = origUninstall
		installServiceEnv = origEnv
	})

	installServiceInstall = false
	installServiceUninstall = false

	tests := []struct {
		name    string
		env     []string
		wantErr bool
	}{
		{"valid KEY=VALUE", []string{"FOO=bar"}, false},
		{"valid multiple", []string{"A=1", "B=2"}, false},
		{"invalid no equals", []string{"BADFORMAT"}, true},
		{"mixed valid and invalid", []string{"GOOD=yes", "BAD"}, true},
		{"empty value is valid", []string{"KEY="}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installServiceEnv = tt.env
			err := runInstallService(nil, nil)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for env=%v, got nil", tt.env)
				} else if !strings.Contains(err.Error(), "KEY=VALUE") {
					t.Errorf("error should mention KEY=VALUE format, got: %v", err)
				}
			}
			// For valid env cases, the function will proceed past validation
			// and may return other errors (e.g., binary path issues), which is fine.
			// We only check that it doesn't return an env format error.
			if !tt.wantErr && err != nil && strings.Contains(err.Error(), "KEY=VALUE") {
				t.Errorf("unexpected env format error for env=%v: %v", tt.env, err)
			}
		})
	}
}

// --- Service name tests ---

func TestServiceName_Default(t *testing.T) {
	origName := installServiceName
	t.Cleanup(func() { installServiceName = origName })

	installServiceName = ""

	if got := buildServiceName("linux"); got != "loom-serve" {
		t.Errorf("buildServiceName(linux) = %q, want %q", got, "loom-serve")
	}
	if got := buildServiceName("darwin"); got != "com.loom.serve" {
		t.Errorf("buildServiceName(darwin) = %q, want %q", got, "com.loom.serve")
	}
}

func TestServiceName_Custom(t *testing.T) {
	origName := installServiceName
	t.Cleanup(func() { installServiceName = origName })

	installServiceName = "foo"

	if got := buildServiceName("linux"); got != "loom-serve-foo" {
		t.Errorf("buildServiceName(linux) = %q, want %q", got, "loom-serve-foo")
	}
	if got := buildServiceName("darwin"); got != "com.loom.serve.foo" {
		t.Errorf("buildServiceName(darwin) = %q, want %q", got, "com.loom.serve.foo")
	}
}

// --- Path tests ---

func TestSystemdUnitPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot get home dir: %v", err)
	}

	got := systemdUnitPath("loom-serve")
	want := filepath.Join(home, ".config", "systemd", "user", "loom-serve.service")
	if got != want {
		t.Errorf("systemdUnitPath(loom-serve) = %q, want %q", got, want)
	}

	// With custom name
	got = systemdUnitPath("loom-serve-myproject")
	want = filepath.Join(home, ".config", "systemd", "user", "loom-serve-myproject.service")
	if got != want {
		t.Errorf("systemdUnitPath(loom-serve-myproject) = %q, want %q", got, want)
	}
}

func TestLaunchdPlistPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot get home dir: %v", err)
	}

	got := launchdPlistPath("com.loom.serve")
	want := filepath.Join(home, "Library", "LaunchAgents", "com.loom.serve.plist")
	if got != want {
		t.Errorf("launchdPlistPath(com.loom.serve) = %q, want %q", got, want)
	}

	// With custom name
	got = launchdPlistPath("com.loom.serve.myproject")
	want = filepath.Join(home, "Library", "LaunchAgents", "com.loom.serve.myproject.plist")
	if got != want {
		t.Errorf("launchdPlistPath(com.loom.serve.myproject) = %q, want %q", got, want)
	}
}
