package stackpublish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/domain"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// admissionCtxKey marks a context that already holds a publish admission so
// nested Restack inside Publish reuses the outer guard (one flock/lease).
type admissionCtxKey struct{}

// Session is one live publish/restack admission. Acquire before forge mutation;
// renew regularly and before each mutating phase; cancel on renew failure;
// bound each GitHub HTTP/git call; release only when remote effects are known
// settled.
//
// Guarantee boundary: the lease fences FleetDB generations only. An already-sent
// GitHub request or an old client can still race after cancellation. These
// per-call safety deadlines are not a Loom agent session budget. Clean Release
// clears FleetDB reuse_after grace — skip it whenever a started forge/git call
// timed out, was cancelled, hit ambiguous transport, or lost renewal, so a
// successor cannot mutate while a prior remote effect may still complete.
type Session struct {
	ws    string
	id    sl.StackID
	store stackstore.PublishAdmittable
	// token and generation are immutable for the life of the session; renew
	// grants must match or the session fails closed.
	token      string
	holder     string
	generation int64

	cancel context.CancelFunc
	lost   atomic.Bool
	// uncertain means remote effects may still be in flight; skip clean Release.
	uncertain atomic.Bool

	mu         sync.Mutex
	expiresAt  time.Time
	reuseAfter time.Time

	// flightMu synchronizes beginCall (Add) against closeForSettle (set closed
	// then Wait) so no new call starts once final Wait/release begins.
	flightMu sync.Mutex
	closed   bool
	inFlight sync.WaitGroup

	// Hooks for tests.
	now           func() time.Time
	renewInterval time.Duration
	stopRenew     chan struct{}
	renewDone     chan struct{}
}

// HolderIdentity builds a lease holder string (role@host#pid), truncated to
// fleet-db's 256-byte limit.
func HolderIdentity(role string) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	role = strings.TrimSpace(role)
	if role == "" {
		role = "loomcli"
	}
	suffix := "#" + strconv.Itoa(os.Getpid())
	budget := 256 - len("@") - len(suffix)
	roleBudget := budget / 2
	hostBudget := budget - roleBudget
	return truncateUTF8(role, roleBudget) + "@" + truncateUTF8(host, hostBudget) + suffix
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.ValidString(value[:maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}

// SessionFrom returns the admission session attached to ctx, if any.
func SessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(admissionCtxKey{}).(*Session)
	return s
}

// withAdmission runs fn under one publish admission. Dry-run callers must not
// invoke this. Nested Restack within Publish reuses the outer session only for
// the same (workspace, stackID); a different stack fails closed.
func (r *Reconciler) withAdmission(ctx context.Context, ws string, id sl.StackID, fn func(context.Context, *Session) error) (err error) {
	if existing := SessionFrom(ctx); existing != nil {
		if existing.ws != ws || existing.id != id {
			return fmt.Errorf("nested publish admission for workspace %q stack %q while holding %q/%q: %w",
				ws, id, existing.ws, existing.id, domain.ErrStackPublishLeaseStoreUnavailable)
		}
		return fn(ctx, existing)
	}
	admittable, ok := r.Store.(stackstore.PublishAdmittable)
	if !ok {
		return fmt.Errorf("stack publish admission unavailable: store %T does not implement PublishAdmittable: %w",
			r.Store, domain.ErrStackPublishLeaseStoreUnavailable)
	}
	sess, sessCtx, err := r.beginAdmissionSession(ctx, admittable, ws, id)
	if err != nil {
		return err
	}
	defer func() {
		err = settleAdmission(err, ctx, sess, admittable, ws, id)
	}()
	return fn(sessCtx, sess)
}

// beginAdmissionSession acquires a grant and starts renew; caller must settle.
func (r *Reconciler) beginAdmissionSession(ctx context.Context, admittable stackstore.PublishAdmittable, ws string, id sl.StackID) (*Session, context.Context, error) {
	holder := strings.TrimSpace(r.Holder)
	if holder == "" {
		holder = HolderIdentity("loomcli")
	}
	grant, err := admittable.AcquirePublishAdmission(ctx, ws, id, holder)
	if err != nil {
		return nil, nil, err
	}
	if grant == nil || strings.TrimSpace(grant.Token) == "" {
		return nil, nil, fmt.Errorf("stack publish admission grant missing token: %w", domain.ErrStackPublishLeaseStoreUnavailable)
	}
	sessCtx, cancel := context.WithCancel(ctx)
	renewEvery := r.AdmissionRenewInterval
	if renewEvery <= 0 {
		renewEvery = domain.DefaultStackPublishLeaseTTL / 3
	}
	sess := &Session{
		ws: ws, id: id, store: admittable,
		token: grant.Token, holder: grant.Holder, generation: grant.Generation,
		cancel: cancel, expiresAt: grant.ExpiresAt, reuseAfter: grant.ReuseAfter,
		now: time.Now, renewInterval: renewEvery,
		stopRenew: make(chan struct{}), renewDone: make(chan struct{}),
	}
	sessCtx = context.WithValue(sessCtx, admissionCtxKey{}, sess)
	sess.startRenewLoop(sessCtx)
	return sess, sessCtx, nil
}

