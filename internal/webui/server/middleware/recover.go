package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover returns middleware that recovers from panics in downstream handlers,
// logs the stack trace at Error level, and returns 500 Internal Server Error.
// If logger is nil, slog.Default() is used.
func Recover(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					logger.Error("panic recovered in HTTP handler",
						"panic", rv,
						"method", r.Method,
						"path", r.URL.Path,
						"stack", string(debug.Stack()),
					)
					writeJSONError(w, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
