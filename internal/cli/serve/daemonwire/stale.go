package daemonwire

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/kv"
)

// InitStaleDetectorHandler starts a KV-backed stale detector if a Redis address
// is provided and returns an HTTP handler for the stale detector status endpoint.
// If Redis is unconfigured, returns a handler that reports disabled status.
func InitStaleDetectorHandler(ctx context.Context, redisAddr, redisPassword string) http.HandlerFunc {
	if redisAddr == "" {
		return handleStaleDetectorDisabled
	}

	kvClient := kv.NewClient(redisAddr, redisPassword, 0)

	breaker := circuitbreaker.NewBreaker("redis-stale-detector", circuitbreaker.Config{
		FailureThreshold: 5,
		OpenTimeout:      30 * time.Second,
		ShouldTrip:       kv.RedisShouldTrip,
	})
	kvClient.SetCircuitBreaker(breaker)

	cfg := kv.DefaultStaleDetectorConfig()
	serverID := kv.GenerateServerID()
	reconciler := kv.NewReconciler("")

	detector := kv.NewStaleDetector(kvClient, cfg, serverID, reconciler)

	go func() {
		if err := detector.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("Stale detector error: %v", err)
		}
	}()
	log.Printf("Stale detector enabled (redis=%s, server=%s)", redisAddr, serverID)

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(detector.Status())
	}
}

func handleStaleDetectorDisabled(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(kv.StaleDetectorStatus{Enabled: false})
}
