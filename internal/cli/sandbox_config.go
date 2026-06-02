package cli

import "strings"

// overlaySandboxConfig applies non-zero fields from src onto dst.
func overlaySandboxConfig(dst, src *SandboxConfig) {
	if dst == nil || src == nil {
		return
	}
	if len(src.Providers) > 0 {
		dst.Providers = src.Providers
	}
	if src.Network != "" {
		dst.Network = src.Network
	}
	if src.From != "" {
		dst.From = src.From
	}
	if src.Backend != "" {
		dst.Backend = src.Backend
	}
}

// mergeSandboxConfig produces a merged SandboxConfig from daemon-level defaults
// and an optional agent-level override. Agent-level fields win when set.
func mergeSandboxConfig(daemon *SandboxConfig, agent *SandboxConfig) SandboxConfig {
	var merged SandboxConfig
	if daemon != nil {
		merged = *daemon
	}
	if agent != nil {
		overlaySandboxConfig(&merged, agent)
	}
	return merged
}

// resolveRepoURL returns the git remote URL for the given project directory.
// It shells out to "git remote get-url origin". Returns an empty string on error.
func resolveRepoURL(projectDir string) string {
	output, err := RunGitCommand(projectDir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// resolveStrategy returns the ExecutionStrategy for an agent based on config.
// When execution is "sandbox", it merges daemon-level and agent-level sandbox config
// and returns a SandboxStrategy that shells out to the openshell CLI.
func resolveStrategy(agent AgentEntry, daemonSandbox *SandboxConfig, projectDir string) ExecutionStrategy {
	if agent.Execution != "sandbox" {
		return &DirectStrategy{}
	}
	merged := mergeSandboxConfig(daemonSandbox, agent.Sandbox)
	repoURL := resolveRepoURL(projectDir)
	return &SandboxStrategy{
		cfg:        merged,
		projectDir: projectDir,
		repoURL:    repoURL,
	}
}
