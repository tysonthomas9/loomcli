package interaction

import (
	"strings"
	"testing"
)

func TestFilterChildBaseEnvFailsClosedForOperatorForgeAndInternalCredentials(t *testing.T) {
	input := []string{
		"PATH=/usr/bin",
		"HOME=/tmp/home",
		"CODEX_HOME=/tmp/codex",
		"CLAUDE_CONFIG_DIR=/tmp/claude",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
		"GITHUB_TOKEN=forge-secret",
		"GH_TOKEN=forge-secret",
		"OPENAI_API_KEY=provider-secret",
		"CODEX_API_KEY=provider-secret",
		"ANTHROPIC_API_KEY=provider-secret",
		"LOOM_FLEET_DB_API_KEY=internal-secret",
		"LOOM_FLEET_DB_URL=http://fleet",
		"LOOM_AGENT_LEASE_TOKEN=standing-secret",
		"LOOM_DAEMON_SOCKET=/tmp/daemon.sock",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"GIT_CONFIG_COUNT=1",
		"AWS_ACCESS_KEY_ID=cloud-secret",
		"RANDOM_UNSCOPED=value",
	}
	got := FilterChildBaseEnv(input)
	joined := strings.Join(got, "\n")
	for _, wanted := range []string{
		"PATH=/usr/bin", "HOME=/tmp/home", "CODEX_HOME=/tmp/codex",
		"CLAUDE_CONFIG_DIR=/tmp/claude", "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8",
	} {
		if !strings.Contains(joined, wanted) {
			t.Errorf("filtered env missing %q: %q", wanted, joined)
		}
	}
	for _, forbidden := range []string{
		"forge-secret", "provider-secret", "internal-secret", "standing-secret",
		"LOOM_FLEET_DB_URL", "LOOM_DAEMON_SOCKET", "SSH_AUTH_SOCK",
		"GIT_CONFIG_COUNT", "AWS_ACCESS_KEY_ID", "RANDOM_UNSCOPED",
	} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("filtered env kept %q: %q", forbidden, joined)
		}
	}
}

func TestChildLaunchOverlayIsExactNotBroadLoomPrefix(t *testing.T) {
	for _, allowed := range []string{
		"LOOM_WORKSPACE", "LOOM_CONFIG_DIR", "LOOM_AGENT_NAME", EnvSessionID, EnvSessionToken, EnvSessionFence,
		EnvInteractionAPIURL,
	} {
		if !ChildLaunchEnvAllowed(allowed) {
			t.Errorf("%s should be allowed", allowed)
		}
	}
	for _, denied := range []string{
		"LOOM_FLEET_DB_API_KEY", "LOOM_DAEMON_SOCKET", "LOOM_ARBITRARY",
		"GITHUB_TOKEN", "OPENAI_API_KEY",
	} {
		if ChildLaunchEnvAllowed(denied) {
			t.Errorf("%s should be denied", denied)
		}
	}
}

func TestSessionEnvelopeCarriesOnlyExactScopeAndFence(t *testing.T) {
	harness := newInteractionHarness(t)
	auth := harness.session(t, ActionOpenTerminal, testTerminal, 9)
	token := NewLeaseToken([]byte("one-time-session-secret"))
	envelope, err := SessionEnvelope(auth, token)
	if err != nil {
		t.Fatalf("SessionEnvelope: %v", err)
	}
	if envelope[EnvSessionWorkspace] != testWorkspace ||
		envelope[EnvSessionID] != testSession ||
		envelope[EnvSessionAgentID] != testAgent ||
		envelope[EnvSessionTerminalID] != testTerminal ||
		envelope[EnvSessionNodeID] != "node-1" ||
		envelope[EnvSessionLeaseID] != "lease-1" ||
		envelope[EnvSessionFence] != "9" ||
		envelope[EnvSessionToken] != "one-time-session-secret" {
		t.Fatalf("session envelope = %+v", envelope)
	}
	if len(envelope) != 8 {
		t.Fatalf("session envelope fields = %d, want 8", len(envelope))
	}
	token.Close()
}
