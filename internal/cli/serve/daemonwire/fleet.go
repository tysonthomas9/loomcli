package daemonwire

import (
	"context"
	"encoding/hex"
	"log"
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
		log.Printf("Warning: failed to provision JWT signing key from Redis: %v", err)
		log.Printf("Fleet auth will use an ephemeral key (tokens won't validate on other servers)")
		return nil
	}
	log.Printf("JWT signing key provisioned from Redis")
	return key
}

// DecodeJWTKeyEnv decodes a hex-encoded JWT key from the environment.
// Fatals if the key is invalid or too short.
func DecodeJWTKeyEnv(envKey string) []byte {
	decoded, err := hex.DecodeString(envKey)
	if err != nil || len(decoded) < 32 {
		log.Fatalf("LOOM_FLEET_JWT_KEY must be a hex-encoded string of at least 32 bytes")
	}
	log.Printf("Using JWT signing key from LOOM_FLEET_JWT_KEY environment variable")
	return decoded
}

// FleetRedisConfig re-exports fleet.RedisConfig so that the serve package
// can refer to it without importing webui/fleet directly.
type FleetRedisConfig = fleet.RedisConfig
