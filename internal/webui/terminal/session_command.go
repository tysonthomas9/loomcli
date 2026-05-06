package terminal

import (
	"fmt"
	"os"
	"strings"
)

// ValidBackends is the list of supported AI backend names. The web UI
// validates its "backend" dropdown against this list and the per-session
// command resolver uses it to distinguish valid `lead-{backend}-{n}`
// session names from arbitrary ones.
var ValidBackends = []string{"claude", "codex", "opencode", "gemini", "cursor"}

var currentExecutable = os.Executable

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func loomExecutableForTerminal() string {
	exe, err := currentExecutable()
	if err != nil || exe == "" {
		return "loom"
	}
	return exe
}

func leadCommandForBackend(backend string) string {
	return fmt.Sprintf("%s lead --backend %s", shellQuote(loomExecutableForTerminal()), backend)
}

// ArgvForSession returns the shell argv to run for a given session name,
// or nil to fall back to PTYManager's default argv.
//
// Session names encode the backend the web UI picked for this tab:
//
//	lead-shell-{n}          → login shell ("-l")
//	{ws}--lead-shell-{n}    → login shell ("-l")
//	lead-{backend}-{n}      → "-c", "{current executable} lead --backend {backend}"
//	{ws}--lead-{backend}-{n}→ same
//	anything else           → nil (use manager default)
func ArgvForSession(session string) []string {
	// Strip an optional "{workspace}--" prefix before matching.
	name := session
	if idx := strings.LastIndex(name, "--lead-"); idx >= 0 {
		name = name[idx+2:]
	}

	if strings.HasPrefix(name, "lead-shell-") {
		return []string{"-l"}
	}
	if !strings.HasPrefix(name, "lead-") {
		return nil
	}
	rest := strings.TrimPrefix(name, "lead-")
	// Strip trailing "-{n}" counter.
	dash := strings.LastIndex(rest, "-")
	if dash <= 0 {
		return nil
	}
	backend := rest[:dash]
	for _, valid := range ValidBackends {
		if backend == valid {
			return []string{"-c", leadCommandForBackend(backend)}
		}
	}
	return nil
}
