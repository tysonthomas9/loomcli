package stackpublish

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/domain"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// admittingStore overlays a PublishAdmittable lease backend on a lineage Store.
type admittingStore struct {
	stackstore.Store
	leases stackstore.PublishAdmittable
}

func (s *admittingStore) AcquirePublishAdmission(ctx context.Context, ws string, id sl.StackID, holder string) (*stackstore.PublishAdmission, error) {
	return s.leases.AcquirePublishAdmission(ctx, ws, id, holder)
}
func (s *admittingStore) RenewPublishAdmission(ctx context.Context, ws string, id sl.StackID, token string) (*stackstore.PublishAdmission, error) {
	return s.leases.RenewPublishAdmission(ctx, ws, id, token)
}
func (s *admittingStore) ReleasePublishAdmission(ctx context.Context, ws string, id sl.StackID, token string) error {
	return s.leases.ReleasePublishAdmission(ctx, ws, id, token)
}
func (s *admittingStore) AbandonPublishAdmission(ctx context.Context, ws string, id sl.StackID, token string) error {
	return s.leases.AbandonPublishAdmission(ctx, ws, id, token)
}

// memLease mirrors FleetDB publish-lease semantics for focused publisher tests.
type memLease struct {
	mu          sync.Mutex
	token       string
	holder      string
	generation  int64
	expiresAt   time.Time
	reuseAfter  time.Time
	unavailable bool
	forceLose   atomic.Bool
	acquires    atomic.Int64
	renews      atomic.Int64
	releases    atomic.Int64
	abandons    atomic.Int64
	onRelease   func()
	onAbandon   func()
}

func (m *memLease) AcquirePublishAdmission(_ context.Context, _ string, _ sl.StackID, holder string) (*stackstore.PublishAdmission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acquires.Add(1)
	if m.unavailable {
		return nil, domain.ErrStackPublishLeaseStoreUnavailable
	}
	now := time.Now().UTC()
	if m.token != "" && now.Before(m.reuseAfter) {
		return nil, &domain.StackPublishLeaseBusyError{
			Holder: m.holder, Generation: m.generation,
			ExpiresAt: m.expiresAt, ReuseAfter: m.reuseAfter,
		}
	}
	m.generation++
	tok := "tok-" + holder + "-" + time.Now().Format("150405.000")
	m.token, m.holder = tok, holder
	m.expiresAt = now.Add(2 * time.Minute)
	m.reuseAfter = m.expiresAt.Add(domain.DefaultStackPublishLeaseGrace)
	return &stackstore.PublishAdmission{
		Token: tok, Holder: holder, Generation: m.generation,
		ExpiresAt: m.expiresAt, ReuseAfter: m.reuseAfter,
	}, nil
}

func (m *memLease) RenewPublishAdmission(_ context.Context, _ string, _ sl.StackID, token string) (*stackstore.PublishAdmission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renews.Add(1)
	if m.unavailable {
		return nil, domain.ErrStackPublishLeaseStoreUnavailable
	}
	if m.forceLose.Load() {
		return nil, domain.ErrStackPublishLeaseLost
	}
	if m.token == "" || m.token != token {
		return nil, domain.ErrStackPublishLeaseTokenMismatch
	}
	now := time.Now().UTC()
	m.expiresAt = now.Add(2 * time.Minute)
	m.reuseAfter = m.expiresAt.Add(domain.DefaultStackPublishLeaseGrace)
	return &stackstore.PublishAdmission{
		Token: m.token, Holder: m.holder, Generation: m.generation,
		ExpiresAt: m.expiresAt, ReuseAfter: m.reuseAfter,
	}, nil
}

func (m *memLease) ReleasePublishAdmission(_ context.Context, _ string, _ sl.StackID, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases.Add(1)
	if m.onRelease != nil {
		defer m.onRelease()
	}
	if m.token == "" {
		return nil
	}
	if m.token != token {
		return domain.ErrStackPublishLeaseTokenMismatch
	}
	// Clean release clears grace (FleetDB semantics).
	m.token, m.holder = "", ""
	m.expiresAt = time.Time{}
	m.reuseAfter = time.Now().UTC().Add(-time.Second)
	return nil
}

