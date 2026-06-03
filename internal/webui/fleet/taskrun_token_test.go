package fleet

import (
	"testing"
	"time"
)

// reuses testSigningKey from jwt_test.go (same package).

func sampleTaskRunClaims() TaskRunClaims {
	return TaskRunClaims{
		Workspace:    "DEMO",
		TaskID:       "DEMO-1",
		SessionID:    "sess-1",
		FencingToken: 7,
	}
}

func TestTaskRunToken_RoundTrip(t *testing.T) {
	tok, err := GenerateTaskRunToken(sampleTaskRunClaims(), testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	got, err := ValidateTaskRunToken(tok, testSigningKey)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.Workspace != "DEMO" || got.TaskID != "DEMO-1" || got.SessionID != "sess-1" || got.FencingToken != 7 {
		t.Errorf("claims round-trip mismatch: %+v", got)
	}
	if got.Subject != "sess-1" {
		t.Errorf("subject = %q, want sess-1", got.Subject)
	}
}

func TestTaskRunToken_DefaultScopesApplied(t *testing.T) {
	tok, err := GenerateTaskRunToken(sampleTaskRunClaims(), testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := ValidateTaskRunToken(tok, testSigningKey)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, want := range DefaultTaskRunScopes {
		if !got.HasScope(want) {
			t.Errorf("default token missing scope %q (scopes=%v)", want, got.Scopes)
		}
	}
	// Least privilege: no claim/admin scope leaks in.
	if got.HasScope("fleet:claim") || got.HasScope("admin") {
		t.Errorf("token unexpectedly carries an elevated scope: %v", got.Scopes)
	}
}

func TestTaskRunToken_CustomScopesPreserved(t *testing.T) {
	c := sampleTaskRunClaims()
	c.Scopes = []string{ScopeTaskRead}
	tok, err := GenerateTaskRunToken(c, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := ValidateTaskRunToken(tok, testSigningKey)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !got.HasScope(ScopeTaskRead) || got.HasScope(ScopeSessionWrite) {
		t.Errorf("custom scopes not preserved: %v", got.Scopes)
	}
}

func TestTaskRunToken_RejectsExpired(t *testing.T) {
	// Negative expiry → already expired.
	tok, err := GenerateTaskRunToken(sampleTaskRunClaims(), testSigningKey, -time.Minute)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := ValidateTaskRunToken(tok, testSigningKey); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestTaskRunToken_RejectsWrongKey(t *testing.T) {
	tok, err := GenerateTaskRunToken(sampleTaskRunClaims(), testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := ValidateTaskRunToken(tok, []byte("a-different-signing-key-entirely!")); err == nil {
		t.Fatal("expected token signed with a different key to be rejected")
	}
}

func TestTaskRunToken_GenerateValidation(t *testing.T) {
	if _, err := GenerateTaskRunToken(sampleTaskRunClaims(), nil, time.Hour); err == nil {
		t.Error("expected error for empty signing key")
	}
	missingSession := sampleTaskRunClaims()
	missingSession.SessionID = ""
	if _, err := GenerateTaskRunToken(missingSession, testSigningKey, time.Hour); err == nil {
		t.Error("expected error for missing session id")
	}
	missingWorkspace := sampleTaskRunClaims()
	missingWorkspace.Workspace = ""
	if _, err := GenerateTaskRunToken(missingWorkspace, testSigningKey, time.Hour); err == nil {
		t.Error("expected error for missing workspace")
	}
}

func TestTaskRunClaims_Authorization(t *testing.T) {
	c := sampleTaskRunClaims()

	// Bound session/task → authorized.
	if !c.AuthorizesSession("DEMO", "sess-1") {
		t.Error("should authorize its own workspace+session")
	}
	if !c.AuthorizesTask("DEMO", "DEMO-1") {
		t.Error("should authorize its own workspace+task")
	}

	// Cross-task / cross-session / cross-workspace → denied (the 403 path).
	if c.AuthorizesSession("DEMO", "sess-2") {
		t.Error("must not authorize a different session")
	}
	if c.AuthorizesTask("DEMO", "DEMO-2") {
		t.Error("must not authorize a different task")
	}
	if c.AuthorizesSession("OTHER", "sess-1") {
		t.Error("must not authorize a different workspace")
	}

	// Empty fields must never accidentally authorize.
	empty := &TaskRunClaims{}
	if empty.AuthorizesSession("", "") || empty.AuthorizesTask("", "") {
		t.Error("empty claims must not authorize anything")
	}
}

func TestTaskRunClaims_FencedOut(t *testing.T) {
	c := sampleTaskRunClaims() // FencingToken = 7

	if !c.FencedOut(8) {
		t.Error("a lower token than the current holder must be fenced out (stale)")
	}
	if c.FencedOut(7) {
		t.Error("the current holder (equal token) must not be fenced out")
	}
	if c.FencedOut(6) {
		t.Error("a higher token must not be fenced out")
	}
}
