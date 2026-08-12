package backends

import (
	"os"
	"strings"
)

func resolveAgentEffort() string {
	if effort := strings.TrimSpace(os.Getenv("LOOM_AGENT_EFFORT")); effort != "" {
		return effort
	}
	return strings.TrimSpace(os.Getenv("LOOM_CLAUDE_EFFORT"))
}

// resolveAgentModel returns the model the supervisor pinned for this agent
// (role.model → LOOM_AGENT_MODEL). Empty means the backend's own default.
func resolveAgentModel() string {
	return strings.TrimSpace(os.Getenv("LOOM_AGENT_MODEL"))
}

// appendCodexModelArgs prepends the config-style model override, mirroring
// appendCodexEffortArgs.
func appendCodexModelArgs(args []string, model string) []string {
	if model == "" {
		return args
	}
	return append([]string{"-c", `model="` + model + `"`}, args...)
}

func appendCodexEffortArgs(args []string, effort string) []string {
	if effort == "" {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, "-c", "model_reasoning_effort=\""+codexEffort(effort)+"\"")
	out = append(out, args...)
	return out
}

func codexEffort(effort string) string {
	if effort == "max" {
		return "xhigh"
	}
	return effort
}
