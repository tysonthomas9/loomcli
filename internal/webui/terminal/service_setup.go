package terminal

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
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
		return setupCommandSpec{}, "", apperrors.ErrValidation("unsupported setup backend")
	}
	command := spec.commands[action]
	if command == "" {
		return setupCommandSpec{}, "", apperrors.ErrValidation("unsupported setup action")
	}
	return spec, command, nil
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

func (s *terminalServiceImpl) StartSetup(ctx context.Context, wsID string, req TerminalSetupRequest) (*TerminalSetupResult, error) {
	if s.ptyMgr == nil {
		return nil, apperrors.ErrUnavailable("terminal manager not initialized")
	}
	runner, ok := s.ptyMgr.(PTYCommandRunner)
	if !ok {
		return nil, apperrors.ErrUnavailable("terminal setup runner not available")
	}

	backend := strings.ToLower(strings.TrimSpace(req.Backend))
	action := strings.ToLower(strings.TrimSpace(req.Action))
	spec, command, err := setupCommandFor(backend, action)
	if err != nil {
		return nil, err
	}

	session := setupSessionName(wsID, backend)
	label := spec.displayName + " setup"
	key := SessionKey{Workspace: wsID, Name: session}
	created, err := runner.EnsureSession(key, setupTerminalCols, setupTerminalRows, setupShellArgv(command))
	if err != nil {
		return nil, apperrors.ErrInternal("failed to start setup terminal", err)
	}
	if !created {
		if err := runner.WriteToSession(key, []byte(command+"\n")); err != nil {
			return nil, apperrors.ErrInternal("failed to run setup command", err)
		}
	}

	if err := s.upsertSetupTab(ctx, wsID, session, label); err != nil {
		slog.Warn("failed to persist setup terminal metadata",
			"workspace", wsID, "session", session, "err", err)
	}
	if s.redisClient != nil {
		_ = s.PatchTerminalState(ctx, wsID, session)
	}

	return &TerminalSetupResult{
		SessionName: session,
		Label:       label,
		Backend:     backend,
		Action:      action,
		Command:     command,
		Title:       setupTitle(action, spec),
		Message:     setupMessage(action, spec),
		Manual:      setupIsManual(action, spec),
		Created:     created,
	}, nil
}

func (s *terminalServiceImpl) upsertSetupTab(ctx context.Context, wsID, session, label string) error {
	if s.tabStore == nil {
		return nil
	}

	existing, err := s.tabStore.Get(ctx, wsID, session)
	if err != nil {
		return apperrors.ErrInternal("failed to get setup tab metadata", err)
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
			SortOrder:   sortOrder,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.tabStore.Set(ctx, meta); err != nil {
			return apperrors.ErrInternal("failed to create setup tab metadata", err)
		}
	} else if existing.Label != label {
		if _, err := s.tabStore.Patch(ctx, wsID, session, map[string]string{"label": label}); err != nil {
			return apperrors.ErrInternal("failed to update setup tab metadata", err)
		}
	}

	if s.hub != nil {
		s.hub.Broadcast(&realtime.MutationPayload{
			Type:        "terminal_metadata",
			EntityType:  "terminal",
			EntityID:    session,
			Action:      "terminal.metadata",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: wsID,
		})
	}
	return nil
}