func (m *memLease) AbandonPublishAdmission(_ context.Context, _ string, _ sl.StackID, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.abandons.Add(1)
	if m.onAbandon != nil {
		defer m.onAbandon()
	}
	// Leave token + reuseAfter so successors wait out grace (skip clean Release).
	return nil
}

func (m *memLease) crashWithGrace(grace time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.expiresAt = now.Add(-time.Second)
	m.reuseAfter = now.Add(grace)
}

func (m *memLease) held() (token string, reuse time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token, m.reuseAfter
}

type phaseForge struct {
	fakeForge
	mu          sync.Mutex
	phases      []string
	pushHold    chan struct{} // when set, PushBranches waits until closed
	pushStarted chan struct{}
	onPush      func()
	pushErr     error // when set (and no hold), returned from PushBranches
}

func (f *phaseForge) record(p string) {
	f.mu.Lock()
	f.phases = append(f.phases, p)
	f.mu.Unlock()
}

func (f *phaseForge) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.phases))
	copy(out, f.phases)
	return out
}

func (f *phaseForge) ListStackPRs(ctx context.Context, owner, repo, headPrefix string) ([]PR, error) {
	f.record("list")
	return f.fakeForge.ListStackPRs(ctx, owner, repo, headPrefix)
}

func (f *phaseForge) UpdatePRBase(ctx context.Context, owner, repo string, number int, base string) error {
	f.record("reparent")
	return f.fakeForge.UpdatePRBase(ctx, owner, repo, number, base)
}

func (f *phaseForge) PushBranches(ctx context.Context, repoPath string, pushes []BranchPush) error {
	f.record("push")
	if f.pushStarted != nil {
		select {
		case <-f.pushStarted:
		default:
			close(f.pushStarted)
		}
	}
	if f.onPush != nil {
		f.onPush()
	}
	if f.pushHold != nil {
		select {
		case <-f.pushHold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.pushErr != nil {
		return f.pushErr
	}
	return f.fakeForge.PushBranches(ctx, repoPath, pushes)
}

func (f *phaseForge) CreatePR(ctx context.Context, owner, repo, head, base, title, body string) (PR, error) {
	f.record("create")
	return f.fakeForge.CreatePR(ctx, owner, repo, head, base, title, body)
}

func (f *phaseForge) UpdatePRBody(ctx context.Context, owner, repo string, number int, body string) error {
	f.record("body")
	return f.fakeForge.UpdatePRBody(ctx, owner, repo, number, body)
}

func TestPublish_TwoClientsContend(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:contend")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{}
	store := &admittingStore{Store: lineage, leases: leases}

	forgeA := &phaseForge{fakeForge: fakeForge{createPR: PR{Number: 1, URL: "u1", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}}}
	hold := make(chan struct{})
	started := make(chan struct{})
	forgeA.pushHold = hold
	forgeA.pushStarted = started

	recA := &Reconciler{Store: store, Forge: forgeA, Holder: "client-a"}
	recB := &Reconciler{Store: store, Forge: &phaseForge{fakeForge: fakeForge{}}, Holder: "client-b"}

	errCh := make(chan error, 1)
	go func() {
		_, err := recA.Publish(ctx, "WS", id, dir, Options{})
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("publish A did not start push")
	}

	_, err := recB.Publish(ctx, "WS", id, dir, Options{})
	var busy *domain.StackPublishLeaseBusyError
	require.ErrorAs(t, err, &busy)
	assert.Equal(t, "client-a", busy.Holder)
	assert.False(t, busy.RetryAt().IsZero())

	close(hold)
	require.NoError(t, <-errCh)
}

func TestPublish_VsEpicReconcileContend(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:vs")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{}
	store := &admittingStore{Store: lineage, leases: leases}

	hold := make(chan struct{})
	started := make(chan struct{})
	manual := &Reconciler{
		Store: store, Holder: "manual-publish",
		Forge: &phaseForge{
			fakeForge:   fakeForge{createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}},
			pushHold:    hold,
			pushStarted: started,
		},
	}
	epic := &Reconciler{Store: store, Holder: "epic-reconcile", Forge: &phaseForge{fakeForge: fakeForge{}}}

	done := make(chan error, 1)
	go func() {
		_, err := manual.Publish(ctx, "WS", id, dir, Options{})
		done <- err
	}()
	<-started

	_, err := epic.Publish(ctx, "WS", id, dir, Options{})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseBusy)

	close(hold)
	require.NoError(t, <-done)
}

