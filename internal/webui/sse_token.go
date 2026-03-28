package webui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sseTokenExpiry          = 30 * time.Second
	sseTokenNonceBytes      = 16
	sseTokenSecretBytes     = 32
	sseTokenCleanupInterval = 5 * time.Minute
	sseTokenNonceMaxAge     = 2 * time.Minute
)

// sseTokenPayload is the JSON structure signed in an SSE auth token.
type sseTokenPayload struct {
	UserID      string `json:"uid"`
	WorkspaceID string `json:"wid,omitempty"`
	Exp         int64  `json:"exp"`
	Nonce       string `json:"nonce"`
}

// sseTokenStore manages one-time tokens for SSE connections.
// Follows the same pattern as terminalAuth (terminal_auth.go).
type sseTokenStore struct {
	secret   []byte
	used     map[string]time.Time // nonce -> time added
	mu       sync.Mutex
	done     chan struct{}
	stopOnce sync.Once
}

// newSSETokenStore creates a new SSE token store with a random HMAC secret.
func newSSETokenStore() (*sseTokenStore, error) {
	secret := make([]byte, sseTokenSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("failed to generate SSE token secret: %w", err)
	}
	s := &sseTokenStore{
		secret: secret,
		used:   make(map[string]time.Time),
		done:   make(chan struct{}),
	}
	go s.cleanupLoop()
	return s, nil
}

// Generate creates a signed, time-limited token embedding the user ID and optional workspace ID.
func (s *sseTokenStore) Generate(userID, workspaceID string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("userID must not be empty")
	}
	nonce := make([]byte, sseTokenNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	payload := sseTokenPayload{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Exp:         time.Now().Add(sseTokenExpiry).Unix(),
		Nonce:       hex.EncodeToString(nonce),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, s.secret)
	mac.Write(payloadBytes)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return payloadB64 + "." + sig, nil
}

// Validate checks the token signature, expiry, nonce single-use, and non-empty user ID.
// Returns the embedded user ID on success.
func (s *sseTokenStore) Validate(token string) (string, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("malformed token")
	}

	payloadB64, sigB64 := parts[0], parts[1]

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", fmt.Errorf("invalid payload encoding")
	}

	// Verify signature
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(payloadBytes)
	expectedSig := mac.Sum(nil)

	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return "", fmt.Errorf("invalid signature encoding")
	}

	if !hmac.Equal(sig, expectedSig) {
		return "", fmt.Errorf("invalid signature")
	}

	// Parse payload
	var payload sseTokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return "", fmt.Errorf("invalid payload")
	}

	// Check expiry
	if time.Now().Unix() > payload.Exp {
		return "", fmt.Errorf("token expired")
	}

	// Check identity binding
	if payload.UserID == "" {
		return "", fmt.Errorf("invalid token: missing user identity")
	}

	// Check single-use
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.used[payload.Nonce]; exists {
		return "", fmt.Errorf("token already used")
	}
	s.used[payload.Nonce] = time.Now()

	return payload.UserID, nil
}

// Stop stops the cleanup goroutine. Safe to call multiple times.
func (s *sseTokenStore) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
	})
}

// cleanupLoop periodically removes old nonces from the used set.
func (s *sseTokenStore) cleanupLoop() {
	ticker := time.NewTicker(sseTokenCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

// cleanup removes expired nonces from the used set.
func (s *sseTokenStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-sseTokenNonceMaxAge)
	for nonce, addedAt := range s.used {
		if addedAt.Before(cutoff) {
			delete(s.used, nonce)
		}
	}
}

// validateSSEAuth checks the opaque token from the query parameter when auth
// is required (sseAuth non-nil). Returns true if the request should proceed.
func validateSSEAuth(w http.ResponseWriter, r *http.Request, sseAuth *sseTokenStore) bool {
	if sseAuth == nil {
		return true
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	if _, err := sseAuth.Validate(token); err != nil {
		respondError(w, http.StatusUnauthorized, "invalid or expired token")
		return false
	}
	return true
}

// handleSSEToken creates an HTTP handler that exchanges a valid JWT for a
// short-lived opaque SSE token. The JWT is validated by ExtAuth middleware
// upstream; this handler extracts UserIdentity from the request context.
func handleSSEToken(store *sseTokenStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := UserIdentityFromContext(r.Context())
		if !ok || identity.UserID == "" {
			respondError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		workspaceID := WorkspaceFromContext(r.Context())

		token, err := store.Generate(identity.UserID, workspaceID)
		if err != nil {
			slog.Warn("failed to generate SSE token", "err", err)
			respondError(w, http.StatusInternalServerError, "failed to generate token")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		respondJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}
