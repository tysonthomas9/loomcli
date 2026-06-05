package terminal

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// validBackends is the set of AI backend names the web UI treats as valid for
// lead terminals (lead-{backend}-{n}) and workspace backend config. It is
// seeded with the known built-ins as a fallback, then replaced at serve startup
// from the backend registry via SetValidBackends — so the list is not hardcoded
// and automatically tracks any newly-registered backend (built-in or plugin),
// avoiding the drift the old per-surface lists suffered.
var (
	validBackendsMu sync.RWMutex
	validBackends   = []string{"claude", "codex", "opencode", "gemini", "cursor", "flue"}
)

// SetValidBackends replaces the valid-backend set. The serve command calls this
// once at startup with cli.ListBackends() so validity is registry-derived.
func SetValidBackends(names []string) {
	cp := append([]string(nil), names...)
	validBackendsMu.Lock()
	validBackends = cp
	validBackendsMu.Unlock()
}

// ValidBackendList returns a copy of the current valid-backend names.
func ValidBackendList() []string {
	validBackendsMu.RLock()
	defer validBackendsMu.RUnlock()
	return append([]string(nil), validBackends...)
}

// IsValidBackend reports whether name is a registered/valid backend.
func IsValidBackend(name string) bool {
	validBackendsMu.RLock()
	defer validBackendsMu.RUnlock()
	for _, v := range validBackends {
		if v == name {
			return true
		}
	}
	return false
}

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
	if IsValidBackend(backend) {
		return []string{"-c", leadCommandForBackend(backend)}
	}
	return nil
}
