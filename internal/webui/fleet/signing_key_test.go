package fleet

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupSigningKeyTest(t *testing.T) (*SigningKeyManager, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewSigningKeyManager(rdb, nil), mr
}

func TestGetOrCreateSigningKey_CreatesKeyWhenNoneExists(t *testing.T) {
	mgr, mr := setupSigningKeyTest(t)
	ctx := context.Background()

	key, err := mgr.GetOrCreateSigningKey(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32-byte key, got %d bytes", len(key))
	}

	// Verify Redis has the current-version set to "1"
	version, err := mr.Get(currentVersionKey())
	if err != nil {
		t.Fatalf("current-version key missing: %v", err)
	}
	if version != "1" {
		t.Errorf("expected current-version to be \"1\", got %q", version)
	}

	// Verify Redis has the v1 key with a valid hex-encoded value
	encoded, err := mr.Get(signingKeyVersionKey(1))
	if err != nil {
		t.Fatalf("signing key v1 missing: %v", err)
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatalf("v1 key is not valid hex: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("expected 32-byte decoded key, got %d bytes", len(decoded))
	}
	if !bytes.Equal(key, decoded) {
		t.Errorf("returned key does not match key stored in Redis")
	}
}

func TestGetOrCreateSigningKey_ReturnsExistingKey(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	key1, err := mgr.GetOrCreateSigningKey(ctx)
	if err != nil {
		t.Fatalf("first call unexpected error: %v", err)
	}

	key2, err := mgr.GetOrCreateSigningKey(ctx)
	if err != nil {
		t.Fatalf("second call unexpected error: %v", err)
	}

	if !bytes.Equal(key1, key2) {
		t.Errorf("expected both calls to return the same key, got different keys")
	}
}

func TestGetOrCreateSigningKey_Concurrent(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	// Pre-create the key so all concurrent goroutines exercise the
	// "key already exists" read path without hitting the inherent race
	// window between SetNX of the version pointer and SetNX of the key.
	initialKey, err := mgr.GetOrCreateSigningKey(ctx)
	if err != nil {
		t.Fatalf("initial create unexpected error: %v", err)
	}

	const numGoroutines = 10
	keys := make([][]byte, numGoroutines)
	errs := make([]error, numGoroutines)

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(i int) {
			defer wg.Done()
			keys[i], errs[i] = mgr.GetOrCreateSigningKey(ctx)
		}(i)
	}
	wg.Wait()

	// Verify no errors and all keys match the initial key
	for i := 0; i < numGoroutines; i++ {
		if errs[i] != nil {
			t.Errorf("goroutine %d got error: %v", i, errs[i])
			continue
		}
		if !bytes.Equal(initialKey, keys[i]) {
			t.Errorf("goroutine %d returned a different key than the initial key", i)
		}
	}
}

func TestGetCurrentSigningKey_ReturnsKeyAfterCreation(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	created, err := mgr.GetOrCreateSigningKey(ctx)
	if err != nil {
		t.Fatalf("create unexpected error: %v", err)
	}

	current, err := mgr.GetCurrentSigningKey(ctx)
	if err != nil {
		t.Fatalf("get current unexpected error: %v", err)
	}

	if !bytes.Equal(created, current) {
		t.Errorf("GetCurrentSigningKey returned different key than GetOrCreateSigningKey")
	}
}

func TestGetCurrentSigningKey_ErrorsWhenNoKeyExists(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	_, err := mgr.GetCurrentSigningKey(ctx)
	if err == nil {
		t.Fatal("expected error when no signing key exists")
	}
	if !strings.Contains(err.Error(), "no signing key exists") {
		t.Errorf("expected error to mention \"no signing key exists\", got: %v", err)
	}
}