func TestPublish_LeaseStoreOutageFailsClosed(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:outage")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{unavailable: true}
	store := &admittingStore{Store: lineage, leases: leases}
	forge := &phaseForge{fakeForge: fakeForge{}}
	rec := &Reconciler{Store: store, Forge: forge}

	_, err := rec.Publish(ctx, "WS", id, dir, Options{})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseStoreUnavailable)
	assert.Empty(t, forge.snapshot(), "no forge calls when lease store unavailable")
}

func TestPublish_DryRunSkipsAdmission(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:dry")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{unavailable: true} // would fail if acquired
	store := &admittingStore{Store: lineage, leases: leases}
	forge := &phaseForge{fakeForge: fakeForge{}}
	rec := &Reconciler{Store: store, Forge: forge}

	rep, err := rec.Publish(ctx, "WS", id, dir, Options{DryRun: true})
	require.NoError(t, err)
	assert.True(t, rep.DryRun)
	assert.Equal(t, int64(0), leases.acquires.Load())
}

func TestPublish_LeaseLossMidCallStopsLaterPhases(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:loss")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{}
	store := &admittingStore{Store: lineage, leases: leases}

	hold := make(chan struct{})
	started := make(chan struct{})
	forge := &phaseForge{
		fakeForge: fakeForge{createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}},
		pushHold:  hold, pushStarted: started,
	}

	rec := &Reconciler{
		Store: store, Forge: forge, Holder: "loser",
		AdmissionRenewInterval: 15 * time.Millisecond,
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := rec.Publish(ctx, "WS", id, dir, Options{})
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("push did not start")
	}
	// Force lease loss while the forge call is in flight; renew loop cancels
	// the session context so the held push observes cancellation and later
	// phases never start.
	leases.forceLose.Store(true)

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, domain.ErrStackPublishLeaseLost)
	case <-time.After(5 * time.Second):
		close(hold)
		t.Fatal("publish did not exit after mid-call lease loss")
	}

	phases := forge.snapshot()
	assert.Contains(t, phases, "push")
	assert.NotContains(t, phases, "create", "must not start phase4 after lease loss mid-push")
	assert.NotContains(t, phases, "body")
	assert.Equal(t, int64(0), leases.releases.Load(), "must skip clean Release after mid-call lease loss")
	assert.Equal(t, int64(1), leases.abandons.Load())
	tok, reuse := leases.held()
	assert.NotEmpty(t, tok, "grace must remain for successor")
	assert.True(t, reuse.After(time.Now().UTC().Add(-time.Second)))
}

func TestPublish_CrashAfterPushSuccessorWaitsAndReplans(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:crash")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{}
	store := &admittingStore{Store: lineage, leases: leases}

	// Simulate crash-after-push: prior holder left the key inside reuse_after grace.
	g, err := leases.AcquirePublishAdmission(ctx, "WS", id, "crashed")
	require.NoError(t, err)
	_ = g
	leases.crashWithGrace(80 * time.Millisecond)

	rec := &Reconciler{
		Store: store, Holder: "successor",
		Forge: &phaseForge{fakeForge: fakeForge{
			createPR: PR{Number: 9, URL: "https://github.com/o/r/pull/9", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"},
		}},
	}

	_, err = rec.Publish(ctx, "WS", id, dir, Options{})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseBusy)

	time.Sleep(100 * time.Millisecond)

	rep, err := rec.Publish(ctx, "WS", id, dir, Options{})
	require.NoError(t, err)
	require.Equal(t, []string{"T1"}, rep.Created)
	phases := rec.Forge.(*phaseForge).snapshot()
	assert.Contains(t, phases, "list", "successor must replan from live GitHub before Phase 1")
	assert.Equal(t, 1, countPhase(phases, "create"), "no duplicate open PR create")
}

func countPhase(phases []string, name string) int {
	n := 0
	for _, p := range phases {
		if p == name {
			n++
		}
	}
	return n
}