// settleAdmission closes the flight gate then Release (clean) or Abandon
// (uncertain). Ordering: closeForSettle before store end so renew/Do drain first.
func settleAdmission(err error, ctx context.Context, sess *Session, admittable stackstore.PublishAdmittable, ws string, id sl.StackID) error {
	// Close flight gate first so Do cannot Add after Wait begins; cancel so
	// a Renew blocked on sessCtx exits; join renew; wait for local callbacks.
	sess.closeForSettle()
	releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer releaseCancel()
	var settleErr error
	if sess.Uncertain() {
		// Skip clean FleetDB Release (preserves reuse_after). Local Abandon
		// drops the flock fd but keeps the persisted cooldown.
		settleErr = admittable.AbandonPublishAdmission(releaseCtx, ws, id, sess.token)
	} else {
		settleErr = admittable.ReleasePublishAdmission(releaseCtx, ws, id, sess.token)
	}
	if settleErr == nil {
		return err
	}
	if err == nil {
		return settleErr
	}
	return errors.Join(err, settleErr)
}

func (s *Session) startRenewLoop(ctx context.Context) {
	go func() {
		defer close(s.renewDone)
		interval := s.renewInterval
		if interval <= 0 {
			interval = domain.DefaultStackPublishLeaseTTL / 3
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopRenew:
				return
			case <-ticker.C:
				if err := s.Renew(ctx); err != nil {
					return
				}
			}
		}
	}()
}

func (s *Session) stopRenewLoop() {
	select {
	case <-s.stopRenew:
	default:
		close(s.stopRenew)
	}
	<-s.renewDone
}

// closeForSettle prevents new Do calls, cancels the session, joins renew, and
// waits for in-flight local callbacks to return.
func (s *Session) closeForSettle() {
	s.flightMu.Lock()
	s.closed = true
	s.flightMu.Unlock()
	s.cancel()
	s.stopRenewLoop()
	s.inFlight.Wait()
}

func (s *Session) beginCall() error {
	s.flightMu.Lock()
	defer s.flightMu.Unlock()
	if s.closed || s.lost.Load() {
		return domain.ErrStackPublishLeaseLost
	}
	s.inFlight.Add(1)
	return nil
}

func (s *Session) endCall() {
	s.inFlight.Done()
}

func (s *Session) markLost() {
	s.uncertain.Store(true)
	if s.lost.CompareAndSwap(false, true) {
		s.cancel()
	}
}

func (s *Session) markUncertain() {
	s.uncertain.Store(true)
	if s.lost.CompareAndSwap(false, true) {
		s.cancel()
	}
}

// Lost reports whether the session lost its lease.
func (s *Session) Lost() bool { return s.lost.Load() }

// Uncertain reports whether remote effects may still be in flight; clean
// Release must be skipped.
func (s *Session) Uncertain() bool { return s.uncertain.Load() }

// Renew extends the verified lease. Failure cancels the session context and
// marks the outcome uncertain (skip clean Release). Token and generation are
// immutable; mismatched renew grants fail closed.
func (s *Session) Renew(ctx context.Context) error {
	if s.lost.Load() {
		return domain.ErrStackPublishLeaseLost
	}
	token := s.token
	gen := s.generation
	grant, err := s.store.RenewPublishAdmission(ctx, s.ws, s.id, token)
	if err != nil {
		s.markLost()
		return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseLost, err)
	}
	if grant == nil || strings.TrimSpace(grant.Token) == "" {
		s.markLost()
		return fmt.Errorf("%w: renew returned empty grant", domain.ErrStackPublishLeaseLost)
	}
	if grant.Token != token || grant.Generation != gen {
		s.markLost()
		// Do not interpolate lease bearer tokens into user-facing errors.
		return fmt.Errorf("%w: renew grant token/generation mismatch (got generation %d want %d)",
			domain.ErrStackPublishLeaseLost, grant.Generation, gen)
	}
	now := s.now()
	if grant.ExpiresAt.IsZero() || !grant.ExpiresAt.After(now) {
		s.markLost()
		return fmt.Errorf("%w: renew grant expiry invalid", domain.ErrStackPublishLeaseLost)
	}
	s.mu.Lock()
	s.expiresAt = grant.ExpiresAt
	s.reuseAfter = grant.ReuseAfter
	s.mu.Unlock()
	return nil
}

