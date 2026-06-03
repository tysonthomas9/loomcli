package supervisor

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// TestAppendSessionEnv_MintsTaskRunToken verifies that when a signing key is
// configured, appendSessionEnv injects a LOOM_TASKRUN_TOKEN bound to the
// session + lease fencing token, and that it validates with the same key.
func TestAppendSessionEnv_MintsTaskRunToken(t *testing.T) {
	key, _ := hex.DecodeString(strings.Repeat("ab", 32)) // 32-byte hex key
	s := &Supervisor{WorkspaceID: "WS1", TaskRunSigningKey: key}
	ap := &AgentProcess{}
	ap.Session = &sessions.Session{Meta: sessions.SessionMetadata{SessionRecord: sessions.SessionRecord{SessionID: "sess-mint-1"}}}
	ap.AgentSessionID = "sess-mint-1"
	ap.AgentLeaseFencingToken = 5

	env := s.appendSessionEnv(nil, ap)

	var token string
	for _, e := range env {
		if strings.HasPrefix(e, "LOOM_TASKRUN_TOKEN=") {
			token = strings.TrimPrefix(e, "LOOM_TASKRUN_TOKEN=")
		}
	}
	if token == "" {
		t.Fatalf("LOOM_TASKRUN_TOKEN not injected; env=%v", env)
	}
	claims, err := fleet.ValidateTaskRunToken(token, key)
	if err != nil {
		t.Fatalf("minted token failed to validate: %v", err)
	}
	if claims.Workspace != "WS1" || claims.SessionID != "sess-mint-1" || claims.FencingToken != 5 {
		t.Errorf("claims mismatch: %+v", claims)
	}

	// No signing key → no token minted.
	noKey := (&Supervisor{WorkspaceID: "WS1"}).appendSessionEnv(nil, ap)
	for _, e := range noKey {
		if strings.HasPrefix(e, "LOOM_TASKRUN_TOKEN=") {
			t.Error("token minted without a signing key")
		}
	}
}
