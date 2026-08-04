package doctor

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/kv"
)

func checkRedis() CheckResult {
	addr := os.Getenv("LOOM_REDIS_ADDR")
	if addr == "" {
		// Redis not configured -- skip silently
		return CheckResult{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	password := os.Getenv("LOOM_REDIS_PASSWORD")
	client := kv.NewClient(addr, password, 0)
	defer func() { _ = client.Close() }()

	if err := client.Ping(ctx); err != nil {
		return CheckResult{
			Name:    "redis",
			Status:  StatusFail,
			Summary: fmt.Sprintf("Redis not reachable at %s", addr),
			Detail:  err.Error(),
		}
	}

	return CheckResult{
		Name:    "redis",
		Status:  StatusPass,
		Summary: fmt.Sprintf("Redis reachable at %s", addr),
	}
}