// ExpiresAt returns the last verified lease expiry.
func (s *Session) ExpiresAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expiresAt
}

// callDeadline returns the per-forge-call timeout: min(MaxCallBound,
// remaining verified validity minus clock skew). Zero/negative means stop.
func (s *Session) callDeadline() time.Duration {
	now := s.now()
	s.mu.Lock()
	expires := s.expiresAt
	s.mu.Unlock()
	remaining := expires.Sub(now) - domain.StackPublishClockSkew
	if remaining <= 0 {
		return 0
	}
	if remaining > domain.MaxStackPublishCallBound {
		return domain.MaxStackPublishCallBound
	}
	return remaining
}

// prepareBound renews (when needed) and returns a call context bounded by
// min(MaxStackPublishCallBound, remaining verified validity minus skew). The
// caller must endCall after the bound work returns. On lease loss before the
// call starts, prepareBound returns an error and has already endCall'd.
func (s *Session) prepareBound(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := s.beginCall(); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		s.endCall()
		s.markUncertain()
		return nil, nil, err
	}
	if err := s.Renew(ctx); err != nil {
		s.endCall()
		return nil, nil, err
	}
	d := s.callDeadline()
	if d <= 0 {
		s.endCall()
		s.markLost()
		return nil, nil, domain.ErrStackPublishLeaseLost
	}
	callCtx, cancel := context.WithTimeout(ctx, d)
	// Mark depth so nested runGit under this bound does not re-enter Do/boundLocal.
	callCtx = context.WithValue(callCtx, callBoundDepthKey{}, true)
	return callCtx, cancel, nil
}

// Do renews (when needed), bounds the call to the verified lease window, and
// tracks in-flight work so settle waits for local callback return. On lease
// loss, timeout, cancellation, or ambiguous transport it marks uncertain and
// never starts (or never treats as settled) a forge call.
//
// Guarantee boundary: local callback return is not an absolute GitHub fence —
// timeout/cancel/transport can leave a remote effect unknown. Use boundLocal
// for settled local-only git exits (rebase conflict, missing ref) that must
// not poison admission.
func (s *Session) Do(ctx context.Context, fn func(context.Context) error) error {
	callCtx, cancel, err := s.prepareBound(ctx)
	if err != nil {
		return err
	}
	defer s.endCall()
	defer cancel()
	err = fn(callCtx)
	if s.lost.Load() {
		s.uncertain.Store(true)
		return domain.ErrStackPublishLeaseLost
	}
	if forgeOutcomeUncertain(err, callCtx) {
		s.markUncertain()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			if err != nil {
				return fmt.Errorf("%w: forge call timed out; outcome uncertain: %w", domain.ErrStackPublishLeaseLost, err)
			}
			return fmt.Errorf("%w: forge call timed out; outcome uncertain", domain.ErrStackPublishLeaseLost)
		}
		if errors.Is(err, context.Canceled) || errors.Is(callCtx.Err(), context.Canceled) {
			if err != nil {
				return fmt.Errorf("%w: forge call cancelled; outcome uncertain: %w", domain.ErrStackPublishLeaseLost, err)
			}
			return fmt.Errorf("%w: forge call cancelled; outcome uncertain", domain.ErrStackPublishLeaseLost)
		}
		if err == nil {
			return fmt.Errorf("%w: forge call context done; outcome uncertain", domain.ErrStackPublishLeaseLost)
		}
		return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseLost, err)
	}
	return err
}

