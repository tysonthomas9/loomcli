package serve

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var (
	installServiceInstall   bool
	installServiceUninstall bool
	installServiceName      string
	installServicePort      int
	installServiceBind      string
	installServiceEnv       []string
)

// validServiceName allows only safe characters for service name suffixes.
var validServiceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

var installServiceCmd = &cobra.Command{
	Use:   "install-service",
	Short: "Generate or install a platform-native service definition for loom serve",
	Long: `Generate and optionally install a service definition to run loom serve
as a persistent background service.

On Linux, generates a systemd user unit file.
On macOS, generates a launchd user agent plist.

By default, prints the service definition to stdout for inspection.
Use --install to write it to the platform-specific location and enable it.
Use --uninstall to stop and remove a previously installed service.

The service runs loom serve with the specified flags and restarts on failure.
The working directory is set to the current directory.`,
	GroupID: "config",
	RunE:    runInstallService,
}

func init() {
	installServiceCmd.Flags().BoolVar(&installServiceInstall, "install", false, "Write and enable the service (instead of printing to stdout)")
	installServiceCmd.Flags().BoolVar(&installServiceUninstall, "uninstall", false, "Stop and remove an installed service")
	installServiceCmd.Flags().StringVar(&installServiceName, "name", "", "Service name suffix for multi-project setups (e.g. my-project)")
	installServiceCmd.Flags().IntVar(&installServicePort, "port", 8080, "Port for loom serve")
	installServiceCmd.Flags().StringVar(&installServiceBind, "bind", "127.0.0.1", "Bind address for loom serve")
	installServiceCmd.Flags().StringArrayVar(&installServiceEnv, "env", nil, "Extra environment variables (KEY=VALUE, repeatable)")
	cli.RegisterCommand(installServiceCmd)
}

// serviceConfig holds all template variables for service generation.
type serviceConfig struct {
	Name             string
	Description      string
	BinaryPath       string
	WorkingDirectory string
	Port             int
	BindAddr         string
	LogDir           string
	ExtraEnv         []string
}

var systemdTemplate = template.Must(template.New("systemd").Funcs(template.FuncMap{
	"escapeSystemdEnv": func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return s
	},
}).Parse(`[Unit]
Description={{.Description}}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory={{.WorkingDirectory}}
ExecStart={{.BinaryPath}} serve --port {{.Port}} --bind {{.BindAddr}}
Restart=on-failure
RestartSec=5
{{- range .ExtraEnv}}
Environment="{{. | escapeSystemdEnv}}"
{{- end}}
StandardOutput=journal
StandardError=journal
SyslogIdentifier={{.Name}}
LimitNOFILE=65536

[Install]
WantedBy=default.target
`))

var launchdTemplate = template.Must(template.New("launchd").Funcs(template.FuncMap{
	"xmlEscape": func(s string) string {
		var b strings.Builder
		if err := xml.EscapeText(&b, []byte(s)); err != nil {
			return s
		}
		return b.String()
	},
	"splitEnv": func(s string) [2]string {
		idx := strings.Index(s, "=")
		if idx < 0 {
			return [2]string{s, ""}
		}
		return [2]string{s[:idx], s[idx+1:]}
	},
}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Name | xmlEscape}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath | xmlEscape}}</string>
        <string>serve</string>
        <string>--port</string>
        <string>{{.Port}}</string>
        <string>--bind</string>
        <string>{{.BindAddr | xmlEscape}}</string>
    </array>
    <key>WorkingDirectory</key>
    <string>{{.WorkingDirectory | xmlEscape}}</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>{{.LogDir | xmlEscape}}/loom-serve.stdout.log</string>
    <key>StandardErrorPath</key>
    <string>{{.LogDir | xmlEscape}}/loom-serve.stderr.log</string>
    {{- if .ExtraEnv}}
    <key>EnvironmentVariables</key>
    <dict>
        {{- range .ExtraEnv}}
        {{- $parts := splitEnv .}}
        <key>{{index $parts 0 | xmlEscape}}</key>
        <string>{{index $parts 1 | xmlEscape}}</string>
        {{- end}}
    </dict>
    {{- end}}
