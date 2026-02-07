package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/redis/go-redis/v9"
)

const (
	// signingKeyPrefix namespaces all JWT signing key Redis keys.
	signingKeyPrefix = keyPrefix + "jwt-signing-key:"

	// signingKeySize is the size of the signing key in bytes (256 bits for HMAC-SHA256).
	signingKeySize = 32
)

// Redis key builders for signing keys.
func signingKeyVersionKey(version int) string {
	return signingKeyPrefix + "v" + strconv.Itoa(version)
}

func currentVersionKey() string {
	return signingKeyPrefix + "current-version"
}

// SigningKeyManager manages shared JWT signing keys in Redis for multi-server deployments.
// It uses SET NX semantics so the first server generates the key and subsequent servers reuse it.
type SigningKeyManager struct {
	client *redis.Client
	logger *slog.Logger
}

// NewSigningKeyManager creates a new SigningKeyManager using the given Redis client.
func NewSigningKeyManager(client *redis.Client, logger *slog.Logger) *SigningKeyManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &SigningKeyManager{
		client: client,
		logger: logger,
	}
}

// GetOrCreateSigningKey atomically gets or creates the JWT signing key.
// If no key exists in Redis, a 32-byte cryptographically random key is generated
// and stored at version 1 using SET NX. If a key already exists, it is returned.
// This is safe for concurrent calls from multiple servers.
func (m *SigningKeyManager) GetOrCreateSigningKey(ctx context.Context) ([]byte, error) {
	// Generate the key material first (before touching Redis) so that
	// if crypto/rand fails we haven't modified any Redis state.
	key, err := generateRandomKey()
	if err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}

	encoded := hex.EncodeToString(key)

	// Use a Lua script to atomically create both the version pointer and key.
	// This avoids the race conditions of a multi-step SetNX approach where
	// the version pointer could be set but the key storage fails.
	result, err := getOrCreateLuaScript.Run(ctx, m.client,
		[]string{currentVersionKey(), signingKeyVersionKey(1)},
		encoded,
	).Text()
	if err != nil {
		return nil, fmt.Errorf("get-or-create signing key: %w", err)
	}

	if result == "created" {
		m.logger.Info("Generated new JWT signing key in Redis", "version", 1)
		return key, nil
	}

	// Another server already created the key — read the current version
	version, err := m.getCurrentVersion(ctx)
	if err != nil {
		return nil, err
	}
	return m.readKeyVersion(ctx, version)
}

// getOrCreateLuaScript atomically creates both the version pointer and v1 key.
// KEYS[1] = current-version key
// KEYS[2] = v1 key
// ARGV[1] = hex-encoded key
// Returns: "created" if new key was stored, "exists" if key already existed.
var getOrCreateLuaScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current ~= false then
    return 'exists'
end
redis.call('SET', KEYS[1], '1')
redis.call('SET', KEYS[2], ARGV[1])
return 'created'
`)

// GetCurrentSigningKey returns the current signing key from Redis.
// Returns an error if no signing key has been created yet.
func (m *SigningKeyManager) GetCurrentSigningKey(ctx context.Context) ([]byte, error) {
	version, err := m.getCurrentVersion(ctx)
	if err != nil {
		return nil, err
	}
	return m.readKeyVersion(ctx, version)
}

// RotateSigningKey generates a new signing key at the next version and atomically
// updates the current-version pointer. Returns the new version number.
func (m *SigningKeyManager) RotateSigningKey(ctx context.Context) (int, error) {
	key, err := generateRandomKey()
	if err != nil {
		return 0, fmt.Errorf("generate new signing key: %w", err)
	}

	encoded := hex.EncodeToString(key)

	// Use a Lua script to atomically increment version and store the new key.
	// This prevents race conditions between reading the version and writing the new key.
	result, err := rotateLuaScript.Run(ctx, m.client,
		[]string{currentVersionKey()},
		signingKeyPrefix+"v", encoded,
	).Int()
	if err != nil {
		return 0, fmt.Errorf("rotate signing key: %w", err)
	}

	m.logger.Info("Rotated JWT signing key", "new_version", result)
	return result, nil
}

// rotateLuaScript atomically increments the version and stores the new key.
// KEYS[1] = current-version key
// ARGV[1] = key prefix (e.g., "fleet:jwt-signing-key:v")
// ARGV[2] = hex-encoded new key
// Returns: new version number
var rotateLuaScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current == false then
    return redis.error_reply('no signing key exists to rotate')
end
local new_version = tonumber(current) + 1
local key_name = ARGV[1] .. tostring(new_version)
redis.call('SET', key_name, ARGV[2])
redis.call('SET', KEYS[1], tostring(new_version))
return new_version
`)

// GetSigningKeyByVersion retrieves the signing key for a specific version.
func (m *SigningKeyManager) GetSigningKeyByVersion(ctx context.Context, version int) ([]byte, error) {
	return m.readKeyVersion(ctx, version)
}

// GetCurrentAndPreviousKeys returns the current signing key and the previous version key
// (if one exists). The previous key is nil if there is no prior version (i.e., current is v1).
// This is used during token validation to accept tokens signed with the previous key
// during a key rotation grace period.
func (m *SigningKeyManager) GetCurrentAndPreviousKeys(ctx context.Context) (current []byte, previous []byte, err error) {
	version, err := m.getCurrentVersion(ctx)
	if err != nil {
		return nil, nil, err
	}

	current, err = m.readKeyVersion(ctx, version)
	if err != nil {
		return nil, nil, fmt.Errorf("read current key (v%d): %w", version, err)
	}

	if version > 1 {
		previous, err = m.readKeyVersion(ctx, version-1)
		if err != nil {
			// Previous key may have been cleaned up; log but don't fail
			m.logger.Warn("Could not read previous signing key", "version", version-1, "error", err)
			previous = nil
		}
	}

	return current, previous, nil
}

// getCurrentVersion reads the current version number from Redis.
func (m *SigningKeyManager) getCurrentVersion(ctx context.Context) (int, error) {
	val, err := m.client.Get(ctx, currentVersionKey()).Result()
	if err == redis.Nil {
		return 0, fmt.Errorf("no signing key exists in Redis")
	}
	if err != nil {
		return 0, fmt.Errorf("read current version: %w", err)
	}

	version, err := strconv.Atoi(val)
	if err != nil || version < 1 {
		return 0, fmt.Errorf("invalid version in Redis: %q", val)
	}
	return version, nil
}

// readKeyVersion reads and decodes a signing key at a specific version.
func (m *SigningKeyManager) readKeyVersion(ctx context.Context, version int) ([]byte, error) {
	encoded, err := m.client.Get(ctx, signingKeyVersionKey(version)).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("signing key v%d not found in Redis", version)
	}
	if err != nil {
		return nil, fmt.Errorf("read signing key v%d: %w", version, err)
	}

	if encoded == "" {
		return nil, fmt.Errorf("signing key v%d is empty in Redis", version)
	}

	key, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode signing key v%d: %w", version, err)
	}

	if len(key) != signingKeySize {
		return nil, fmt.Errorf("signing key v%d has invalid size: got %d, want %d", version, len(key), signingKeySize)
	}

	return key, nil
}

// generateRandomKey creates a cryptographically random key of signingKeySize bytes.
func generateRandomKey() ([]byte, error) {
	key := make([]byte, signingKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("crypto/rand: %w", err)
	}
	return key, nil
}
