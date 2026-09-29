package browserauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func testSigner(t *testing.T) (*Signer, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSigner("k1", priv, "loom", "fleet-db")
	if err != nil {
		t.Fatal(err)
	}
	return s, pub
}

func parseDelegation(t *testing.T, token string, pub ed25519.PublicKey) (jwt.MapClaims, map[string]any) {
	t.Helper()
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return pub, nil },
		jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithAudience("fleet-db"), jwt.WithIssuer("loom"))
	if err != nil {
		t.Fatalf("parse delegation: %v", err)
	}
	return claims, parsed.Header
}

func agentPrincipal() Principal {
	return Principal{Kind: domain.BrowserPrincipalAgentSession, Subject: "agent-session:abs_1", SessionID: "abs_1"}
}

func TestMintAgentDelegationShape(t *testing.T) {
	s, pub := testSigner(t)
	token, err := s.Mint("ws", "lead", agentPrincipal(), domain.BrowserOpCreate)
	if err != nil {
		t.Fatal(err)
	}
	claims, header := parseDelegation(t, token, pub)
	if header["alg"] != "EdDSA" || header["typ"] != "JWT" || header["kid"] != "k1" {
		t.Fatalf("header = %v", header)
	}
	for k, want := range map[string]any{
		"sub": "agent-session:abs_1", "workspace_key": "ws", "owner_agent_id": "lead",
		"principal_kind": "agent_session", "session_id": "abs_1",
	} {
		if claims[k] != want {
			t.Errorf("claim %s = %v, want %v", k, claims[k], want)
		}
	}
	if _, ok := claims["auth_mode"]; ok {
		t.Error("agent delegation must not carry auth_mode")
	}
	ops, _ := claims["operations"].([]any)
	if len(ops) != 1 || ops[0] != "browser:create" {
		t.Errorf("operations = %v", claims["operations"])
	}
	iat, _ := claims.GetIssuedAt()
	exp, _ := claims.GetExpirationTime()
	if life := exp.Sub(iat.Time); life <= 0 || life > MaxDelegationLifetime {
		t.Errorf("lifetime %v outside (0, %v]", life, MaxDelegationLifetime)
	}
	if jti, _ := claims["jti"].(string); jti == "" {
		t.Error("jti missing")
	}
}

func TestMintOperatorDelegationShape(t *testing.T) {
	s, pub := testSigner(t)
	p := Principal{Kind: domain.BrowserPrincipalWorkspaceOperator, Subject: "local-os-user:501",
		AuthMode: domain.BrowserAuthModeLocalDesktop, LocalOperatorSessionID: "los_1"}
	token, err := s.Mint("ws", "lead", p, domain.BrowserOpSelect)
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := parseDelegation(t, token, pub)
	if claims["auth_mode"] != "local_desktop" || claims["local_operator_session_id"] != "los_1" {
		t.Fatalf("claims = %v", claims)
	}
	if _, ok := claims["session_id"]; ok {
		t.Error("operator delegation must not carry session_id")
	}
}

func TestMintRejectsOperatorCreateAndBadShapes(t *testing.T) {
	s, _ := testSigner(t)
	op := Principal{Kind: domain.BrowserPrincipalWorkspaceOperator, Subject: "u", AuthMode: domain.BrowserAuthModeRemoteUser}
	if _, err := s.Mint("ws", "lead", op, domain.BrowserOpCreate); !errors.Is(err, domain.ErrBrowserForbidden) {
		t.Fatalf("operator create err = %v, want forbidden", err)
	}
	cases := []struct {
		name string
		p    Principal
		ops  []string
	}{
		{"no ops", agentPrincipal(), nil},
		{"unknown op", agentPrincipal(), []string{"browser:delete"}},
		{"no subject", Principal{Kind: domain.BrowserPrincipalAgentSession, SessionID: "x"}, []string{domain.BrowserOpList}},
		{"agent with auth mode", Principal{Kind: domain.BrowserPrincipalAgentSession, Subject: "s", SessionID: "x", AuthMode: "remote_user"}, []string{domain.BrowserOpList}},
		{"local without session", Principal{Kind: domain.BrowserPrincipalWorkspaceOperator, Subject: "s", AuthMode: domain.BrowserAuthModeLocalDesktop}, []string{domain.BrowserOpList}},
		{"remote with local id", Principal{Kind: domain.BrowserPrincipalWorkspaceOperator, Subject: "s", AuthMode: domain.BrowserAuthModeRemoteUser, LocalOperatorSessionID: "l"}, []string{domain.BrowserOpList}},
		{"unknown kind", Principal{Kind: "root", Subject: "s"}, []string{domain.BrowserOpList}},
	}
	for _, tc := range cases {
		if _, err := s.Mint("ws", "lead", tc.p, tc.ops...); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
	}
	var nilSigner *Signer
	if _, err := nilSigner.Mint("ws", "lead", agentPrincipal(), domain.BrowserOpList); !errors.Is(err, domain.ErrBrowserUnavailable) {
		t.Fatalf("nil signer err = %v", err)
	}
}

func TestSignerFromEnv(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if s, err := SignerFromEnv(get); s != nil || err != nil {
		t.Fatalf("unset: %v %v", s, err)
	}
	env[EnvSigningKey] = "k=not-base64!"
	if _, err := SignerFromEnv(get); err == nil {
		t.Fatal("malformed key accepted")
	}
	seed := make([]byte, ed25519.SeedSize)
	env[EnvSigningKey] = "kid9=" + base64.RawURLEncoding.EncodeToString(seed)
	env[EnvAudience] = "aud2"
	s, err := SignerFromEnv(get)
	if err != nil || s == nil || s.keyID != "kid9" || s.audience != "aud2" || s.issuer != DefaultIssuer {
		t.Fatalf("signer = %+v err=%v", s, err)
	}
}