func TestPublish_NestedRestackNoDeadlock(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:nested")
	dir, lineage := repoWithFileOrigin(t, ctx, id, "T1", "feat (T1)")
	// Local flock path: nested Restack must reuse outer admission.
	store := lineage
	forge := &phaseForge{fakeForge: fakeForge{
		createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"},
	}}
	rec := &Reconciler{Store: store, Forge: forge, Holder: "nested"}

	// Resolver present triggers nested Restack inside Publish; with one unit and
	// no merged predecessor Restack is a no-op but still enters withAdmission.
	done := make(chan error, 1)
	go func() {
		_, err := rec.Publish(ctx, "WS", id, dir, Options{Resolver: nopResolver{}})
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("nested Restack deadlocked on publish admission")
	}
}

type nopResolver struct{}

func (nopResolver) ResolveRebaseConflicts(context.Context, string, string, string, []string) error {
	return errors.New("unexpected conflict")
}

func TestLocalFlock_PublishAndRestackParity(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:flock")
	dir, store := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")

	hold := make(chan struct{})
	started := make(chan struct{})
	pub := &Reconciler{
		Store: store, Holder: "publish",
		Forge: &phaseForge{
			fakeForge:   fakeForge{createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}},
			pushHold:    hold,
			pushStarted: started,
		},
	}
	restackRec := &Reconciler{Store: store, Holder: "restack", Forge: &phaseForge{fakeForge: fakeForge{}}}

	done := make(chan error, 1)
	go func() {
		_, err := pub.Publish(ctx, "WS", id, dir, Options{})
		done <- err
	}()
	<-started

	_, err := restackRec.Restack(ctx, "WS", id, dir, nil)
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseBusy)

	close(hold)
	require.NoError(t, <-done)
}

func TestSession_CallDeadlineRespectsSkewAndMaxBound(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	sess := &Session{now: func() time.Time { return now }}

	sess.mu.Lock()
	sess.expiresAt = now.Add(5 * time.Minute)
	sess.mu.Unlock()
	assert.Equal(t, domain.MaxStackPublishCallBound, sess.callDeadline(), "cap at max forge call bound")

	sess.mu.Lock()
	sess.expiresAt = now.Add(domain.StackPublishClockSkew + 12*time.Second)
	sess.mu.Unlock()
	assert.Equal(t, 12*time.Second, sess.callDeadline(), "remaining minus skew")

	sess.mu.Lock()
	sess.expiresAt = now.Add(domain.StackPublishClockSkew)
	sess.mu.Unlock()
	assert.Equal(t, time.Duration(0), sess.callDeadline(), "no room after skew")
}

func TestSession_ReleaseWaitsForInFlight(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:settle")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")

	released := make(chan struct{})
	hold := make(chan struct{})
	started := make(chan struct{})
	leases := &memLease{
		onRelease: func() { close(released) },
	}
	store := &admittingStore{Store: lineage, leases: leases}
	forge := &phaseForge{
		fakeForge:   fakeForge{createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}},
		pushHold:    hold,
		pushStarted: started,
	}
	rec := &Reconciler{Store: store, Forge: forge, Holder: "settle"}

	errCh := make(chan error, 1)
	go func() {
		_, err := rec.Publish(ctx, "WS", id, dir, Options{})
		errCh <- err
	}()
	<-started

	select {
	case <-released:
		t.Fatal("released lease while forge call still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(hold)
	require.NoError(t, <-errCh)

	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("lease was not released after forge calls settled")
	}
	assert.Equal(t, int64(1), leases.releases.Load())
	assert.Equal(t, int64(0), leases.abandons.Load())
}

func TestSession_DoStopsWhenLeaseLost(t *testing.T) {
	ctx := context.Background()
	leases := &memLease{}
	grant, err := leases.AcquirePublishAdmission(ctx, "WS", sl.StackID("epic:do"), "t")
	require.NoError(t, err)

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sess := &Session{
		ws: "WS", id: "epic:do", store: leases,
		token: grant.Token, cancel: cancel,
		expiresAt: grant.ExpiresAt, now: time.Now,
		stopRenew: make(chan struct{}), renewDone: make(chan struct{}),
	}
	close(sess.renewDone) // no renew loop

	leases.forceLose.Store(true)
	err = sess.Do(sessCtx, func(context.Context) error {
		t.Fatal("must not start forge call after renew failure")
		return nil
	})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseLost)
	assert.True(t, sess.Lost())
	assert.True(t, sess.Uncertain(), "renew failure marks outcome uncertain")
}

