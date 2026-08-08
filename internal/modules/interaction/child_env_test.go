package interaction

import "testing"

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
