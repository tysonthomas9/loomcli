package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newKeyManager returns a SigningKeyManager backed by the given miniredis addr.
func newKeyManager(t *testing.T, addr string) *SigningKeyManager {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewSigningKeyManager(rdb, nil)
}

// TestMintTaskRunToken_CrossProcessKeySharing is the core Phase-C proof: a token
// minted by one process validates in another, because both read the same shared
// signing key from Redis — so the daemon/supervisor can mint and loom serve can
// validate without distributing a key out of band.
func TestMintTaskRunToken_CrossProcessKeySharing(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)

	// "serve" creates the shared key at startup.
	serve := newKeyManager(t, mr.Addr())
	if _, err := serve.GetOrCreateSigningKey(ctx); err != nil {
		t.Fatalf("create signing key: %v", err)
	}

	// "daemon" is a separate manager (separate client) on the same Redis.
	daemon := newKeyManager(t, mr.Addr())

	claims := TaskRunClaims{Workspace: "DEMO", TaskID: "DEMO-1", SessionID: "sess-1", FencingToken: 4}
	tok, err := daemon.MintTaskRunToken(ctx, claims, time.Hour)
	if err != nil {
		t.Fatalf("daemon mint: %v", err)
	}

	got, err := serve.ValidateTaskRunTokenFromStore(ctx, tok)
	if err != nil {
		t.Fatalf("serve validate: %v", err)
	}
	if got.SessionID != "sess-1" || got.TaskID != "DEMO-1" || got.FencingToken != 4 {
		t.Errorf("claims round-trip mismatch across processes: %+v", got)
	}
}

// TestMintTaskRunToken_RequiresExistingKey ensures minting fails clearly when
// no signing key has been created yet (serve hasn't started).
func TestMintTaskRunToken_RequiresExistingKey(t *testing.T) {
	mr := miniredis.RunT(t)
	mgr := newKeyManager(t, mr.Addr())
	if _, err := mgr.MintTaskRunToken(context.Background(), TaskRunClaims{Workspace: "W", SessionID: "s"}, time.Hour); err == nil {
		t.Fatal("expected mint to fail when no signing key exists")
	}
}

// TestValidateTaskRunTokenFromStore_RotationGrace verifies a token minted under
// the previous key still validates after a rotation (grace period).
func TestValidateTaskRunTokenFromStore_RotationGrace(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	mgr := newKeyManager(t, mr.Addr())
	if _, err := mgr.GetOrCreateSigningKey(ctx); err != nil {
		t.Fatalf("create signing key: %v", err)
	}

	tok, err := mgr.MintTaskRunToken(ctx, TaskRunClaims{Workspace: "DEMO", SessionID: "sess-1"}, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	if _, err := mgr.RotateSigningKey(ctx); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Still valid via the previous-key fallback.
	if _, err := mgr.ValidateTaskRunTokenFromStore(ctx, tok); err != nil {
		t.Errorf("token minted before rotation should still validate during grace: %v", err)
	}
}
