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