func TestLocalKeyPersistsWithPrivateModes(t *testing.T) {
	dir := t.TempDir()
	kp, err := LoadOrCreateLocalKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreateLocalKey(dir)
	if err != nil || again.KeyID != kp.KeyID || !again.Private.Equal(kp.Private) {
		t.Fatalf("key not stable: %v", err)
	}
	fi, _ := os.Stat(LocalKeyPath(dir))
	di, _ := os.Stat(filepath.Dir(LocalKeyPath(dir)))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Fatalf("modes file=%o dir=%o", fi.Mode().Perm(), di.Mode().Perm())
	}
	env := kp.FleetVerifierEnv()
	if len(env) != 3 || !strings.HasPrefix(env[2], EnvFleetPublicKeys+"="+kp.KeyID+"=") {
		t.Fatalf("verifier env = %v", env)
	}
	if err := os.Chmod(LocalKeyPath(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateLocalKey(dir); err == nil {
		t.Fatal("world-readable key accepted")
	}
}

func TestLocalKeyRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := t.TempDir()
	if _, err := LoadOrCreateLocalKey(target); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(LocalKeyPath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(LocalKeyPath(target), LocalKeyPath(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateLocalKey(dir); err == nil {
		t.Fatal("symlinked key accepted")
	}
}

func TestLocalKeyConcurrentCreateAgrees(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	kids := make([]string, 8)
	for i := range kids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			kp, err := LoadOrCreateLocalKey(dir)
			if err != nil {
				t.Error(err)
				return
			}
			kids[i] = kp.KeyID
		}(i)
	}
	wg.Wait()
	for _, k := range kids {
		if k != kids[0] {
			t.Fatalf("racing creators disagree: %v", kids)
		}
	}
}

func TestAgentSessionRegistry(t *testing.T) {
	r := NewAgentSessionRegistry()
	tok, b, err := r.Issue("ws", "lead", "orch", "term-1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(tok)
	if err != nil || got.AgentName != "lead" || got.ID != b.ID {
		t.Fatalf("resolve = %+v %v", got, err)
	}
	if _, err := r.Resolve("garbage"); !errors.Is(err, domain.ErrBrowserUnauthorized) {
		t.Fatalf("garbage err = %v", err)
	}
	// Respawn in the same terminal replaces the binding.
	tok2, _, _ := r.Issue("ws", "lead", "orch", "term-1")
	if _, err := r.Resolve(tok); !errors.Is(err, domain.ErrBrowserUnauthorized) {
		t.Fatal("old binding survived re-issue")
	}
	// Revoking the stale token must not revoke the live respawn.
	if r.RevokeToken(tok) {
		t.Fatal("stale token revoked something")
	}
	if _, err := r.Resolve(tok2); err != nil {
		t.Fatalf("live binding lost: %v", err)
	}
	if !r.RevokeToken(tok2) || r.Len() != 0 {
		t.Fatal("revoke failed")
	}
	tok3, _, _ := r.Issue("ws", "lead", "orch", "term-2")
	if !r.RevokeTerminal("ws", "term-2") {
		t.Fatal("revoke terminal failed")
	}
	if _, err := r.Resolve(tok3); err == nil {
		t.Fatal("revoked terminal still resolves")
	}
}

func TestOperatorSessionRegistryExpiry(t *testing.T) {
	r := NewOperatorSessionRegistry()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r.SetClock(func() time.Time { return now })
	tok, sess, err := r.Issue("ws", 501)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Subject() != "local-os-user:501" {
		t.Fatalf("subject = %s", sess.Subject())
	}
	if _, err := r.Validate(tok, "other"); !errors.Is(err, domain.ErrBrowserForbidden) {
		t.Fatalf("cross-workspace err = %v", err)
	}
	// Activity keeps it alive past one idle window...
	now = now.Add(OperatorIdleTTL - time.Minute)
	if _, err := r.Refresh(tok); err != nil {
		t.Fatal(err)
	}
	now = now.Add(OperatorIdleTTL - time.Minute)
	if _, err := r.Validate(tok, "ws"); err != nil {
		t.Fatal(err)
	}
	// ...but idleness expires it.
	now = now.Add(OperatorIdleTTL)
	if _, err := r.Validate(tok, "ws"); !errors.Is(err, ErrOperatorSessionExpired) {
		t.Fatalf("idle err = %v", err)
	}

	// Absolute deadline wins over constant activity.
	tok, _, _ = r.Issue("ws", 501)
	for elapsed := time.Duration(0); elapsed < OperatorAbsoluteTTL; elapsed += 10 * time.Minute {
		now = now.Add(10 * time.Minute)
		if _, err := r.Refresh(tok); err != nil {
			break
		}
	}
	if _, err := r.Validate(tok, "ws"); !errors.Is(err, domain.ErrBrowserUnauthorized) {
		t.Fatalf("absolute expiry err = %v", err)
	}

	tok, _, _ = r.Issue("ws", 501)
	if !r.Revoke(tok) {
		t.Fatal("revoke failed")
	}
	if _, err := r.Validate(tok, "ws"); !errors.Is(err, domain.ErrBrowserUnauthorized) {
		t.Fatal("revoked session validated")
	}
	tok, _, _ = r.Issue("ws", 501)
	r.RevokeAll()
	if _, err := r.Validate(tok, "ws"); err == nil {
		t.Fatal("RevokeAll left a session")
	}
}
