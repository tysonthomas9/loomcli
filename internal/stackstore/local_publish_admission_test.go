package stackstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

func TestPublishLockKey_CollisionResistant(t *testing.T) {
	t.Parallel()
	// Distinct IDs that formerly collided under lossy sanitization must differ.
	a := publishLockKey("WS", sl.StackID("epic:E"))
	b := publishLockKey("WS", sl.StackID("epic-E"))
	assert.NotEqual(t, a, b)
	// Workspace is part of the key.
	c := publishLockKey("OTHER", sl.StackID("epic:E"))
	assert.NotEqual(t, a, c)
	assert.Equal(t, 64, len(a), "sha256 hex")
	assert.Equal(t, a, publishLockKey("WS", sl.StackID("epic:E")))
}

func TestLocalPublishAdmission_TwoClientsContend(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	b := New(dir)
	id := sl.StackID("epic:E")
	ctx := context.Background()

	g1, err := a.AcquirePublishAdmission(ctx, "WS", id, "client-a")
	require.NoError(t, err)
	require.NotEmpty(t, g1.Token)

	_, err = b.AcquirePublishAdmission(ctx, "WS", id, "client-b")
	var busy *domain.StackPublishLeaseBusyError
	require.ErrorAs(t, err, &busy)
	assert.Equal(t, "client-a", busy.Holder)
	assert.False(t, busy.RetryAt().IsZero())
	assert.Equal(t, g1.ReuseAfter.UTC(), busy.ReuseAfter.UTC(), "cross-process busy must use recorded reuse_after, not a synthetic hint")
	assert.True(t, errors.Is(err, domain.ErrStackPublishLeaseBusy))

	require.NoError(t, a.ReleasePublishAdmission(ctx, "WS", id, g1.Token))

	g2, err := b.AcquirePublishAdmission(ctx, "WS", id, "client-b")
	require.NoError(t, err)
	require.NoError(t, b.ReleasePublishAdmission(ctx, "WS", id, g2.Token))
}

func TestLocalPublishAdmission_DistinctWorkspaceAndIDNoCollide(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	ctx := context.Background()

	g1, err := s.AcquirePublishAdmission(ctx, "WS-A", sl.StackID("epic:E"), "a")
	require.NoError(t, err)
	g2, err := s.AcquirePublishAdmission(ctx, "WS-B", sl.StackID("epic:E"), "b")
	require.NoError(t, err, "different workspace must not share flock")
	g3, err := s.AcquirePublishAdmission(ctx, "WS-A", sl.StackID("epic-E"), "c")
	require.NoError(t, err, "epic:E and epic-E must not collide")

	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS-A", sl.StackID("epic:E"), g1.Token))
	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS-B", sl.StackID("epic:E"), g2.Token))
	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS-A", sl.StackID("epic-E"), g3.Token))
}

func TestLocalPublishAdmission_StaleTokenCannotRelease(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	id := sl.StackID("epic:E")
	ctx := context.Background()

	g, err := s.AcquirePublishAdmission(ctx, "WS", id, "holder")
	require.NoError(t, err)

	err = s.ReleasePublishAdmission(ctx, "WS", id, "not-the-token")
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseTokenMismatch)

	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS", id, g.Token))
}

func TestLocalPublishAdmission_PublishRestackReconcileShareFlock(t *testing.T) {
	dir := t.TempDir()
	publish := New(dir)
	restack := New(dir)
	reconcile := New(dir)
	id := sl.StackID("epic:shared")
	ctx := context.Background()

	g, err := publish.AcquirePublishAdmission(ctx, "WS", id, "publish")
	require.NoError(t, err)

	_, err = restack.AcquirePublishAdmission(ctx, "WS", id, "restack")
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseBusy)

	_, err = reconcile.AcquirePublishAdmission(ctx, "WS", id, "epic-reconcile")
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseBusy)

	require.NoError(t, publish.ReleasePublishAdmission(ctx, "WS", id, g.Token))

	g2, err := restack.AcquirePublishAdmission(ctx, "WS", id, "restack")
	require.NoError(t, err)
	require.NoError(t, restack.ReleasePublishAdmission(ctx, "WS", id, g2.Token))
}