func TestPublish_SkipCleanReleaseOnTimeout(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:timeout")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{}
	store := &admittingStore{Store: lineage, leases: leases}
	forge := &phaseForge{
		fakeForge: fakeForge{createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}},
		pushErr:   context.DeadlineExceeded,
	}
	rec := &Reconciler{Store: store, Forge: forge, Holder: "timeout"}

	_, err := rec.Publish(ctx, "WS", id, dir, Options{})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseLost)
	assert.Equal(t, int64(0), leases.releases.Load(), "timeout must skip clean Release")
	assert.Equal(t, int64(1), leases.abandons.Load())
	tok, _ := leases.held()
	assert.NotEmpty(t, tok)

	_, err = rec.Publish(ctx, "WS", id, dir, Options{})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseBusy, "successor must wait/replan while grace remains")
}

func TestPublish_SkipCleanReleaseOnCallerCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	id := sl.StackID("epic:cancel")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{}
	store := &admittingStore{Store: lineage, leases: leases}
	hold := make(chan struct{})
	started := make(chan struct{})
	forge := &phaseForge{
		fakeForge:   fakeForge{createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}},
		pushHold:    hold,
		pushStarted: started,
	}
	rec := &Reconciler{Store: store, Forge: forge, Holder: "cancel"}

	errCh := make(chan error, 1)
	go func() {
		_, err := rec.Publish(ctx, "WS", id, dir, Options{})
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("push did not start")
	}
	cancel()
	select {
	case err := <-errCh:
		require.Error(t, err)
		require.ErrorIs(t, err, domain.ErrStackPublishLeaseLost)
	case <-time.After(5 * time.Second):
		close(hold)
		t.Fatal("publish did not exit after cancel")
	}
	assert.Equal(t, int64(0), leases.releases.Load())
	assert.Equal(t, int64(1), leases.abandons.Load())
}

func TestPublish_SkipCleanReleaseOnAmbiguousTransport(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:ambig")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{}
	store := &admittingStore{Store: lineage, leases: leases}
	forge := &phaseForge{
		fakeForge: fakeForge{createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}},
		pushErr:   fmt.Errorf("push: %w", ErrAmbiguousForgeTransport),
	}
	rec := &Reconciler{Store: store, Forge: forge, Holder: "ambig"}

	_, err := rec.Publish(ctx, "WS", id, dir, Options{})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseLost)
	assert.Equal(t, int64(0), leases.releases.Load())
	assert.Equal(t, int64(1), leases.abandons.Load())
}

func TestPublish_SuccessSettlesBeforeRelease(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:ok-release")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	order := make(chan string, 4)
	leases := &memLease{
		onRelease: func() { order <- "release" },
	}
	store := &admittingStore{Store: lineage, leases: leases}
	forge := &phaseForge{
		fakeForge: fakeForge{createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}},
		onPush:    func() { order <- "push-done" },
	}
	rec := &Reconciler{Store: store, Forge: forge, Holder: "ok"}

	_, err := rec.Publish(ctx, "WS", id, dir, Options{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), leases.releases.Load())
	assert.Equal(t, int64(0), leases.abandons.Load())
	tok, _ := leases.held()
	assert.Empty(t, tok, "clean release clears lease")

	require.Equal(t, "push-done", <-order)
	require.Equal(t, "release", <-order)
}

