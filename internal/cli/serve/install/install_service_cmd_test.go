package install

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func captureInstallStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
		_ = r.Close()
	}()

	fn()
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	return buf.String()
}

// baseCfg returns a minimal serviceConfig for testing.
func baseCfg() serviceConfig {
	return serviceConfig{
		Name:             "loom-serve",
		Description:      "Loom agent management server",
		BinaryPath:       "/usr/local/bin/loom",
		WorkingDirectory: "/home/user/project",
		Port:             8080,
		BindAddr:         "127.0.0.1",
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

	// --no-webui flag was removed; the generated unit file must not reference it.
	if strings.Contains(out, "--no-webui") {
		t.Errorf("--no-webui should not appear in generated unit file (flag removed)")
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

	// --no-webui flag was removed; the generated plist must not reference it.
	if strings.Contains(out, "--no-webui") {
		t.Errorf("--no-webui should not appear in generated plist (flag removed)")
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

func TestHandleSystemdAndLaunchdPrintDefinitions(t *testing.T) {
	cfg := baseCfg()
	systemdOut := captureInstallStdout(t, func() {
		if err := handleSystemd(cfg, false, false); err != nil {
			t.Fatalf("handleSystemd print: %v", err)
		}
	})
	if !strings.Contains(systemdOut, "# systemd user unit file") || !strings.Contains(systemdOut, "ExecStart=/usr/local/bin/loom serve") {
		t.Fatalf("systemd print output = %s", systemdOut)
	}

	cfg.Name = "com.loom.serve"
	launchdOut := captureInstallStdout(t, func() {
		if err := handleLaunchd(cfg, false, false); err != nil {
			t.Fatalf("handleLaunchd print: %v", err)
		}
	})
	if !strings.Contains(launchdOut, "launchd user agent plist") || !strings.Contains(launchdOut, "<key>ProgramArguments</key>") {
		t.Fatalf("launchd print output = %s", launchdOut)
	}
}

func TestUninstallMissingServicesAreNoops(t *testing.T) {
	missingSystemd := filepath.Join(t.TempDir(), "missing.service")
	out := captureInstallStdout(t, func() {
		if err := uninstallSystemd("loom-serve", missingSystemd); err != nil {
			t.Fatalf("uninstallSystemd missing: %v", err)
		}
	})
	if !strings.Contains(out, "not installed") {
		t.Fatalf("missing systemd output = %q", out)
	}

	missingPlist := filepath.Join(t.TempDir(), "missing.plist")
	out = captureInstallStdout(t, func() {
		if err := uninstallLaunchd("com.loom.serve", missingPlist); err != nil {
			t.Fatalf("uninstallLaunchd missing: %v", err)
		}
	})
	if !strings.Contains(out, "not installed") {
		t.Fatalf("missing launchd output = %q", out)
	}
}

func TestInstallAndUninstallServiceCommandMissingManagers(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if err := installSystemd("loom-serve", filepath.Join(t.TempDir(), "loom.service"), "unit"); err == nil || !strings.Contains(err.Error(), "systemctl not found") {
		t.Fatalf("installSystemd missing systemctl err = %v", err)
	}
	systemdUnit := filepath.Join(t.TempDir(), "installed.service")
	if err := os.WriteFile(systemdUnit, []byte("unit"), 0o644); err != nil {
		t.Fatalf("write unit: %v", err)
	}
	if err := uninstallSystemd("loom-serve", systemdUnit); err == nil || !strings.Contains(err.Error(), "systemctl not found") {
		t.Fatalf("uninstallSystemd missing systemctl err = %v", err)
	}

	if err := installLaunchd(baseCfg(), filepath.Join(t.TempDir(), "loom.plist"), "plist"); err == nil || !strings.Contains(err.Error(), "launchctl not found") {
		t.Fatalf("installLaunchd missing launchctl err = %v", err)
	}
	plist := filepath.Join(t.TempDir(), "installed.plist")
	if err := os.WriteFile(plist, []byte("plist"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	if err := uninstallLaunchd("com.loom.serve", plist); err == nil || !strings.Contains(err.Error(), "launchctl not found") {
		t.Fatalf("uninstallLaunchd missing launchctl err = %v", err)
	}
}

func TestInstallAndUninstallServiceWithFakeManagers(t *testing.T) {
	fakeBin := t.TempDir()
	fakeManager := "systemctl"
	cfg := baseCfg()
	cfg.LogDir = filepath.Join(t.TempDir(), "logs")
	unitPath := filepath.Join(t.TempDir(), "loom.service")
	installFn := func() error { return installSystemd("loom-serve", unitPath, "unit") }
	uninstallFn := func() error { return uninstallSystemd("loom-serve", unitPath) }
	if runtime.GOOS == "darwin" {
		fakeManager = "launchctl"
		cfg.Name = "com.loom.serve"
		unitPath = filepath.Join(t.TempDir(), "loom.plist")
		installFn = func() error { return installLaunchd(cfg, unitPath, "plist") }
		uninstallFn = func() error { return uninstallLaunchd(cfg.Name, unitPath) }
	}
	fakePath := filepath.Join(fakeBin, fakeManager)
	if err := os.WriteFile(fakePath, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatalf("write fake manager: %v", err)
	}
	t.Setenv("PATH", fakeBin)

	installOut := captureInstallStdout(t, func() {
		if err := installFn(); err != nil {
			t.Fatalf("install with fake %s: %v", fakeManager, err)
		}
	})
	if _, err := os.Stat(unitPath); err != nil {
		t.Fatalf("installed service file stat: %v", err)
	}
	if !strings.Contains(installOut, "installed") {
		t.Fatalf("install output = %q", installOut)
	}

	uninstallOut := captureInstallStdout(t, func() {
		if err := uninstallFn(); err != nil {
			t.Fatalf("uninstall with fake %s: %v", fakeManager, err)
		}
	})
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("service file after uninstall stat err = %v", err)
	}
	if !strings.Contains(uninstallOut, "uninstalled") {
		t.Fatalf("uninstall output = %q", uninstallOut)
	}
}

func TestWriteServiceFileAndLogDirResolution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "loom.service")
	if err := writeServiceFile(path, "unit"); err != nil {
		t.Fatalf("writeServiceFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read service file: %v", err)
	}
	if string(data) != "unit" {
		t.Fatalf("service file content = %q", string(data))
	}

	cwd := t.TempDir()
	if got := resolveServiceLogDir(cwd); got != filepath.Join(cwd, ".loom", "logs") {
		t.Fatalf("default log dir = %q", got)
	}
}

func TestBuildServiceNameFallbackAndInvalidNameValidation(t *testing.T) {
	oldName := installServiceName
	oldEnv := installServiceEnv
	oldInstall := installServiceInstall
	oldUninstall := installServiceUninstall
	t.Cleanup(func() {
		installServiceName = oldName
		installServiceEnv = oldEnv
		installServiceInstall = oldInstall
		installServiceUninstall = oldUninstall
	})

	installServiceName = ""
	if got := buildServiceName("plan9"); got != "loom-serve" {
		t.Fatalf("fallback service name = %q", got)
	}

	installServiceName = "bad name"
	installServiceEnv = nil
	installServiceInstall = false
	installServiceUninstall = false
	if err := validateInstallServiceFlags(); err == nil || !strings.Contains(err.Error(), "invalid --name") {
		t.Fatalf("invalid name err = %v", err)
	}
}

func TestBuildServiceConfigUsesFlagsAndCWD(t *testing.T) {
	oldName := installServiceName
	oldPort := installServicePort
	oldBind := installServiceBind
	oldEnv := installServiceEnv
	t.Cleanup(func() {
		installServiceName = oldName
		installServicePort = oldPort
		installServiceBind = oldBind
		installServiceEnv = oldEnv
	})
	dir := t.TempDir()
	t.Chdir(dir)
	installServiceName = "project"
	installServicePort = 19001
	installServiceBind = "0.0.0.0"
	installServiceEnv = []string{"A=B"}

	cfg, err := buildServiceConfig()
	if err != nil {
		t.Fatalf("buildServiceConfig: %v", err)
	}
	if cfg.WorkingDirectory != dir || cfg.Port != 19001 || cfg.BindAddr != "0.0.0.0" || len(cfg.ExtraEnv) != 1 {
		t.Fatalf("service config = %+v", cfg)
	}
	if cfg.Name == "" || cfg.BinaryPath == "" || cfg.LogDir == "" {
		t.Fatalf("service config missing resolved fields: %+v", cfg)
	}
}
