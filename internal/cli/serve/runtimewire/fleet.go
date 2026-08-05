package runtimewire

import (
	"context"
	"encoding/hex"
	"log/slog"
	"os"

	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// ResolveFleetJWTKey resolves the fleet JWT signing key from environment or
// Redis. Returns the key bytes and the Redis config (nil if Redis is not used).
func ResolveFleetJWTKey(ctx context.Context, redisAddr, redisPassword string) ([]byte, *fleet.RedisConfig) {
	if redisAddr != "" {
		redisCfg := &fleet.RedisConfig{Address: redisAddr, Password: redisPassword}
		key := resolveJWTKeyFromEnvOrRedis(ctx, redisAddr, redisPassword)
		return key, redisCfg
	}
	if envKey := os.Getenv("LOOM_FLEET_JWT_KEY"); envKey != "" {
		return DecodeJWTKeyEnv(envKey), nil
	}
	return nil, nil
}

func resolveJWTKeyFromEnvOrRedis(ctx context.Context, redisAddr, redisPassword string) []byte {
	if envKey := os.Getenv("LOOM_FLEET_JWT_KEY"); envKey != "" {
		return DecodeJWTKeyEnv(envKey)
	}
	redisClient := fleet.NewRedisClient(redisAddr, redisPassword, 0)
	mgr := fleet.NewSigningKeyManager(redisClient, nil)
	key, err := mgr.GetOrCreateSigningKey(ctx)
	_ = redisClient.Close()
	if err != nil {
		slog.Warn("failed to provision JWT signing key from Redis", "err", err)
		slog.Warn("Fleet auth will use an ephemeral key; tokens will not validate on other servers")
		return nil
	}
	slog.Info("JWT signing key provisioned from Redis")
	return key
}

// DecodeJWTKeyEnv decodes a hex-encoded JWT key from the environment.
func DecodeJWTKeyEnv(envKey string) []byte {
	decoded, err := hex.DecodeString(envKey)
	if err != nil || len(decoded) < 32 {
		slog.Error("LOOM_FLEET_JWT_KEY must be a hex-encoded string of at least 32 bytes")
		os.Exit(1)
	}
	slog.Info("Using JWT signing key from LOOM_FLEET_JWT_KEY environment variable")
	return decoded
}

type FleetRedisConfig = fleet.RedisConfig
