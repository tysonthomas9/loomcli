package pushproxy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestTokenClaimsExpiryAndFence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lease, err := store.ClaimLease(ctx, LeaseScope("w", "a"), "runner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := Mint(ctx, store, "/repo", "w", "a", lease.Fence)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Verify(ctx, store, token)
	if err != nil || claims.Repo != "/repo" || claims.Workspace != "w" || claims.Attempt != "a" || claims.Fence != lease.Fence {
		t.Fatalf("claims: %+v %v", claims, err)
	}
	reopened, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := Verify(ctx, reopened, token); err != nil {
		t.Fatalf("stored host key did not survive reopen: %v", err)
	}
	key, err := store.ProxyKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(c Claims) string {
		payload, _ := json.Marshal(c)
		part := base64.RawURLEncoding.EncodeToString(payload)
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(part))
		return part + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	expired := claims
	expired.Expires = time.Now().Add(-time.Second).Unix()
	if _, err := Verify(ctx, store, sign(expired)); err == nil {
		t.Fatal("expired token accepted")
	}
	wrong := claims
	wrong.Attempt = "other"
	if _, err := Verify(ctx, store, sign(wrong)); err == nil {
		t.Fatal("wrong attempt accepted")
	}
	if err := store.ReleaseLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimLease(ctx, LeaseScope("w", "a"), "replacement", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(ctx, store, token); err == nil {
		t.Fatal("stale fence accepted")
	}
}
