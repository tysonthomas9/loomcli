package interaction

import (
	"fmt"
	"strings"
)

// ValidBackends is the list of supported AI backend names used by workspace
// configuration and durable terminal launch-intent validation.
var ValidBackends = []string{"claude", "codex", "opencode", "gemini", "cursor"}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func loomExecutableForTerminal() string {
	return "loom"
}

// LoomExecutableForTerminal returns Interaction's product command name.
// The private PTY adapter pins the current Loom executable directory on PATH,
// keeping operating-system executable discovery out of the capability core.
func LoomExecutableForTerminal() string {
	return loomExecutableForTerminal()
}

// ShellArgvForCommand converts argv into the shell argv used by PTYManager.
func ShellArgvForCommand(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return []string{"-c", strings.Join(quoted, " ")}
}

func leadCommandForBackend(backend string) string {
	return fmt.Sprintf("%s lead --backend %s", shellQuote(loomExecutableForTerminal()), backend)
}

// LaunchSpecForBackend converts a validated tab-creation intent into the
// complete command envelope persisted with that tab. WebSocket attachment
// consumes this envelope verbatim; session names are placement identifiers and
// never act as a second command protocol.
func LaunchSpecForBackend(backend, loomConfigDir string) (*LaunchSpec, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	var argv []string
	if backend == "shell" {
		argv = []string{"-l"}
	} else {
		validBackend := false
		for _, valid := range ValidBackends {
			if backend == valid {
				validBackend = true
				break
			}
		}
		if !validBackend {
			return nil, fmt.Errorf("unsupported terminal backend %q", backend)
		}
		argv = []string{"-c", leadCommandForBackend(backend)}
	}

	launch := &LaunchSpec{Argv: argv}
	if loomConfigDir = strings.TrimSpace(loomConfigDir); loomConfigDir != "" {
		launch.Env = map[string]string{"LOOM_CONFIG_DIR": loomConfigDir}
	}
	return launch, nil
}

// IsValidTerminalBackend reports whether a backend can be used to create a
// generic terminal tab. "shell" is intentionally terminal-only and is not
// added to ValidBackends, which remains the list of AI backends accepted by
// workspace configuration.
func IsValidTerminalBackend(backend string) bool {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "shell" {
		return true
	}
	for _, valid := range ValidBackends {
		if backend == valid {
			return true
		}
	}
	return false
}