func TestLocalPublishAdmission_AbandonPersistsCooldown(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	id := sl.StackID("epic:abandon")
	ctx := context.Background()

	g, err := s.AcquirePublishAdmission(ctx, "WS", id, "holder-a")
	require.NoError(t, err)

	lockDir, err := s.publishLockDir("WS", id)
	require.NoError(t, err)
	shortReuse := time.Now().UTC().Add(80 * time.Millisecond)
	localAdmissionMu.Lock()
	if held := localAdmissions[lockDir]; held != nil {
		held.reuseAfter = shortReuse
		held.expiresAt = time.Now().UTC().Add(-time.Second)
	}
	localAdmissionMu.Unlock()

	require.NoError(t, s.AbandonPublishAdmission(ctx, "WS", id, g.Token))

	_, err = s.AcquirePublishAdmission(ctx, "WS", id, "holder-b")
	var busy *domain.StackPublishLeaseBusyError
	require.ErrorAs(t, err, &busy)
	assert.Equal(t, "holder-a", busy.Holder)
	assert.Equal(t, g.Generation, busy.Generation)

	time.Sleep(100 * time.Millisecond)
	g2, err := s.AcquirePublishAdmission(ctx, "WS", id, "holder-b")
	require.NoError(t, err)
	assert.Greater(t, g2.Generation, g.Generation)
	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS", id, g2.Token))
}

func TestLocalPublishAdmission_CrashCooldownSurvivesNewProcess(t *testing.T) {
	dir := t.TempDir()
	id := sl.StackID("epic:crash")
	ctx := context.Background()

	a := New(dir)
	g, err := a.AcquirePublishAdmission(ctx, "WS", id, "crashed-holder")
	require.NoError(t, err)

	lockDir, err := a.publishLockDir("WS", id)
	require.NoError(t, err)
	reuse := time.Now().UTC().Add(150 * time.Millisecond)
	localAdmissionMu.Lock()
	held := localAdmissions[lockDir]
	if held != nil {
		held.reuseAfter = reuse
		held.expiresAt = time.Now().UTC().Add(-time.Second)
	}
	localAdmissionMu.Unlock()
	// Persist cooldown then simulate process death: unlock flock, drop maps,
	// leave meta on disk for the next same-host process.
	require.NoError(t, writePublishMeta(a.publishMetaPath(lockDir), localPublishMeta{
		Holder: "crashed-holder", Generation: g.Generation,
		ExpiresAt: time.Now().UTC().Add(-time.Second), ReuseAfter: reuse,
	}))
	localAdmissionMu.Lock()
	held = localAdmissions[lockDir]
	delete(localAdmissions, lockDir)
	localAdmissionMu.Unlock()
	if held != nil && held.file != nil {
		_ = lockfile.FlockUnlock(held.file)
		_ = held.file.Close()
	}

	b := New(dir) // "new process" same host/dir
	_, err = b.AcquirePublishAdmission(ctx, "WS", id, "successor")
	var busy *domain.StackPublishLeaseBusyError
	require.ErrorAs(t, err, &busy)
	assert.Equal(t, "crashed-holder", busy.Holder)
	assert.Equal(t, reuse.UTC(), busy.ReuseAfter.UTC())

	time.Sleep(180 * time.Millisecond)
	g2, err := b.AcquirePublishAdmission(ctx, "WS", id, "successor")
	require.NoError(t, err)
	assert.Greater(t, g2.Generation, g.Generation)
	require.NoError(t, b.ReleasePublishAdmission(ctx, "WS", id, g2.Token))
}

func TestLocalPublishAdmission_CleanReleaseWritesPastReuseTombstone(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	id := sl.StackID("epic:clean")
	ctx := context.Background()

	g, err := s.AcquirePublishAdmission(ctx, "WS", id, "h")
	require.NoError(t, err)
	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS", id, g.Token))

	lockDir, err := s.publishLockDir("WS", id)
	require.NoError(t, err)
	meta, err := readPublishMeta(s.publishMetaPath(lockDir))
	require.NoError(t, err)
	require.NotNil(t, meta, "clean release must leave a generation tombstone")
	assert.Equal(t, "h", meta.Holder)
	assert.Equal(t, g.Generation, meta.Generation)
	assert.Empty(t, meta.TokenHint, "clean release clears token capability hint")
	assert.False(t, time.Now().UTC().Before(meta.ReuseAfter), "tombstone reuse_after must be past")

	g2, err := s.AcquirePublishAdmission(ctx, "WS", id, "h2")
	require.NoError(t, err)
	assert.Equal(t, g.Generation+1, g2.Generation, "generation must stay monotonic across clean release")
	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS", id, g2.Token))
}

