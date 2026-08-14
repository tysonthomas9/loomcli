package interaction

import (
	"context"
	"strings"
	"time"
)

const setupTerminalRows uint16 = 24
const setupTerminalCols uint16 = 80

type setupCommandSpec struct {
	displayName string
	commands    map[string]string
	messages    map[string]string
	titles      map[string]string
	manual      map[string]bool
}

// TerminalSetupDefinition is the fully resolved allowlisted setup intent.
// Callers choose only backend and action; the catalog owns command, copy, and
// whether operator interaction is required.
type TerminalSetupDefinition struct {
	Backend     string
	Action      string
	DisplayName string
	Command     string
	Title       string
	Message     string
	Manual      bool
}

// TerminalSetupCatalog resolves an allowlisted backend/action pair. It is an
// injected Interaction port so delivery cannot smuggle arbitrary shell input
// and backend command knowledge does not spread across handlers.
type TerminalSetupCatalog interface {
	ResolveTerminalSetup(backend, action string) (TerminalSetupDefinition, error)
}

type staticTerminalSetupCatalog struct{}

// NewTerminalSetupCatalog returns Loom's built-in setup-command catalog.
func NewTerminalSetupCatalog() TerminalSetupCatalog { return staticTerminalSetupCatalog{} }

var setupCommandSpecs = map[string]setupCommandSpec{
	"claude": {
		displayName: "Claude",
		commands: map[string]string{
			"install":   "npm install -g @anthropic-ai/claude-code",
			"login":     "claude login",
			"configure": "claude login",
			"test":      "claude --version",
		},
	},
	"codex": {
		displayName: "Codex",
		commands: map[string]string{
			"install":   "npm install -g @openai/codex",
			"login":     "codex login",
			"configure": "codex login",
			"test":      "codex --version",
		},
	},
	"gemini": {
		displayName: "Gemini",
		commands: map[string]string{
			"install":   "npm install -g @google/gemini-cli",
			"login":     "gemini",
			"configure": "gemini",
			"test":      "gemini --version",
		},
	},
	"opencode": {
		displayName: "OpenCode",
		commands: map[string]string{
			"install":   "npm install -g opencode-ai",
			"login":     "opencode auth login",
			"configure": "opencode auth login",
			"test":      "opencode --version",
		},
	},
	"cursor": {
		displayName: "Cursor",
		commands: map[string]string{
			"install": "curl https://cursor.com/install -fsS | bash",
			"login": strings.Join([]string{
				"printf '%s\\n'",
				"'Cursor setup uses CURSOR_API_KEY for Loom.'",
				"'Set CURSOR_API_KEY in the environment that launches Loom, then restart and click Recheck.'",
				"'For this terminal only, you can run: export CURSOR_API_KEY=...'",
			}, " "),
			"configure": strings.Join([]string{
				"printf '%s\\n'",
				"'Cursor setup uses CURSOR_API_KEY for Loom.'",
				"'Set CURSOR_API_KEY in the environment that launches Loom, then restart and click Recheck.'",
				"'For this terminal only, you can run: export CURSOR_API_KEY=...'",
			}, " "),
			"test": "cursor-agent --version",
		},
		messages: map[string]string{
			"login":     "The setup terminal shows how Loom detects Cursor credentials. You can take control there to configure this shell.",
			"configure": "The setup terminal shows how Loom detects Cursor credentials. You can take control there to configure this shell.",
		},
		titles: map[string]string{
			"login":     "Configure Cursor credentials",
			"configure": "Configure Cursor credentials",
		},
		manual: map[string]bool{
			"login":     true,
			"configure": true,
		},
	},
}

func setupCommandFor(backend, action string) (setupCommandSpec, string, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	action = strings.ToLower(strings.TrimSpace(action))
	spec, ok := setupCommandSpecs[backend]
	if !ok {
		return setupCommandSpec{}, "", terminalError(ErrInvalid, "unsupported setup backend", nil)
	}
	command := spec.commands[action]
	if command == "" {
		return setupCommandSpec{}, "", terminalError(ErrInvalid, "unsupported setup action", nil)
	}
	return spec, command, nil
}

func (staticTerminalSetupCatalog) ResolveTerminalSetup(backend, action string) (TerminalSetupDefinition, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	action = strings.ToLower(strings.TrimSpace(action))
	spec, command, err := setupCommandFor(backend, action)
	if err != nil {
		return TerminalSetupDefinition{}, err
	}
	return TerminalSetupDefinition{
		Backend: backend, Action: action, DisplayName: spec.displayName,
		Command: command, Title: setupTitle(action, spec), Message: setupMessage(action, spec),
		Manual: setupIsManual(action, spec),
	}, nil
}

