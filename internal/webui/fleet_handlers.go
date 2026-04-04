package webui

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// FleetRegisterConfig holds configuration for fleet worker registration authentication.
type FleetRegisterConfig struct {
	APIKey      string            `json:"-"` // Pre-shared API key for fleet registration
	RateLimiter *FleetRateLimiter // Optional rate limiter (nil = no rate limiting)
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
	RegisterWorker(ctx context.Context, worker *fleet.Worker) error
}

// handleFleetRegister returns a handler that registers a fleet worker and issues a JWT.
func handleFleetRegister(store *fleet.Store, tokenCfg *TokenConfig, regCfg *FleetRegisterConfig) http.HandlerFunc {
	return handleFleetRegisterWithStore(store, tokenCfg, regCfg)
}

// handleFleetRegisterWithStore is the internal implementation that accepts an interface for testing.
func handleFleetRegisterWithStore(store workerRegistrar, tokenCfg *TokenConfig, regCfg *FleetRegisterConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check store availability
		if store == nil || tokenCfg == nil {
			respondJSON(w, http.StatusServiceUnavailable, FleetRegisterResponse{
				Success: false,
				Error:   "fleet API not available",
			})
			return
		}

		// Validate fleet API key
		if regCfg == nil || regCfg.APIKey == "" {
			respondJSON(w, http.StatusServiceUnavailable, FleetRegisterResponse{
				Success: false,
				Error:   "fleet authentication not configured",
			})
			return
		}

		apiKeyHeader := r.Header.Get("X-Fleet-API-Key")
		if apiKeyHeader == "" {
			respondJSON(w, http.StatusUnauthorized, FleetRegisterResponse{
				Success: false,
				Error:   "missing X-Fleet-API-Key header",
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(apiKeyHeader), []byte(regCfg.APIKey)) != 1 {
			respondJSON(w, http.StatusUnauthorized, FleetRegisterResponse{
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
				respondJSON(w, http.StatusTooManyRequests, FleetRegisterResponse{
					Success: false,
					Error:   "rate limit exceeded",
				})
				return
			}
		}

		// Parse request body
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req FleetRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, FleetRegisterResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			logger.Warn("invalid request body", "handler", "handleFleetRegister", "err", err)
			respondJSON(w, http.StatusBadRequest, FleetRegisterResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Validate worker_id
		if req.WorkerID == "" {
			respondJSON(w, http.StatusBadRequest, FleetRegisterResponse{
				Success: false,
				Error:   "worker_id is required",
			})
			return
		}

		if len(req.WorkerID) > maxWorkerIDLength {
			respondJSON(w, http.StatusBadRequest, FleetRegisterResponse{
				Success: false,
				Error:   fmt.Sprintf("worker_id exceeds maximum length of %d characters", maxWorkerIDLength),
			})
			return
		}

		// Register the worker
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		worker := &fleet.Worker{
			WorkerID:     req.WorkerID,
			Repos:        req.Repos,
			RegisteredAt: time.Now().Unix(),
		}

		if err := store.RegisterWorker(ctx, worker); err != nil {
			logger.Error("failed to register worker", "worker_id", req.WorkerID, "err", err)
			respondJSON(w, http.StatusInternalServerError, FleetRegisterResponse{
				Success: false,
				Error:   "failed to register worker",
			})
			return
		}

		// Generate JWT token
		token, err := GenerateWorkerToken(req.WorkerID, req.Repos, tokenCfg.SigningKey, tokenCfg.Expiry)
		if err != nil {
			logger.Error("failed to generate token", "worker_id", req.WorkerID, "err", err)
			respondJSON(w, http.StatusInternalServerError, FleetRegisterResponse{
				Success: false,
				Error:   "failed to generate token",
			})
			return
		}

		logger.Info("worker registered", "worker_id", req.WorkerID)
		respondJSON(w, http.StatusCreated, FleetRegisterResponse{
			Success: true,
			Token:   token,
		})
	}
}
