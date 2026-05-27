package local

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"text/template"
)

const localLaunchAgentLabel = "com.loom.local"

type launchAgentConfig struct {
	Label             string
	BinaryPath        string
	DataDir           string
	Port              int
	Path              string
	FleetDBRuntimeDir string
	StdoutPath        string
	StderrPath        string
}

var localLaunchAgentTemplate = template.Must(template.New("local-launchagent").Funcs(template.FuncMap{
	"xmlEscape": func(s string) string {
		var b bytes.Buffer
		if err := xml.EscapeText(&b, []byte(s)); err != nil {
			return s
		}
		return b.String()
	},
}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label | xmlEscape}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath | xmlEscape}}</string>
        <string>local</string>
        <string>--data-dir</string>
        <string>{{.DataDir | xmlEscape}}</string>
        <string>service</string>
        {{- if gt .Port 0}}
        <string>--port</string>
        <string>{{.Port}}</string>
        {{- end}}
    </array>
    <key>WorkingDirectory</key>
    <string>{{.DataDir | xmlEscape}}</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>{{.StdoutPath | xmlEscape}}</string>
    <key>StandardErrorPath</key>
    <string>{{.StderrPath | xmlEscape}}</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>LOOM_CONFIG_DIR</key>
        <string>{{.DataDir | xmlEscape}}</string>
        <key>LOOM_DESKTOP_DATA_DIR</key>
        <string>{{.DataDir | xmlEscape}}</string>
        <key>LOOM_FLEET_DB_RUNTIME_DIR</key>
        <string>{{.FleetDBRuntimeDir | xmlEscape}}</string>
        <key>PATH</key>
        <string>{{.Path | xmlEscape}}</string>
    </dict>
</dict>
</plist>
`))

func installLaunchAgent(w io.Writer, dataDir string, port int) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("persistent local service install is only supported on macOS")
	}
	if _, err := exec.LookPath("launchctl"); err != nil {
		return fmt.Errorf("launchctl not found in PATH")
	}
	cfg, err := buildLaunchAgentConfig(dataDir, port)
	if err != nil {
		return err
	}
	content, err := renderLaunchAgentPlist(cfg)
	if err != nil {
		return err
	}
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	uid := fmt.Sprintf("%d", os.Getuid())
	if _, err := os.Stat(plistPath); err == nil {
		_, _ = fmt.Fprintln(w, "Replacing existing local runtime service definition")
		_ = launchctl("bootout", "gui/"+uid, plistPath)
	}
	if err := writeLaunchAgentFile(plistPath, content); err != nil {
		return err
	}
	if err := launchctl("bootstrap", "gui/"+uid, plistPath); err != nil {
		if err2 := launchctl("load", plistPath); err2 != nil {
			return fmt.Errorf("loading local runtime service: %v (also tried launchctl load: %v)", err, err2)
		}
	}
	_, _ = fmt.Fprintf(w, "Loom local runtime service installed: %s\n", plistPath)
	return nil
}

func uninstallLaunchAgent(w io.Writer) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("persistent local service install is only supported on macOS")
	}
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		_, _ = fmt.Fprintf(w, "Loom local runtime service is not installed: %s\n", plistPath)
		return nil
	}
	if _, err := exec.LookPath("launchctl"); err != nil {
		return fmt.Errorf("launchctl not found in PATH")
	}
	uid := fmt.Sprintf("%d", os.Getuid())
	if err := launchctl("bootout", "gui/"+uid, plistPath); err != nil {
		_ = launchctl("unload", plistPath)
	}
	if err := os.Remove(plistPath); err != nil {
		return fmt.Errorf("remove %s: %w", plistPath, err)
	}
	_, _ = fmt.Fprintf(w, "Loom local runtime service uninstalled: %s\n", plistPath)
	return nil
}

func buildLaunchAgentConfig(dataDir string, port int) (launchAgentConfig, error) {
	binaryPath, err := os.Executable()
	if err != nil {
		return launchAgentConfig{}, fmt.Errorf("resolve loom executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(binaryPath); err == nil {
		binaryPath = resolved
	}
	return launchAgentConfig{
		Label:             localLaunchAgentLabel,
		BinaryPath:        binaryPath,
		DataDir:           dataDir,
		Port:              port,
		Path:              launchAgentPathEnv(),
		FleetDBRuntimeDir: localFleetDBRuntimeDir(dataDir),
		StdoutPath:        serviceLogPath(dataDir),
		StderrPath:        serviceLogPath(dataDir),
	}, nil
}

func renderLaunchAgentPlist(cfg launchAgentConfig) (string, error) {
	var buf bytes.Buffer
	if err := localLaunchAgentTemplate.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("render local launch agent: %w", err)
	}
	return buf.String(), nil
}

func writeLaunchAgentFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func launchAgentPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", localLaunchAgentLabel+".plist"), nil
}

func launchAgentPathEnv() string {
	if path := os.Getenv("PATH"); path != "" {
		return path
	}
	return "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

// launchctl runs an /bin/launchctl subcommand. All call sites in this
// package target launchctl specifically (bootout/bootstrap/load/unload);
// generalizing the receiver to an arbitrary binary tripped unparam.
func launchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...) //nolint:gosec // fixed executable; args are the subcommand + plist path
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
