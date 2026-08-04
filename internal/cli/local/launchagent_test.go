package local

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderLaunchAgentPlist(t *testing.T) {
	cfg := launchAgentConfig{
		Label:      localLaunchAgentLabel,
		BinaryPath: "/Applications/Loom.app/Contents/MacOS/loom",
		DataDir:    "/Users/test/Library/Application Support/Loom/data",
		Port:       18444,
		Path:       "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		StdoutPath: "/Users/test/Library/Application Support/Loom/data/logs/loom-local-service.log",
		StderrPath: "/Users/test/Library/Application Support/Loom/data/logs/loom-local-service.log",
	}
	out, err := renderLaunchAgentPlist(cfg)
	if err != nil {
		t.Fatalf("renderLaunchAgentPlist() error = %v", err)
	}
	checks := []string{
		"<string>com.loom.local</string>",
		"<string>/Applications/Loom.app/Contents/MacOS/loom</string>",
		"<string>local</string>",
		"<string>--data-dir</string>",
		"<string>/Users/test/Library/Application Support/Loom/data</string>",
		"<string>service</string>",
		"<string>--port</string>",
		"<string>18444</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<key>LOOM_CONFIG_DIR</key>",
		"<key>LOOM_DESKTOP_DATA_DIR</key>",
		"<key>PATH</key>",
		"<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered plist missing %q:\n%s", want, out)
		}
	}
}

func TestRenderLaunchAgentPlistOmitsZeroPort(t *testing.T) {
	cfg := launchAgentConfig{
		Label:      localLaunchAgentLabel,
		BinaryPath: "/Applications/Loom.app/Contents/MacOS/loom",
		DataDir:    "/Users/test/Library/Application Support/Loom/data",
		Path:       "/usr/bin:/bin",
		StdoutPath: "/tmp/loom-local-service.log",
		StderrPath: "/tmp/loom-local-service.log",
	}
	out, err := renderLaunchAgentPlist(cfg)
	if err != nil {
		t.Fatalf("renderLaunchAgentPlist() error = %v", err)
	}
	if strings.Contains(out, "<string>--port</string>") {
		t.Fatalf("rendered plist should omit --port when port is zero:\n%s", out)
	}
}

func TestLaunchAgentPlistPath(t *testing.T) {
	got, err := launchAgentPlistPath()
	if err != nil {
		t.Fatalf("launchAgentPlistPath() error = %v", err)
	}
	wantSuffix := filepath.Join("Library", "LaunchAgents", "com.loom.local.plist")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("launchAgentPlistPath() = %q, want suffix %q", got, wantSuffix)
	}
}
