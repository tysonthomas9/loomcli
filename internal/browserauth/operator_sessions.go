package browserauth

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// OperatorSessionHeader carries the local desktop operator bearer. The Tauri
// shell obtains it over the per-user Unix socket and hands it to the WebView
// through native IPC; it never appears in a URL, cookie or web storage.
const OperatorSessionHeader = "X-Loom-Operator-Session"

// Local operator session lifetimes (see docs/product/kernel-browser-auth.md).
const (
	OperatorIdleTTL     = 20 * time.Minute
	OperatorAbsoluteTTL = 12 * time.Hour
	maxOperatorSessions = 256
)

// ErrOperatorSessionExpired distinguishes expiry from an unknown bearer so the
// UI can say why it cleared state. Both wrap domain.ErrBrowserUnauthorized.
var ErrOperatorSessionExpired = fmt.Errorf("local operator session expired: %w", domain.ErrBrowserUnauthorized)

// OperatorSession is one live local desktop operator session. It is bound to
// the OS user that requested it over the socket and to one workspace.
type OperatorSession struct {
	ID         string    `json:"session_id"`
	Workspace  string    `json:"workspace"`
	UID        uint32    `json:"uid"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// Subject is the delegation subject for this session. It names the OS
// account, not an authenticated human.
func (s OperatorSession) Subject() string {
	return "local-os-user:" + strconv.FormatUint(uint64(s.UID), 10)
}

// IdleExpiresAt / AbsoluteExpiresAt report the session deadlines.
func (s OperatorSession) IdleExpiresAt() time.Time     { return s.LastUsedAt.Add(OperatorIdleTTL) }
func (s OperatorSession) AbsoluteExpiresAt() time.Time { return s.CreatedAt.Add(OperatorAbsoluteTTL) }

// ExpiresAt is the earlier of the idle and absolute deadlines.
func (s OperatorSession) ExpiresAt() time.Time {
	if idle, abs := s.IdleExpiresAt(), s.AbsoluteExpiresAt(); idle.Before(abs) {
		return idle
	}
	return s.AbsoluteExpiresAt()
}

// OperatorSessionRegistry holds local operator sessions in memory only; a
// Loom service restart revokes all of them.
type OperatorSessionRegistry struct {
	mu       sync.Mutex
	sessions map[[32]byte]*OperatorSession
	now      func() time.Time
}

// NewOperatorSessionRegistry returns an empty registry.
func NewOperatorSessionRegistry() *OperatorSessionRegistry {
	return &OperatorSessionRegistry{sessions: map[[32]byte]*OperatorSession{}, now: time.Now}
}

// SetClock overrides the clock (tests).
func (r *OperatorSessionRegistry) SetClock(now func() time.Time) {
	r.mu.Lock()
	r.now = now
	r.mu.Unlock()
}

// Issue creates a session for uid in workspace and returns its bearer.
func (r *OperatorSessionRegistry) Issue(workspace string, uid uint32) (string, OperatorSession, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", OperatorSession{}, errors.New("operator session requires a workspace")
	}
	token, hash, err := newBearer()
	if err != nil {
		return "", OperatorSession{}, err
	}
	id, err := randomID()
	if err != nil {
		return "", OperatorSession{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	r.sweepLocked(now)
	if len(r.sessions) >= maxOperatorSessions {
		return "", OperatorSession{}, errors.New("too many local operator sessions")
	}
	s := &OperatorSession{ID: "los_" + id, Workspace: workspace, UID: uid, CreatedAt: now, LastUsedAt: now}
	r.sessions[hash] = s
	return token, *s, nil
}

// Validate checks token for an operation in workspace and extends its idle
// deadline. Expired sessions are removed.
func (r *OperatorSessionRegistry) Validate(token, workspace string) (OperatorSession, error) {
	s, err := r.touch(token)
	if err != nil {
		return OperatorSession{}, err
	}
	if s.Workspace != workspace {
		return OperatorSession{}, fmt.Errorf("local operator session is bound to another workspace: %w", domain.ErrBrowserForbidden)
	}
	return s, nil
}

// Refresh extends the idle deadline without an operation. It cannot extend
// past the absolute deadline.
func (r *OperatorSessionRegistry) Refresh(token string) (OperatorSession, error) {
	return r.touch(token)
}

// Revoke ends the session for token. Returns false when it was not live.
func (r *OperatorSessionRegistry) Revoke(token string) bool {
	hash, ok := hashBearer(token)
	if !ok {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.sessions[hash]; !found {
		return false
	}
	delete(r.sessions, hash)
	return true
}

// RevokeAll ends every session (service shutdown).
func (r *OperatorSessionRegistry) RevokeAll() {
	r.mu.Lock()
	r.sessions = map[[32]byte]*OperatorSession{}
	r.mu.Unlock()
}

// Len reports live (unswept) sessions.
func (r *OperatorSessionRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(r.now().UTC())
	return len(r.sessions)
}

func (r *OperatorSessionRegistry) touch(token string) (OperatorSession, error) {
	if r == nil {
		return OperatorSession{}, fmt.Errorf("local operator sessions: %w", domain.ErrBrowserUnavailable)
	}
	hash, ok := hashBearer(token)
	if !ok {
		return OperatorSession{}, fmt.Errorf("local operator session required: %w", domain.ErrBrowserUnauthorized)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, found := r.sessions[hash]
	if !found {
		return OperatorSession{}, fmt.Errorf("local operator session not active: %w", domain.ErrBrowserUnauthorized)
	}
	now := r.now().UTC()
	if !now.Before(s.ExpiresAt()) {
		delete(r.sessions, hash)
		return OperatorSession{}, ErrOperatorSessionExpired
	}
	s.LastUsedAt = now
	return *s, nil
}

func (r *OperatorSessionRegistry) sweepLocked(now time.Time) {
	for hash, s := range r.sessions {
		if !now.Before(s.ExpiresAt()) {
			delete(r.sessions, hash)
		}
	}
}