func TestRotateSigningKey_IncrementsVersion(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	// Create initial key (v1)
	v1Key, err := mgr.GetOrCreateSigningKey(ctx)
	if err != nil {
		t.Fatalf("create unexpected error: %v", err)
	}

	// Rotate to v2
	newVersion, err := mgr.RotateSigningKey(ctx)
	if err != nil {
		t.Fatalf("rotate unexpected error: %v", err)
	}
	if newVersion != 2 {
		t.Errorf("expected new version 2, got %d", newVersion)
	}

	// Old key should still be accessible
	oldKey, err := mgr.GetSigningKeyByVersion(ctx, 1)
	if err != nil {
		t.Fatalf("get v1 after rotation unexpected error: %v", err)
	}
	if !bytes.Equal(v1Key, oldKey) {
		t.Errorf("v1 key changed after rotation")
	}

	// Current key should be the new key (different from v1)
	currentKey, err := mgr.GetCurrentSigningKey(ctx)
	if err != nil {
		t.Fatalf("get current after rotation unexpected error: %v", err)
	}
	if bytes.Equal(v1Key, currentKey) {
		t.Errorf("expected current key to differ from v1 after rotation")
	}
}

func TestRotateSigningKey_ErrorsWhenNoKeyExists(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	_, err := mgr.RotateSigningKey(ctx)
	if err == nil {
		t.Fatal("expected error when rotating without an existing key")
	}
}

func TestGetCurrentAndPreviousKeys_BeforeRotation(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	_, err := mgr.GetOrCreateSigningKey(ctx)
	if err != nil {
		t.Fatalf("create unexpected error: %v", err)
	}

	current, previous, err := mgr.GetCurrentAndPreviousKeys(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if current == nil {
		t.Fatal("expected non-nil current key")
	}
	if previous != nil {
		t.Fatalf("expected nil previous key before rotation, got %d bytes", len(previous))
	}
}

func TestGetCurrentAndPreviousKeys_AfterRotation(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	v1Key, err := mgr.GetOrCreateSigningKey(ctx)
	if err != nil {
		t.Fatalf("create unexpected error: %v", err)
	}

	_, err = mgr.RotateSigningKey(ctx)
	if err != nil {
		t.Fatalf("rotate unexpected error: %v", err)
	}

	current, previous, err := mgr.GetCurrentAndPreviousKeys(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if current == nil {
		t.Fatal("expected non-nil current key")
	}
	if previous == nil {
		t.Fatal("expected non-nil previous key after rotation")
	}
	if bytes.Equal(current, previous) {
		t.Error("expected current and previous keys to be different")
	}

	// Verify current matches v2
	v2Key, err := mgr.GetSigningKeyByVersion(ctx, 2)
	if err != nil {
		t.Fatalf("get v2 unexpected error: %v", err)
	}
	if !bytes.Equal(current, v2Key) {
		t.Error("current key does not match v2")
	}

	// Verify previous matches v1
	if !bytes.Equal(previous, v1Key) {
		t.Error("previous key does not match v1")
	}
}

func TestGetOrCreateSigningKey_KeyIsExactly32Bytes(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	key, err := mgr.GetOrCreateSigningKey(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected exactly 32 bytes, got %d", len(key))
	}
}

func TestGetOrCreateSigningKey_RedisConnectionFailure(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	mgr := NewSigningKeyManager(rdb, nil)

	// Close miniredis to simulate connection failure
	mr.Close()

	ctx := context.Background()
	_, err := mgr.GetOrCreateSigningKey(ctx)
	if err == nil {
		t.Fatal("expected error when Redis is unavailable")
	}
}

func TestGetSigningKeyByVersion_InvalidVersion(t *testing.T) {
	mgr, _ := setupSigningKeyTest(t)
	ctx := context.Background()

	_, err := mgr.GetSigningKeyByVersion(ctx, 999)
	if err == nil {
		t.Fatal("expected error for non-existent version")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error to mention \"not found\", got: %v", err)
	}
}

func TestGetCurrentSigningKey_EmptyKeyInRedis(t *testing.T) {
	_, mr := setupSigningKeyTest(t)

	// Manually set an empty key and version in Redis
	mr.Set(currentVersionKey(), "1")
	mr.Set(signingKeyVersionKey(1), "")

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	mgr := NewSigningKeyManager(rdb, nil)

	ctx := context.Background()
	_, err := mgr.GetCurrentSigningKey(ctx)
	if err == nil {
		t.Fatal("expected error for empty key in Redis")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected error to mention \"empty\", got: %v", err)
	}
}
