package fleet

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// RegisterConfig holds configuration for fleet worker registration authentication.
type RegisterConfig struct {
	APIKey      string       `json:"-"` // Pre-shared API key for fleet registration
	RateLimiter *RateLimiter // Optional rate limiter (nil = no rate limiting)
}

// FleetRegisterRequest represents the JSON body for POST /api/fleet/register.
type FleetRegisterRequest struct {
	WorkerID string   `json:"worker_id"`
	Repos    []string `json:"repos,omitempty"`
}

// FleetRegisterResponse wraps the registration result for JSON response.
type FleetRegisterResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token,omitempty"`
	Error   string `json:"error,omitempty"`
}

// maxWorkerIDLength is the maximum length for a worker_id to prevent abuse.
const maxWorkerIDLength = 256

// workerRegistrar is an internal interface for testing worker registration.
type workerRegistrar interface {
	RegisterWorker(ctx context.Context, worker *Worker) error
}

// handleFleetRegister returns a handler that registers a fleet worker and issues a JWT.
func handleFleetRegister(store *Store, tokenCfg *TokenConfig, regCfg *RegisterConfig) http.HandlerFunc {
	return handleFleetRegisterWithStore(store, tokenCfg, regCfg)
}

// handleFleetRegisterWithStore is the internal implementation that accepts an interface for testing.
func handleFleetRegisterWithStore(store workerRegistrar, tokenCfg *TokenConfig, regCfg *RegisterConfig) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		// Check store availability
		if store == nil || tokenCfg == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, FleetRegisterResponse{
				Success: false,
				Error:   "fleet API not available",
			})
			return
		}

		// Validate fleet API key
		if regCfg == nil || regCfg.APIKey == "" {
			handler.WriteJSON(w, http.StatusServiceUnavailable, FleetRegisterResponse{
				Success: false,
				Error:   "fleet authentication not configured",
			})
			return
		}

		apiKeyHeader := r.Header.Get("X-Fleet-API-Key")
		if apiKeyHeader == "" {
			handler.WriteJSON(w, http.StatusUnauthorized, FleetRegisterResponse{
				Success: false,
				Error:   "missing X-Fleet-API-Key header",
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(apiKeyHeader), []byte(regCfg.APIKey)) != 1 {
			handler.WriteJSON(w, http.StatusUnauthorized, FleetRegisterResponse{
				Success: false,
				Error:   "invalid API key",
			})
			return
		}

		// Rate limit by client IP (if rate limiter is configured)
		if regCfg.RateLimiter != nil {
			clientIP := middleware.ExtractClientIP(r)
			allowed, _ := regCfg.RateLimiter.Allow(r.Context(), clientIP)
			if !allowed {
				handler.WriteJSON(w, http.StatusTooManyRequests, FleetRegisterResponse{
					Success: false,
					Error:   "rate limit exceeded",
				})
				return
			}
		}

		// Parse request body.
		var req FleetRegisterRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody}); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, FleetRegisterResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			slog.Warn("invalid request body", "handler", "handleFleetRegister", "err", err)
			handler.WriteJSON(w, http.StatusBadRequest, FleetRegisterResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Validate worker_id
		if req.WorkerID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, FleetRegisterResponse{
				Success: false,
				Error:   "worker_id is required",
			})
			return
		}

		if len(req.WorkerID) > maxWorkerIDLength {
			handler.WriteJSON(w, http.StatusBadRequest, FleetRegisterResponse{
				Success: false,
				Error:   fmt.Sprintf("worker_id exceeds maximum length of %d characters", maxWorkerIDLength),
			})
			return
		}

		// Register the worker
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		worker := &Worker{
			WorkerID:     req.WorkerID,
			Repos:        req.Repos,
			RegisteredAt: time.Now().Unix(),
		}

		if err := store.RegisterWorker(ctx, worker); err != nil {
			slog.Error("failed to register worker", "worker_id", req.WorkerID, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, FleetRegisterResponse{
				Success: false,
				Error:   "failed to register worker",
			})
			return
		}

		// Generate JWT token
		token, err := GenerateWorkerToken(req.WorkerID, req.Repos, tokenCfg.SigningKey, tokenCfg.Expiry)
		if err != nil {
			slog.Error("failed to generate token", "worker_id", req.WorkerID, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, FleetRegisterResponse{
				Success: false,
				Error:   "failed to generate token",
			})
			return
		}

		slog.Info("worker registered", "worker_id", req.WorkerID)
		handler.WriteJSON(w, http.StatusCreated, FleetRegisterResponse{
			Success: true,
			Token:   token,
		})
	}
}
