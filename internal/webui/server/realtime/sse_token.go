package realtime

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// TokenExpiry is the lifetime of an SSE auth token.
	TokenExpiry = 30 * time.Second

	tokenNonceBytes      = 16
	tokenSecretBytes     = 32
	tokenCleanupInterval = 5 * time.Minute
	tokenNonceMaxAge     = 2 * time.Minute
)

// tokenPayload is the JSON structure signed in an SSE auth token.
type tokenPayload struct {
	UserID      string `json:"uid"`
	WorkspaceID string `json:"wid,omitempty"`
	Exp         int64  `json:"exp"`
	Nonce       string `json:"nonce"`
}

// TokenStore manages one-time tokens for SSE connections.
type TokenStore struct {
	secret   []byte
	used     map[string]time.Time // nonce -> time added
	mu       sync.Mutex
	done     chan struct{}
	stopOnce sync.Once
}

// NewTokenStore creates a new SSE token store with a random HMAC secret.
func NewTokenStore() (*TokenStore, error) {
	secret := make([]byte, tokenSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("failed to generate SSE token secret: %w", err)
	}
	s := &TokenStore{
		secret: secret,
		used:   make(map[string]time.Time),
		done:   make(chan struct{}),
	}
	go s.cleanupLoop()
	return s, nil
}

// Generate creates a signed, time-limited token embedding the user ID and optional workspace ID.
func (s *TokenStore) Generate(userID, workspaceID string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("userID must not be empty")
	}
	nonce := make([]byte, tokenNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	payload := tokenPayload{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Exp:         time.Now().Add(TokenExpiry).Unix(),
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

// Validate checks the token signature, expiry, workspace binding, nonce single-use,
// and non-empty user ID. Returns the embedded user ID on success.
// The expectedWorkspaceID must match the workspace embedded in the token.
func (s *TokenStore) Validate(token, expectedWorkspaceID string) (string, error) {
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
	var payload tokenPayload
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

	// Check workspace binding (before nonce consumption so a mismatch doesn't burn the nonce)
	if payload.WorkspaceID != expectedWorkspaceID {
		return "", fmt.Errorf("workspace mismatch")
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
func (s *TokenStore) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
	})
}

// cleanupLoop periodically removes old nonces from the used set.
func (s *TokenStore) cleanupLoop() {
	ticker := time.NewTicker(tokenCleanupInterval)
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
func (s *TokenStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-tokenNonceMaxAge)
	for nonce, addedAt := range s.used {
		if addedAt.Before(cutoff) {
			delete(s.used, nonce)
		}
	}
}