// boundLocal renews and applies the same ≤60s / remaining-validity-minus-skew
// deadline as Do, but treats a settled local nonzero exit (contexts still live)
// as known — the error is returned without marking uncertain or canceling the
// session. Timeout, cancel, and renew loss still mark uncertain.
func (s *Session) boundLocal(ctx context.Context, fn func(context.Context) error) error {
	callCtx, cancel, err := s.prepareBound(ctx)
	if err != nil {
		return err
	}
	defer s.endCall()
	defer cancel()
	err = fn(callCtx)
	if s.lost.Load() {
		s.uncertain.Store(true)
		return domain.ErrStackPublishLeaseLost
	}
	if err == nil {
		if callCtx.Err() != nil {
			s.markUncertain()
			return fmt.Errorf("%w: local call context done; outcome uncertain", domain.ErrStackPublishLeaseLost)
		}
		return nil
	}
	// Settled local failure while parent and call contexts are still live.
	if callCtx.Err() == nil && ctx.Err() == nil {
		return err
	}
	s.markUncertain()
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: local call timed out; outcome uncertain: %w", domain.ErrStackPublishLeaseLost, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(callCtx.Err(), context.Canceled) {
		return fmt.Errorf("%w: local call cancelled; outcome uncertain: %w", domain.ErrStackPublishLeaseLost, err)
	}
	return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseLost, err)
}

// forgeOutcomeUncertain reports whether err (after a started call) means the
// remote effect may still complete. Conservative default: any non-nil error is
// uncertain unless an explicit proven no-effect response is allowlisted. A nil
// error with an already-expired/cancelled call context is also uncertain.
func forgeOutcomeUncertain(err error, callCtx context.Context) bool {
	if err == nil {
		return callCtx.Err() != nil
	}
	// Allowlist of proven no-effect responses may be added here. Until then,
	// treat every post-start forge/git error as uncertain (including 4xx/5xx,
	// EOF, and git transport failures) so clean Release cannot clear grace.
	return true
}

// ErrAmbiguousForgeTransport marks a forge/git failure where the remote effect
// may still complete (no clear settled response).
var ErrAmbiguousForgeTransport = errors.New("stackpublish: ambiguous forge transport; outcome uncertain")

// leasingForge wraps Forge so every call is lease-bounded. Read-only discovery
// under an active session still runs through Do so a lost lease stops the plan.
type leasingForge struct {
	inner Forge
	sess  *Session
}

func (f *leasingForge) ListStackPRs(ctx context.Context, owner, repo, headPrefix string) ([]PR, error) {
	var out []PR
	err := f.sess.Do(ctx, func(cctx context.Context) error {
		var e error
		out, e = f.inner.ListStackPRs(cctx, owner, repo, headPrefix)
		return e
	})
	return out, err
}

func (f *leasingForge) CreatePR(ctx context.Context, owner, repo, head, base, title, body string) (PR, error) {
	var out PR
	err := f.sess.Do(ctx, func(cctx context.Context) error {
		var e error
		out, e = f.inner.CreatePR(cctx, owner, repo, head, base, title, body)
		return e
	})
	return out, err
}

func (f *leasingForge) UpdatePRBase(ctx context.Context, owner, repo string, number int, base string) error {
	return f.sess.Do(ctx, func(cctx context.Context) error {
		return f.inner.UpdatePRBase(cctx, owner, repo, number, base)
	})
}

func (f *leasingForge) ClosePR(ctx context.Context, owner, repo string, number int, comment string) error {
	return f.sess.Do(ctx, func(cctx context.Context) error {
		return f.inner.ClosePR(cctx, owner, repo, number, comment)
	})
}

func (f *leasingForge) PushBranches(ctx context.Context, repoPath string, pushes []BranchPush) error {
	return f.sess.Do(ctx, func(cctx context.Context) error {
		return f.inner.PushBranches(cctx, repoPath, pushes)
	})
}

func (f *leasingForge) QueuedPRNumbers(ctx context.Context, owner, repo string) (map[int]bool, error) {
	var out map[int]bool
	err := f.sess.Do(ctx, func(cctx context.Context) error {
		var e error
		out, e = f.inner.QueuedPRNumbers(cctx, owner, repo)
		return e
	})
	return out, err
}

func (f *leasingForge) PRStatuses(ctx context.Context, owner, repo, headPrefix string) (map[string]PRStatus, error) {
	var out map[string]PRStatus
	err := f.sess.Do(ctx, func(cctx context.Context) error {
		var e error
		out, e = f.inner.PRStatuses(cctx, owner, repo, headPrefix)
		return e
	})
	return out, err
}

func (f *leasingForge) UpdatePRBody(ctx context.Context, owner, repo string, number int, body string) error {
	return f.sess.Do(ctx, func(cctx context.Context) error {
		return f.inner.UpdatePRBody(cctx, owner, repo, number, body)
	})
}