func TestForgeOutcomeUncertain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.True(t, forgeOutcomeUncertain(context.DeadlineExceeded, context.Background()))
	assert.True(t, forgeOutcomeUncertain(context.Canceled, ctx))
	assert.True(t, forgeOutcomeUncertain(ErrAmbiguousForgeTransport, context.Background()))
	assert.True(t, forgeOutcomeUncertain(errors.New("github 422 validation"), context.Background()),
		"4xx is not a proven no-effect allowlist entry; preserve grace")
	assert.True(t, forgeOutcomeUncertain(io.EOF, context.Background()))
	assert.True(t, forgeOutcomeUncertain(errors.New("github 500 Internal Server Error"), context.Background()))
	assert.True(t, forgeOutcomeUncertain(errors.New("github 502 Bad Gateway"), context.Background()))
	assert.True(t, forgeOutcomeUncertain(errors.New("git push: fatal: unable to access 'https://github.com/...': Could not resolve host"), context.Background()))
	assert.False(t, forgeOutcomeUncertain(nil, context.Background()))

	expired, expCancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer expCancel()
	<-expired.Done()
	assert.True(t, forgeOutcomeUncertain(nil, expired), "nil error with expired call context must preserve grace")
	assert.True(t, forgeOutcomeUncertain(nil, ctx), "nil error with cancelled call context must preserve grace")
}

func TestPublish_SkipCleanReleaseOnGitHub422(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:422")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	leases := &memLease{}
	store := &admittingStore{Store: lineage, leases: leases}
	forge := &phaseForge{
		fakeForge: fakeForge{createPR: PR{Number: 1, URL: "u", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}},
		pushErr:   errors.New("github 422 validation failed"),
	}
	rec := &Reconciler{Store: store, Forge: forge, Holder: "422"}

	_, err := rec.Publish(ctx, "WS", id, dir, Options{})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseLost)
	assert.ErrorContains(t, err, "422")
	assert.Equal(t, int64(0), leases.releases.Load())
	assert.Equal(t, int64(1), leases.abandons.Load())
}

func TestSession_RenewRejectsMismatchedGrant(t *testing.T) {
	ctx := context.Background()
	leases := &mutatingRenewLease{memLease: memLease{}}
	grant, err := leases.AcquirePublishAdmission(ctx, "WS", sl.StackID("epic:mismatch"), "t")
	require.NoError(t, err)

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sess := &Session{
		ws: "WS", id: "epic:mismatch", store: leases,
		token: grant.Token, generation: grant.Generation, cancel: cancel,
		expiresAt: grant.ExpiresAt, now: time.Now,
		stopRenew: make(chan struct{}), renewDone: make(chan struct{}),
	}
	close(sess.renewDone)

	leases.rewriteToken = "stolen-token"
	err = sess.Renew(sessCtx)
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseLost)
	assert.True(t, sess.Lost())
	assert.True(t, sess.Uncertain())
	assert.Equal(t, grant.Token, sess.token, "session token must stay immutable")
	assert.Equal(t, grant.Generation, sess.generation)
	assert.NotContains(t, err.Error(), grant.Token, "acquired lease token must not appear in error text")
	assert.NotContains(t, err.Error(), "stolen-token", "returned lease token must not appear in error text")
	assert.ErrorContains(t, err, "generation")
}

func TestSession_ConcurrentRenewKeepsTokenStable(t *testing.T) {
	ctx := context.Background()
	leases := &memLease{}
	grant, err := leases.AcquirePublishAdmission(ctx, "WS", sl.StackID("epic:concurrent"), "t")
	require.NoError(t, err)

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sess := &Session{
		ws: "WS", id: "epic:concurrent", store: leases,
		token: grant.Token, generation: grant.Generation, cancel: cancel,
		expiresAt: grant.ExpiresAt, reuseAfter: grant.ReuseAfter, now: time.Now,
		stopRenew: make(chan struct{}), renewDone: make(chan struct{}),
	}
	close(sess.renewDone)

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- sess.Renew(sessCtx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, grant.Token, sess.token)
	assert.Equal(t, grant.Generation, sess.generation)
	assert.False(t, sess.Lost())
	assert.True(t, sess.ExpiresAt().After(grant.ExpiresAt.Add(-time.Second)))
}