func TestLocalPublishAdmission_AuthoritativeMetaUnderLock(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	id := sl.StackID("epic:under-lock")
	ctx := context.Background()

	reuse := time.Now().UTC().Add(time.Minute)
	testingHookAfterPublishLock = func() {
		lockDir, err := s.publishLockDir("WS", id)
		require.NoError(t, err)
		require.NoError(t, writePublishMeta(s.publishMetaPath(lockDir), localPublishMeta{
			Holder: "prior-abandon", Generation: 7,
			ExpiresAt: time.Now().UTC().Add(-time.Second), ReuseAfter: reuse,
		}))
	}
	t.Cleanup(func() { testingHookAfterPublishLock = nil })

	_, err := s.AcquirePublishAdmission(ctx, "WS", id, "successor")
	var busy *domain.StackPublishLeaseBusyError
	require.ErrorAs(t, err, &busy)
	assert.Equal(t, "prior-abandon", busy.Holder)
	assert.Equal(t, int64(7), busy.Generation)
	assert.Equal(t, reuse.UTC(), busy.ReuseAfter.UTC())
}

func TestLocalPublishAdmission_PostAbandonCooldownThenMonotonicAcquire(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	id := sl.StackID("epic:abandon-gen")
	ctx := context.Background()

	g, err := s.AcquirePublishAdmission(ctx, "WS", id, "holder-a")
	require.NoError(t, err)

	lockDir, err := s.publishLockDir("WS", id)
	require.NoError(t, err)
	shortReuse := time.Now().UTC().Add(80 * time.Millisecond)
	localAdmissionMu.Lock()
	if held := localAdmissions[lockDir]; held != nil {
		held.reuseAfter = shortReuse
		held.expiresAt = time.Now().UTC().Add(-time.Second)
	}
	localAdmissionMu.Unlock()

	require.NoError(t, s.AbandonPublishAdmission(ctx, "WS", id, g.Token))

	_, err = s.AcquirePublishAdmission(ctx, "WS", id, "holder-b")
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseBusy)

	time.Sleep(100 * time.Millisecond)
	g2, err := s.AcquirePublishAdmission(ctx, "WS", id, "holder-b")
	require.NoError(t, err)
	assert.Equal(t, g.Generation+1, g2.Generation)

	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS", id, g2.Token))
	g3, err := s.AcquirePublishAdmission(ctx, "WS", id, "holder-c")
	require.NoError(t, err)
	assert.Equal(t, g2.Generation+1, g3.Generation, "generation continues after abandon then clean release")
	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS", id, g3.Token))
}

func TestLocalPublishAdmission_CorruptMetaFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	id := sl.StackID("epic:corrupt")
	ctx := context.Background()

	lockDir, err := s.publishLockDir("WS", id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(s.publishMetaPath(lockDir), []byte("{not-json"), 0o600))

	_, err = s.AcquirePublishAdmission(ctx, "WS", id, "h")
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseStoreUnavailable)

	// Incomplete meta also fails closed.
	require.NoError(t, os.WriteFile(s.publishMetaPath(lockDir), []byte(`{"holder":"x"}`), 0o600))
	_, err = s.AcquirePublishAdmission(ctx, "WS", id, "h")
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseStoreUnavailable)
}

func TestLocalPublishAdmission_SameProcessReentryBusy(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	id := sl.StackID("epic:reentry")
	ctx := context.Background()

	g, err := s.AcquirePublishAdmission(ctx, "WS", id, "same")
	require.NoError(t, err)
	_, err = s.AcquirePublishAdmission(ctx, "WS", id, "same-again")
	require.ErrorIs(t, err, domain.ErrStackPublishLeaseBusy)
	require.NoError(t, s.ReleasePublishAdmission(ctx, "WS", id, g.Token))
}

func TestLocalPublishAdmission_LockDirUsesKey(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	id := sl.StackID("epic:path")
	lockDir, err := s.publishLockDir("WS", id)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "stack-publish-locks", publishLockKey("WS", id)), lockDir)
}