func setupSessionName(wsID, backend string) string {
	safeWorkspace := sanitizeSessionPart(wsID)
	prefix := ""
	if safeWorkspace != "" && safeWorkspace != "default" {
		prefix = safeWorkspace + "--"
	}
	return prefix + "lead-shell-setup-" + sanitizeSessionPart(backend)
}

func sanitizeSessionPart(value string) string {
	value = strings.ReplaceAll(value, ".", "-")
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_' || r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func setupShellArgv(command string) []string {
	script := strings.Join([]string{
		"printf '\\033[36m[loom] starting setup command:\\033[0m %s\\n' " + shellQuote(command),
		command,
		"status=$?",
		"printf '\\n\\033[36m[loom] setup exited with status %s\\033[0m\\n' \"$status\"",
		"exec \"${SHELL:-/bin/sh}\" -l",
	}, "\n")
	return []string{"-lc", script}
}

func setupTitle(action string, spec setupCommandSpec) string {
	if spec.titles != nil {
		if title := spec.titles[action]; title != "" {
			return title
		}
	}
	switch action {
	case "install":
		return "Install " + spec.displayName
	case "login":
		return "Log in to " + spec.displayName
	case "configure":
		return "Configure " + spec.displayName
	case "test":
		return "Test " + spec.displayName
	default:
		return spec.displayName + " setup"
	}
}

func setupMessage(action string, spec setupCommandSpec) string {
	if spec.messages != nil {
		if message := spec.messages[action]; message != "" {
			return message
		}
	}
	return "The backend started this command in the setup terminal. You can take control there if it prompts or fails."
}

func setupIsManual(action string, spec setupCommandSpec) bool {
	return spec.manual != nil && spec.manual[action]
}

func (s *TerminalTabService) StartSetup(ctx context.Context, wsID string, req TerminalSetupRequest) (*TerminalSetupResult, error) {
	if s.runtime == nil {
		return nil, terminalError(ErrUnavailable, "terminal runtime not initialized", nil)
	}

	if s.agentTerminal.Setup == nil {
		return nil, terminalError(ErrUnavailable, "terminal setup catalog is unavailable", nil)
	}
	definition, err := s.agentTerminal.Setup.ResolveTerminalSetup(req.Backend, req.Action)
	if err != nil {
		return nil, err
	}
	backend, action, command := definition.Backend, definition.Action, definition.Command

	session := setupSessionName(wsID, backend)
	label := definition.DisplayName + " setup"
	key := TerminalKey{WorkspaceKey: wsID, TerminalID: session}
	created, err := s.runtime.Ensure(key, setupTerminalCols, setupTerminalRows, &LaunchSpec{Argv: setupShellArgv(command)})
	if err != nil {
		return nil, terminalError(ErrUnavailable, "failed to start setup terminal", err)
	}
	if !created {
		if err := s.runtime.WriteInput(key, []byte(command+"\n")); err != nil {
			return nil, terminalError(ErrUnavailable, "failed to run setup command", err)
		}
	}

	if err := s.upsertSetupTab(ctx, wsID, session, label); err != nil {
		return nil, err
	}

	return &TerminalSetupResult{
		SessionName: session,
		Label:       label,
		Backend:     backend,
		Action:      action,
		Command:     command,
		Title:       definition.Title,
		Message:     definition.Message,
		Manual:      definition.Manual,
		Created:     created,
	}, nil
}

func (s *TerminalTabService) upsertSetupTab(ctx context.Context, wsID, session, label string) error {
	if s.tabStore == nil {
		return nil
	}

	existing, err := s.tabStore.Get(ctx, wsID, session)
	if err != nil {
		return terminalError(ErrUnavailable, "failed to get setup tab metadata", err)
	}
	if existing == nil {
		now := time.Now().UTC()
		sortOrder := 0
		if tabs, listErr := s.tabStore.List(ctx, wsID); listErr == nil {
			sortOrder = len(tabs)
		}
		meta := &TabMetadata{
			SessionName: session,
			Workspace:   wsID,
			Label:       label,
			Kind:        "setup",
			Backend:     "shell",
			SortOrder:   sortOrder,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.tabStore.Set(ctx, meta); err != nil {
			return terminalError(ErrUnavailable, "failed to create setup tab metadata", err)
		}
	} else if existing.Label != label || existing.Kind != "setup" || existing.Backend != "shell" {
		existing.Label = label
		existing.Kind = "setup"
		existing.Backend = "shell"
		existing.UpdatedAt = time.Now().UTC()
		if err := s.tabStore.Set(ctx, existing); err != nil {
			return terminalError(ErrUnavailable, "failed to update setup tab metadata", err)
		}
	}
	return nil
}