func TestWithAdmission_RejectsCrossStackNested(t *testing.T) {
	ctx := context.Background()
	leases := &memLease{}
	store := &admittingStore{Store: stackstore.New(t.TempDir()), leases: leases}
	rec := &Reconciler{Store: store, Holder: "nested-cross"}

	outer := sl.StackID("epic:outer")
	inner := sl.StackID("epic:inner")
	err := rec.withAdmission(ctx, "WS", outer, func(ctx context.Context, _ *Session) error {
		return rec.withAdmission(ctx, "WS", inner, func(context.Context, *Session) error {
			t.Fatal("must not reuse outer session for a different stack")
			return nil
		})
	})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseStoreUnavailable)
	assert.ErrorContains(t, err, "epic:inner")
	assert.Equal(t, int64(1), leases.acquires.Load(), "inner must not acquire")
	assert.Equal(t, int64(1), leases.releases.Load(), "outer clean release after nested reject")
}

// mutatingRenewLease returns a rewritten token/generation on Renew for mismatch tests.
type mutatingRenewLease struct {
	memLease
	rewriteToken string
	rewriteGen   int64
}

func (m *mutatingRenewLease) RenewPublishAdmission(ctx context.Context, ws string, id sl.StackID, token string) (*stackstore.PublishAdmission, error) {
	grant, err := m.memLease.RenewPublishAdmission(ctx, ws, id, token)
	if err != nil || grant == nil {
		return grant, err
	}
	if m.rewriteToken != "" {
		cp := *grant
		cp.Token = m.rewriteToken
		if m.rewriteGen != 0 {
			cp.Generation = m.rewriteGen
		}
		return &cp, nil
	}
	return grant, nil
}

func TestRestack_RejectsCrossStackNested(t *testing.T) {
	ctx := context.Background()
	leases := &memLease{}
	outer := sl.StackID("epic:outer")
	inner := sl.StackID("epic:inner")
	dir, lineage := repoWithOwnedCommit(t, ctx, outer, "T1", "feat (T1)")
	store := &admittingStore{Store: lineage, leases: leases}
	rec := &Reconciler{Store: store, Forge: &phaseForge{fakeForge: fakeForge{}}, Holder: "restack-cross"}

	err := rec.withAdmission(ctx, "WS", outer, func(ctx context.Context, _ *Session) error {
		_, rerr := rec.Restack(ctx, "WS", inner, dir, nil)
		return rerr
	})
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseStoreUnavailable)
	assert.ErrorContains(t, err, "epic:inner")
	assert.Equal(t, int64(1), leases.acquires.Load(), "cross-stack Restack must not acquire")
	assert.Equal(t, int64(1), leases.releases.Load(), "outer clean release after nested reject")
	assert.Equal(t, int64(0), leases.abandons.Load())
}

func TestRestack_SameKeyNestedReusesOuterSession(t *testing.T) {
	ctx := context.Background()
	leases := &memLease{}
	id := sl.StackID("epic:same-nested")
	dir, lineage := repoWithFileOrigin(t, ctx, id, "T1", "feat (T1)")
	store := &admittingStore{Store: lineage, leases: leases}
	forge := &phaseForge{fakeForge: fakeForge{}}
	rec := &Reconciler{Store: store, Forge: forge, Holder: "restack-same"}

	err := rec.withAdmission(ctx, "WS", id, func(ctx context.Context, sess *Session) error {
		// Mimic Publish: one leasingForge on the outer session.
		inner := *rec
		inner.Forge = &leasingForge{inner: forge, sess: sess}
		_, rerr := inner.Restack(ctx, "WS", id, dir, nil)
		return rerr
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), leases.acquires.Load(), "nested Restack must reuse outer lease")
	assert.Equal(t, int64(1), leases.releases.Load())
	assert.Equal(t, int64(0), leases.abandons.Load())
}

func TestSession_LocalRebaseConflictReleasesClean(t *testing.T) {
	ctx := context.Background()
	leases := &memLease{}
	id := sl.StackID("epic:rebase-local")
	store := &admittingStore{Store: stackstore.New(t.TempDir()), leases: leases}
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	rec := &Reconciler{Store: store, Forge: &phaseForge{fakeForge: fakeForge{}}, Holder: "rebase-local"}

	dir, t1tip := conflictRepo(t)
	res := &fakeResolver{resolveTo: "RESOLVED\n"}

	err := rec.withAdmission(ctx, "WS", id, func(ctx context.Context, sess *Session) error {
		resolved, rerr := rec.rebaseOnto(ctx, dir, "loom/stack/s/T2", "main", t1tip, res)
		require.NoError(t, rerr)
		assert.True(t, resolved)
		assert.GreaterOrEqual(t, res.calls, 1)
		assert.False(t, sess.Uncertain(), "rebase conflict is a settled local exit")
		assert.False(t, sess.Lost())
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), leases.releases.Load(), "known local rebase outcome must clean-release")
	assert.Equal(t, int64(0), leases.abandons.Load())
}

