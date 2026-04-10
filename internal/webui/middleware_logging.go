package webui

import (
	"log/slog"
	"net/http"
	"time"
)

// NewRequestLogMiddleware returns middleware that logs method, path, status,
// duration_ms, and client IP for every HTTP request using slog.
// Health-check paths (/health, /api/health) are excluded to avoid probe spam.
// If logger is nil, slog.Default() is used.
func NewRequestLogMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isHealthCheckPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			ctx := r.Context()
			start := time.Now()
			rec := newRWRecorder(w)
			next.ServeHTTP(rec, r)
			duration := time.Since(start)

			logger.InfoContext(ctx, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.Status()),
				slog.Float64("duration_ms", float64(duration.Microseconds())/1000.0),
				slog.String("ip", extractClientIP(r)),
			)
		})
	}
}

func isHealthCheckPath(path string) bool {
	return path == "/health" || path == "/api/health"
}
