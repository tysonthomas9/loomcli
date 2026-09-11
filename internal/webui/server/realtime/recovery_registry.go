package realtime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var (
	ErrRecoveryUnavailable = errors.New("issue recovery handle unavailable")
	ErrRecoveryDenied      = errors.New("issue recovery handle scope denied")
	ErrRecoveryBusy        = errors.New("issue recovery handle already in use")
)

const recoveryHandleTTL = 60 * time.Second
const recoveryHandleCapacity = 256
const recoveryPrincipalCapacity = 8
const issueRecoveryManifest = "fleet.issue-workspace.v2"

// RecoveryHandle identifies a captured source for bounded retries. SourceRepos
// records subscription filters, not repository authorization or snapshot scope.
type RecoveryHandle struct {
	Handle      string    `json:"handle"`
	Workspace   string    `json:"workspace"`
	SourceRepos []string  `json:"source_repos"`
	ExpiresAt   time.Time `json:"expires_at"`
	Manifest    string    `json:"manifest"`
}

type recoveryRegistration struct {
	handle    RecoveryHandle
	principal string
	source    backend.IssueRecoveryBackend
	busy      bool
}

// RecoveryRegistry owns short-lived references to exact sources, independent of
// the SSE request lifetime. It never replaces a captured source or acknowledges
// recovery. Expired references are reclaimed lazily on registration or lookup;
// at most 256 remain retained until another operation or Close. There is no
// cleanup goroutine. Call Close when its serving hub stops.
type RecoveryRegistry struct {
	mu       sync.Mutex
	entries  map[string]*recoveryRegistration
	now      func() time.Time
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
	shutdown <-chan struct{}
}

func NewRecoveryRegistry() *RecoveryRegistry {
	ctx, cancel := context.WithCancel(context.Background())
	return &RecoveryRegistry{entries: make(map[string]*recoveryRegistration), now: time.Now, ctx: ctx, cancel: cancel}
}

// ValidRecoveryHandle accepts only the canonical random 32-byte handle format.
func ValidRecoveryHandle(value string) bool {
	if len(value) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}

func (r *RecoveryRegistry) Register(principal, workspace string, repos []string, source backend.IssueRecoveryBackend) (RecoveryHandle, error) {
	if strings.TrimSpace(principal) == "" || strings.TrimSpace(workspace) == "" || source == nil {
		return RecoveryHandle{}, ErrRecoveryUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stoppedLocked() {
		return RecoveryHandle{}, ErrRecoveryUnavailable
	}
	now := r.now()
	count := 0
	for token, entry := range r.entries {
		if !now.Before(entry.handle.ExpiresAt) {
			delete(r.entries, token)
			continue
		}
		if entry.principal == principal {
			count++
		}
	}
	if len(r.entries) >= recoveryHandleCapacity || count >= recoveryPrincipalCapacity {
		return RecoveryHandle{}, ErrRecoveryUnavailable
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return RecoveryHandle{}, fmt.Errorf("%w: random handle: %v", ErrRecoveryUnavailable, err)
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	if _, exists := r.entries[token]; exists {
		return RecoveryHandle{}, ErrRecoveryUnavailable
	}
	handle := RecoveryHandle{Handle: token, Workspace: workspace, SourceRepos: append([]string{}, repos...), ExpiresAt: now.Add(recoveryHandleTTL).UTC(), Manifest: issueRecoveryManifest}
	r.entries[token] = &recoveryRegistration{handle: handle, principal: principal, source: source}
	// Both the caller and registry own their filter slices.
	handle.SourceRepos = append([]string{}, handle.SourceRepos...)
	return handle, nil
}

func (r *RecoveryRegistry) Read(ctx context.Context, principal, workspace, handle string) (backend.IssueRecoverySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return backend.IssueRecoverySnapshot{}, err
	}
	entry, err := r.acquire(principal, workspace, handle)
	if err != nil {
		return backend.IssueRecoverySnapshot{}, err
	}
	defer func() { r.mu.Lock(); entry.busy = false; r.mu.Unlock() }()
	readCtx, cancel := context.WithDeadline(ctx, entry.handle.ExpiresAt)
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	defer cancel()
	if r.ctx.Err() != nil {
		cancel()
	}
	if err := readCtx.Err(); err != nil {
		return backend.IssueRecoverySnapshot{}, err
	}
	result, err := entry.source.ReadIssueRecovery(readCtx)
	if err != nil {
		return backend.IssueRecoverySnapshot{}, err
	}
	if err := readCtx.Err(); err != nil {
		return backend.IssueRecoverySnapshot{}, err
	}
	r.mu.Lock()
	valid := !r.stoppedLocked() && r.entries[handle] == entry && r.now().Before(entry.handle.ExpiresAt)
	r.mu.Unlock()
	if !valid || result.Workspace != entry.handle.Workspace || result.Manifest != issueRecoveryManifest || result.Through == "" || len(result.Document) == 0 {
		return backend.IssueRecoverySnapshot{}, ErrRecoveryUnavailable
	}
	return result, nil
}

func (r *RecoveryRegistry) acquire(principal, workspace, handle string) (*recoveryRegistration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[handle]
	if r.stoppedLocked() || !ValidRecoveryHandle(handle) || entry == nil {
		return nil, ErrRecoveryUnavailable
	}
	if !r.now().Before(entry.handle.ExpiresAt) {
		delete(r.entries, handle)
		return nil, ErrRecoveryUnavailable
	}
	if entry.principal != principal || entry.handle.Workspace != workspace {
		return nil, ErrRecoveryDenied
	}
	if entry.busy {
		return nil, ErrRecoveryBusy
	}
	entry.busy = true
	return entry, nil
}

// Close cancels all active reads and releases retained source references.
func (r *RecoveryRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	clear(r.entries)
	r.cancel()
}

// bindShutdown adds a synchronous availability fence; the owner's watcher still
// calls Close to cancel in-flight work and release retained sources.
func (r *RecoveryRegistry) bindShutdown(done <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shutdown = done
}

func (r *RecoveryRegistry) stoppedLocked() bool {
	if r.closed {
		return true
	}
	select {
	case <-r.shutdown:
		return true
	default:
		return false
	}
}
