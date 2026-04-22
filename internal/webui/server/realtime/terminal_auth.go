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
	// TerminalTokenExpiry is the lifetime of a terminal auth token.
	TerminalTokenExpiry = 60 * time.Second

	terminalNonceBytes      = 16
	terminalSecretBytes     = 32
	terminalCleanupInterval = 5 * time.Minute
	terminalNonceMaxAge     = 2 * time.Minute
)

// terminalTokenPayload is the JSON structure signed in a terminal auth token.
// Ws binds the token to the workspace it was minted against so a token for
// ws-A cannot be reused to open a terminal in ws-B.
type terminalTokenPayload struct {
	Session string `json:"session"`
	Ws      string `json:"ws,omitempty"`
	UserID  string `json:"uid,omitempty"`
	Exp     int64  `json:"exp"`
	Nonce   string `json:"nonce"`
}

// TerminalAuth manages one-time tokens for terminal WebSocket connections.
type TerminalAuth struct {
	secret   []byte
	used     map[string]time.Time // nonce -> time added
	mu       sync.Mutex
	done     chan struct{}
	stopOnce sync.Once
}

// NewTerminalAuth creates a new terminal auth manager with a random secret.
func NewTerminalAuth() (*TerminalAuth, error) {
	secret := make([]byte, terminalSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("failed to generate terminal auth secret: %w", err)
	}
	ta := &TerminalAuth{
		secret: secret,
		used:   make(map[string]time.Time),
		done:   make(chan struct{}),
	}
	go ta.cleanupLoop()
	return ta, nil
}

// GenerateToken creates a signed, time-limited token for the given session
// within a specific workspace. userID is embedded for audit logging; pass ""
// in open mode (no auth). ws binds the token to a workspace so it cannot be
// reused in another workspace's terminal endpoint.
func (ta *TerminalAuth) GenerateToken(session, ws, userID string) (string, error) {
	nonce := make([]byte, terminalNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	payload := terminalTokenPayload{
		Session: session,
		Ws:      ws,
		UserID:  userID,
		Exp:     time.Now().Add(TerminalTokenExpiry).Unix(),
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

// parseAndVerify decodes the token, checks its signature against ta.secret,
// and returns the parsed payload. Pure function of the token + secret — does
// not touch session/workspace/nonce state.
func (ta *TerminalAuth) parseAndVerify(token string) (terminalTokenPayload, error) {
	var payload terminalTokenPayload
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return payload, fmt.Errorf("malformed token")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return payload, fmt.Errorf("invalid payload encoding")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return payload, fmt.Errorf("invalid signature encoding")
	}
	mac := hmac.New(sha256.New, ta.secret)
	mac.Write(payloadBytes)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return payload, fmt.Errorf("invalid signature")
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return payload, fmt.Errorf("invalid payload")
	}
	return payload, nil
}

// ValidateToken checks the token signature, expiry, session/workspace match,
// and single-use. Returns the embedded userID (may be empty in open mode) and
// nil error if valid. Callers must pass the workspace taken from the request
// path so cross-workspace token reuse is rejected.
func (ta *TerminalAuth) ValidateToken(token, session, ws string) (string, error) {
	payload, err := ta.parseAndVerify(token)
	if err != nil {
		return "", err
	}

	if time.Now().Unix() > payload.Exp {
		return "", fmt.Errorf("token expired")
	}
	if payload.Session != session {
		return "", fmt.Errorf("session mismatch")
	}
	// Prevents a token minted for ws-A from being replayed at
	// /api/workspaces/ws-B/terminal/ws.
	if payload.Ws != ws {
		return "", fmt.Errorf("workspace mismatch")
	}

	// Single-use enforcement
	ta.mu.Lock()
	defer ta.mu.Unlock()

	if _, exists := ta.used[payload.Nonce]; exists {
		return "", fmt.Errorf("token already used")
	}
	ta.used[payload.Nonce] = time.Now()

	return payload.UserID, nil
}

// Stop stops the cleanup goroutine. Safe to call multiple times.
func (ta *TerminalAuth) Stop() {
	ta.stopOnce.Do(func() {
		close(ta.done)
	})
}

// cleanupLoop periodically removes old nonces from the used set.
func (ta *TerminalAuth) cleanupLoop() {
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
func (ta *TerminalAuth) cleanup() {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	cutoff := time.Now().Add(-terminalNonceMaxAge)
	for nonce, addedAt := range ta.used {
		if addedAt.Before(cutoff) {
			delete(ta.used, nonce)
		}
	}
}