</dict>
</plist>
`))

func runInstallService(_ *cobra.Command, _ []string) error {
	if err := validateInstallServiceFlags(); err != nil {
		return err
	}

	cfg, err := buildServiceConfig()
	if err != nil {
		return err
	}

	switch runtime.GOOS {
	case "linux":
		return handleSystemd(cfg, installServiceInstall, installServiceUninstall)
	case "darwin":
		return handleLaunchd(cfg, installServiceInstall, installServiceUninstall)
	default:
		return fmt.Errorf("install-service is not supported on %s; supported platforms: linux (systemd), darwin (launchd)", runtime.GOOS)
	}
}

func validateInstallServiceFlags() error {
	if installServiceInstall && installServiceUninstall {
		return fmt.Errorf("--install and --uninstall are mutually exclusive")
	}
	if installServiceName != "" && !validServiceName.MatchString(installServiceName) {
		return fmt.Errorf("invalid --name %q: must contain only alphanumeric characters, hyphens, and dots", installServiceName)
	}
	for _, e := range installServiceEnv {
		if !strings.Contains(e, "=") {
			return fmt.Errorf("invalid --env value %q: must be KEY=VALUE format", e)
		}
	}
	return nil
}

func buildServiceConfig() (serviceConfig, error) {
	resolved := resolveServiceBinaryPath()
	cwd, err := os.Getwd()
	if err != nil {
		return serviceConfig{}, fmt.Errorf("cannot determine working directory: %w", err)
	}
	return serviceConfig{
		Name:             buildServiceName(runtime.GOOS),
		Description:      "Loom agent management server",
		BinaryPath:       resolved,
		WorkingDirectory: cwd,
		Port:             installServicePort,
		BindAddr:         installServiceBind,
		LogDir:           resolveServiceLogDir(cwd),
		ExtraEnv:         installServiceEnv,
	}, nil
}

func resolveServiceBinaryPath() string {
	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot determine loom binary path: %v\n", err)
		return "loom"
	}
	resolved, err := filepath.EvalSymlinks(binPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot resolve symlinks for %s: %v (using unresolved path)\n", binPath, err)
		resolved = binPath
	}
	if strings.Contains(resolved, os.TempDir()) || strings.Contains(resolved, "go-build") {
		fmt.Fprintf(os.Stderr, "Warning: loom binary is in a temporary directory (%s). The service may fail after reboot. Install loom to a permanent location first.\n", resolved)
	}
	return resolved
}

func resolveServiceLogDir(cwd string) string {
	return filepath.Join(cwd, ".loom", "logs")
}

func buildServiceName(goos string) string {
	switch goos {
	case "linux":
		if installServiceName != "" {
			return "loom-serve-" + installServiceName
		}
		return "loom-serve"
	case "darwin":
		if installServiceName != "" {
			return "com.loom.serve." + installServiceName
		}
		return "com.loom.serve"
	default:
		return "loom-serve"
	}
}

func renderTemplate(tmpl *template.Template, cfg serviceConfig) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("rendering %s template: %w", tmpl.Name(), err)
	}
	return buf.String(), nil
}

// handleSystemd manages systemd user unit lifecycle.
func handleSystemd(cfg serviceConfig, install, uninstall bool) error {
	unitPath := systemdUnitPath(cfg.Name)

	if uninstall {
		return uninstallSystemd(cfg.Name, unitPath)
	}

	content, err := renderTemplate(systemdTemplate, cfg)
	if err != nil {
		return err
	}

	if install {
		return installSystemd(cfg.Name, unitPath, content)
	}

	fmt.Println("# systemd user unit file")
	fmt.Printf("# Save to: %s\n", unitPath)
	fmt.Printf("# Then run: systemctl --user daemon-reload && systemctl --user enable --now %s\n\n", cfg.Name)
	fmt.Print(content)
	return nil
}

func uninstallSystemd(name, unitPath string) error {
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		fmt.Printf("Service %s is not installed\n", name)
		return nil
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found in PATH. Is systemd available?")
	}
	_ = execCommand("systemctl", "--user", "stop", name)
	_ = execCommand("systemctl", "--user", "disable", name)
	if err := os.Remove(unitPath); err != nil {
		return fmt.Errorf("removing unit file %s: %w", unitPath, err)
	}
	_ = execCommand("systemctl", "--user", "daemon-reload")
	fmt.Printf("Service %s uninstalled\n", name)
	return nil
}

func installSystemd(name, unitPath, content string) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found in PATH. Is systemd available?")
	}
	if _, err := os.Stat(unitPath); err == nil {
		fmt.Println("Replacing existing service definition")
	}
	if err := writeServiceFile(unitPath, content); err != nil {
		return err
	}
	if err := execCommand("systemctl", "--user", "daemon-reload"); err != nil {
		u, _ := user.Current()
		username := "USERNAME"
		if u != nil {
			username = u.Username
		}
		return fmt.Errorf("systemd user session not available: %v. You may need to enable lingering: loginctl enable-linger %s", err, username)
	}
	if err := execCommand("systemctl", "--user", "enable", name); err != nil {
		return fmt.Errorf("enabling service: %w", err)
	}
	if err := execCommand("systemctl", "--user", "start", name); err != nil {
		return fmt.Errorf("starting service: %w", err)
	}
	fmt.Printf("Service %s installed and started\n\n", name)
	fmt.Printf("Check status:  systemctl --user status %s\n", name)
	fmt.Printf("View logs:     journalctl --user -u %s -f\n", name)
	fmt.Printf("Stop:          systemctl --user stop %s\n", name)
	fmt.Printf("Uninstall:     loom install-service --uninstall\n")
	return nil
}

// handleLaunchd manages launchd user agent lifecycle.
func handleLaunchd(cfg serviceConfig, install, uninstall bool) error {
	plistPath := launchdPlistPath(cfg.Name)

	if uninstall {
		return uninstallLaunchd(cfg.Name, plistPath)
	}

	content, err := renderTemplate(launchdTemplate, cfg)
	if err != nil {
		return err
	}

	if install {
		return installLaunchd(cfg, plistPath, content)
	}

	fmt.Println("<!-- launchd user agent plist -->")
	fmt.Printf("<!-- Save to: %s -->\n", plistPath)
	fmt.Printf("<!-- Then run: launchctl bootstrap gui/$(id -u) %s -->\n\n", plistPath)
	fmt.Print(content)
	return nil
}

func uninstallLaunchd(name, plistPath string) error {
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		fmt.Printf("Service %s is not installed\n", name)
		return nil
	}
	if _, err := exec.LookPath("launchctl"); err != nil {
		return fmt.Errorf("launchctl not found in PATH")
	}
	uid := fmt.Sprintf("%d", os.Getuid())
	if err := execCommand("launchctl", "bootout", "gui/"+uid, plistPath); err != nil {
		_ = execCommand("launchctl", "unload", plistPath)
	}
	if err := os.Remove(plistPath); err != nil {
		return fmt.Errorf("removing plist file %s: %w", plistPath, err)
	}
	fmt.Printf("Service %s uninstalled\n", name)
	return nil
}

func installLaunchd(cfg serviceConfig, plistPath, content string) error {
	if _, err := exec.LookPath("launchctl"); err != nil {
		return fmt.Errorf("launchctl not found in PATH")
	}
	uid := fmt.Sprintf("%d", os.Getuid())
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Println("Replacing existing service definition")
		_ = execCommand("launchctl", "bootout", "gui/"+uid, plistPath)
	}
	if err := writeServiceFile(plistPath, content); err != nil {
		return err
	}
	if err := execCommand("launchctl", "bootstrap", "gui/"+uid, plistPath); err != nil {
		if err2 := execCommand("launchctl", "load", plistPath); err2 != nil {
			return fmt.Errorf("loading service: %v (also tried launchctl load: %v)", err, err2)
		}
	}
	fmt.Printf("Service %s installed and started\n\n", cfg.Name)
	fmt.Printf("Check status:  launchctl list | grep %s\n", cfg.Name)
	fmt.Printf("View logs:     tail -f %s/loom-serve.stdout.log\n", cfg.LogDir)
	fmt.Printf("Stop:          launchctl bootout gui/%s %s\n", uid, plistPath)
	fmt.Printf("Uninstall:     loom install-service --uninstall\n")
	return nil
}

func writeServiceFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing service file %s: %w", path, err)
	}
	return nil
}

func systemdUnitPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", name+".service")
}

func launchdPlistPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", name+".plist")
}

// execCommand runs a system command and returns an error if it fails.
func execCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec // G204: intentional service management command
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
