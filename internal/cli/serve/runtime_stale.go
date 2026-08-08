package serve

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/kv"
)

// initStaleDetectorHandler starts a KV-backed stale detector when Redis is
// configured and otherwise returns a disabled status handler.
func initStaleDetectorHandler(ctx context.Context, redisAddr, redisPassword string) http.HandlerFunc {
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
	detector := kv.NewStaleDetector(kvClient, cfg, serverID)
	go func() {
		if err := detector.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("stale detector stopped", "err", err)
		}
	}()
	slog.Info("stale detector enabled", "redis", redisAddr, "server", serverID)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(detector.Status())
	}
}

func handleStaleDetectorDisabled(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(kv.StaleDetectorStatus{Enabled: false})
}