func TestSession_MissingRefEmptinessSoftContinueReleasesClean(t *testing.T) {
	ctx := context.Background()
	leases := &memLease{}
	id := sl.StackID("epic:missing-ref")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	store := &admittingStore{Store: lineage, leases: leases}
	rec := &Reconciler{Store: store, Forge: &phaseForge{fakeForge: fakeForge{}}, Holder: "missing-ref"}

	err := rec.withAdmission(ctx, "WS", id, func(ctx context.Context, sess *Session) error {
		_, cerr := commitsBetween(ctx, dir, "main", "loom/stack/"+string(id)+"/MISSING")
		require.Error(t, cerr, "missing local ref must surface as git error")
		assert.False(t, sess.Uncertain(), "missing-ref emptiness soft-continue must not poison admission")
		assert.False(t, sess.Lost())
		assert.False(t, errors.Is(cerr, domain.ErrStackPublishLeaseLost))
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), leases.releases.Load())
	assert.Equal(t, int64(0), leases.abandons.Load())
}

func TestSession_DirectGitPushUnderSessionAbandons(t *testing.T) {
	ctx := context.Background()
	leases := &memLease{}
	id := sl.StackID("epic:push-do")
	dir, lineage := repoWithOwnedCommit(t, ctx, id, "T1", "feat (T1)")
	store := &admittingStore{Store: lineage, leases: leases}
	rec := &Reconciler{Store: store, Forge: &phaseForge{fakeForge: fakeForge{}}, Holder: "push-do"}

	err := rec.withAdmission(ctx, "WS", id, func(ctx context.Context, sess *Session) error {
		// Nonexistent remote: fail closed immediately with no network I/O while
		// still exercising the push verb → Session.Do conservative Abandon.
		_, perr := runGit(ctx, dir, nil, "push", "no-such-remote", "main")
		require.Error(t, perr)
		assert.True(t, sess.Uncertain(), "git push unknown outcome must use Session.Do")
		return perr
	})
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseLost)
	assert.Equal(t, int64(0), leases.releases.Load())
	assert.Equal(t, int64(1), leases.abandons.Load())
}

func TestGitInvocationIsPush(t *testing.T) {
	assert.True(t, gitInvocationIsPush([]string{"push", "origin", "main"}))
	assert.True(t, gitInvocationIsPush([]string{"-c", "credential.helper=", "push", "--atomic", "origin"}))
	assert.False(t, gitInvocationIsPush([]string{"rebase", "--onto", "a", "b", "c"}))
	assert.False(t, gitInvocationIsPush([]string{"rev-list", "--count", "main..head"}))
	assert.False(t, gitInvocationIsPush([]string{"-c", "foo=bar", "fetch", "origin", "main"}))
}

// repoWithFileOrigin is like repoWithOwnedCommit but points origin at a local
// bare repo whose file:// URL ends in github.com/o/r.git. Restack's fetchRef
// succeeds offline, and `git remote get-url origin` still matches repoSlug
// (unlike url.*.insteadOf, which get-url expands away from the GitHub form).
func repoWithFileOrigin(t *testing.T, ctx context.Context, id sl.StackID, taskID, subject string) (string, stackstore.Store) {
	t.Helper()
	dir, store := repoWithOwnedCommit(t, ctx, id, taskID, subject)
	root := t.TempDir()
	origin := filepath.Join(root, "github.com", "o", "r.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(origin), 0o755))
	git(t, root, "init", "-q", "--bare", "--initial-branch=main", origin)
	git(t, dir, "remote", "set-url", "origin", "file://"+origin)
	git(t, dir, "push", "-q", "origin", "main")
	branch := sl.OutputBranchName(id, taskID)
	git(t, dir, "push", "-q", "origin", branch)
	return dir, store
}
