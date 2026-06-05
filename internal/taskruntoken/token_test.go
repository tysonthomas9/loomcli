package taskruntoken

import (
	"testing"
	"time"
)

var testSigningKey = []byte("test-secret-key-for-jwt-signing!")

func sampleClaims() Claims {
	return Claims{Workspace: "DEMO", TaskID: "DEMO-1", SessionID: "sess-1", FencingToken: 7}
}

func TestToken_RoundTrip(t *testing.T) {
	tok, err := Generate(sampleClaims(), testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	got, err := Validate(tok, testSigningKey)
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

func TestToken_DefaultScopesApplied(t *testing.T) {
	tok, _ := Generate(sampleClaims(), testSigningKey, time.Hour)
	got, err := Validate(tok, testSigningKey)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, want := range DefaultScopes {
		if !got.HasScope(want) {
			t.Errorf("default token missing scope %q (scopes=%v)", want, got.Scopes)
		}
	}
	if got.HasScope("fleet:claim") || got.HasScope("admin") {
		t.Errorf("token unexpectedly carries an elevated scope: %v", got.Scopes)
	}
}

func TestToken_CustomScopesPreserved(t *testing.T) {
	c := sampleClaims()
	c.Scopes = []string{ScopeTaskRead}
	tok, _ := Generate(c, testSigningKey, time.Hour)
	got, err := Validate(tok, testSigningKey)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !got.HasScope(ScopeTaskRead) || got.HasScope(ScopeSessionWrite) {
		t.Errorf("custom scopes not preserved: %v", got.Scopes)
	}
}

func TestToken_RejectsExpired(t *testing.T) {
	tok, _ := Generate(sampleClaims(), testSigningKey, -time.Minute)
	if _, err := Validate(tok, testSigningKey); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestToken_RejectsWrongKey(t *testing.T) {
	tok, _ := Generate(sampleClaims(), testSigningKey, time.Hour)
	if _, err := Validate(tok, []byte("a-different-signing-key-entirely!")); err == nil {
		t.Fatal("expected token signed with a different key to be rejected")
	}
}

func TestToken_GenerateValidation(t *testing.T) {
	if _, err := Generate(sampleClaims(), nil, time.Hour); err == nil {
		t.Error("expected error for empty signing key")
	}
	missingSession := sampleClaims()
	missingSession.SessionID = ""
	if _, err := Generate(missingSession, testSigningKey, time.Hour); err == nil {
		t.Error("expected error for missing session id")
	}
	missingWorkspace := sampleClaims()
	missingWorkspace.Workspace = ""
	if _, err := Generate(missingWorkspace, testSigningKey, time.Hour); err == nil {
		t.Error("expected error for missing workspace")
	}
}

func TestClaims_Authorization(t *testing.T) {
	c := sampleClaims()
	if !c.AuthorizesSession("DEMO", "sess-1") || !c.AuthorizesTask("DEMO", "DEMO-1") {
		t.Error("should authorize its own workspace+session/task")
	}
	if c.AuthorizesSession("DEMO", "sess-2") || c.AuthorizesTask("DEMO", "DEMO-2") || c.AuthorizesSession("OTHER", "sess-1") {
		t.Error("must not authorize a different session/task/workspace")
	}
	empty := &Claims{}
	if empty.AuthorizesSession("", "") || empty.AuthorizesTask("", "") {
		t.Error("empty claims must not authorize anything")
	}
}

func TestClaims_FencedOut(t *testing.T) {
	c := sampleClaims() // FencingToken = 7
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
