package realtime

import (
	"strings"
	"testing"
	"time"
)

func newTestTerminalAuth(t *testing.T) *TerminalAuth {
	t.Helper()
	ta, err := NewTerminalAuth()
	if err != nil {
		t.Fatalf("NewTerminalAuth: %v", err)
	}
	t.Cleanup(ta.Stop)
	return ta
}

func TestTerminalAuth_GenerateAndValidate(t *testing.T) {
	ta := newTestTerminalAuth(t)

	token, err := ta.GenerateToken("sess-1", "user-1")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	uid, err := ta.ValidateToken(token, "sess-1")
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if uid != "user-1" {
		t.Errorf("expected user-1, got %s", uid)
	}
}

func TestTerminalAuth_SessionBinding(t *testing.T) {
	ta := newTestTerminalAuth(t)

	token, err := ta.GenerateToken("sess-1", "user-1")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	_, err = ta.ValidateToken(token, "sess-2")
	if err == nil {
		t.Fatal("expected error for session mismatch")
	}
	if !strings.Contains(err.Error(), "session mismatch") {
		t.Errorf("expected 'session mismatch', got: %v", err)
	}
}

func TestTerminalAuth_SingleUse(t *testing.T) {
	ta := newTestTerminalAuth(t)

	token, err := ta.GenerateToken("sess-1", "user-1")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// First use
	if _, err := ta.ValidateToken(token, "sess-1"); err != nil {
		t.Fatalf("first ValidateToken: %v", err)
	}

	// Second use
	_, err = ta.ValidateToken(token, "sess-1")
	if err == nil {
		t.Fatal("expected error on second use")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Errorf("expected 'already used', got: %v", err)
	}
}

func TestTerminalAuth_EmptyUserID(t *testing.T) {
	ta := newTestTerminalAuth(t)

	// Empty userID is allowed (open mode)
	token, err := ta.GenerateToken("sess-1", "")
	if err != nil {
		t.Fatalf("GenerateToken with empty userID: %v", err)
	}

	uid, err := ta.ValidateToken(token, "sess-1")
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if uid != "" {
		t.Errorf("expected empty userID, got %q", uid)
	}
}

func TestTerminalAuth_Tampering(t *testing.T) {
	ta := newTestTerminalAuth(t)

	token, err := ta.GenerateToken("sess-1", "user-1")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	tampered := token + "X"
	_, err = ta.ValidateToken(tampered, "sess-1")
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestTerminalAuth_MalformedToken(t *testing.T) {
	ta := newTestTerminalAuth(t)

	_, err := ta.ValidateToken("garbage", "sess-1")
	if err == nil {
		t.Fatal("expected error for malformed token")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("expected 'malformed', got: %v", err)
	}
}

func TestTerminalAuth_UniqueTokens(t *testing.T) {
	ta := newTestTerminalAuth(t)

	t1, _ := ta.GenerateToken("sess-1", "user-1")
	t2, _ := ta.GenerateToken("sess-1", "user-1")
	if t1 == t2 {
		t.Error("expected different tokens (different nonces)")
	}
}

func TestTerminalAuth_StopIdempotent(t *testing.T) {
	ta := newTestTerminalAuth(t)
	ta.Stop()
	ta.Stop()
}

func TestTerminalAuth_Cleanup(t *testing.T) {
	ta := newTestTerminalAuth(t)

	token, _ := ta.GenerateToken("sess-1", "user-1")
	ta.ValidateToken(token, "sess-1")

	ta.mu.Lock()
	usedCount := len(ta.used)
	ta.mu.Unlock()
	if usedCount != 1 {
		t.Fatalf("expected 1 used nonce, got %d", usedCount)
	}

	// Backdate nonce to trigger cleanup
	ta.mu.Lock()
	for k := range ta.used {
		ta.used[k] = time.Now().Add(-3 * time.Minute)
	}
	ta.mu.Unlock()

	ta.cleanup()

	ta.mu.Lock()
	usedCount = len(ta.used)
	ta.mu.Unlock()
	if usedCount != 0 {
		t.Errorf("expected 0 used nonces after cleanup, got %d", usedCount)
	}
}
