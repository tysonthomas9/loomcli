package webui

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
	terminalTokenExpiry     = 60 * time.Second
	terminalNonceBytes      = 16
	terminalSecretBytes     = 32
	terminalCleanupInterval = 5 * time.Minute
	terminalNonceMaxAge     = 2 * time.Minute
)

// terminalTokenPayload is the JSON structure signed in a terminal auth token.
type terminalTokenPayload struct {
	Session string `json:"session"`
	Exp     int64  `json:"exp"`
	Nonce   string `json:"nonce"`
}

// terminalAuth manages one-time tokens for terminal WebSocket connections.
type terminalAuth struct {
	secret   []byte
	used     map[string]time.Time // nonce -> time added
	mu       sync.Mutex
	done     chan struct{}
	stopOnce sync.Once
}

// newTerminalAuth creates a new terminal auth manager with a random secret.
func newTerminalAuth() (*terminalAuth, error) {
	secret := make([]byte, terminalSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("failed to generate terminal auth secret: %w", err)
	}
	ta := &terminalAuth{
		secret: secret,
		used:   make(map[string]time.Time),
		done:   make(chan struct{}),
	}
	go ta.cleanupLoop()
	return ta, nil
}

// GenerateToken creates a signed, time-limited token for the given session.
func (ta *terminalAuth) GenerateToken(session string) (string, error) {
	nonce := make([]byte, terminalNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	payload := terminalTokenPayload{
		Session: session,
		Exp:     time.Now().Add(terminalTokenExpiry).Unix(),
		Nonce:   hex.EncodeToString(nonce),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, ta.secret)
	mac.Write(payloadBytes)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return payloadB64 + "." + sig, nil
}

// ValidateToken checks the token signature, expiry, session match, and single-use.
// Returns nil if valid, error describing the failure otherwise.
func (ta *terminalAuth) ValidateToken(token, session string) error {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("malformed token")
	}

	payloadB64, sigB64 := parts[0], parts[1]

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return fmt.Errorf("invalid payload encoding")
	}

	// Verify signature
	mac := hmac.New(sha256.New, ta.secret)
	mac.Write(payloadBytes)
	expectedSig := mac.Sum(nil)

	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("invalid signature encoding")
	}

	if !hmac.Equal(sig, expectedSig) {
		return fmt.Errorf("invalid signature")
	}

	// Parse payload
	var payload terminalTokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return fmt.Errorf("invalid payload")
	}

	// Check expiry
	if time.Now().Unix() > payload.Exp {
		return fmt.Errorf("token expired")
	}

	// Check session match
	if payload.Session != session {
		return fmt.Errorf("session mismatch")
	}

	// Check single-use
	ta.mu.Lock()
	defer ta.mu.Unlock()

	if _, exists := ta.used[payload.Nonce]; exists {
		return fmt.Errorf("token already used")
	}
	ta.used[payload.Nonce] = time.Now()

	return nil
}

// Stop stops the cleanup goroutine. Safe to call multiple times.
func (ta *terminalAuth) Stop() {
	ta.stopOnce.Do(func() {
		close(ta.done)
	})
}

// cleanupLoop periodically removes old nonces from the used set.
func (ta *terminalAuth) cleanupLoop() {
	ticker := time.NewTicker(terminalCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ta.done:
			return
		case <-ticker.C:
			ta.cleanup()
		}
	}
}

// cleanup removes expired nonces from the used set.
func (ta *terminalAuth) cleanup() {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	cutoff := time.Now().Add(-terminalNonceMaxAge)
	for nonce, addedAt := range ta.used {
		if addedAt.Before(cutoff) {
			delete(ta.used, nonce)
		}
	}
}
