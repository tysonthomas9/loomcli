package netutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WaitForHealthz polls GET <baseURL>/healthz until it responds 200 OK
// or the context's deadline expires. Backs off exponentially from
// 50ms to 1s so the happy path returns quickly without burning CPU
// during slow startups.
//
// Pass a context with timeout/deadline to bound total wait. Each
// individual request uses perRequestTimeout (defaults to 2s when 0).
func WaitForHealthz(ctx context.Context, baseURL string, perRequestTimeout time.Duration) error {
	if perRequestTimeout <= 0 {
		perRequestTimeout = 2 * time.Second
	}
	client := &http.Client{Timeout: perRequestTimeout}
	delay := 50 * time.Millisecond
	const maxDelay = time.Second
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("healthz returned %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = errors.New("startup deadline exceeded")
			}
			return fmt.Errorf("waitForHealthz: %w", lastErr)
		case <-time.After(delay):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}
