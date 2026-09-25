package stackstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

// localPublishLockFileName is the flock file inside each per-stack lock dir.
// Shared by manual Publish, Restack, and epic reconcile entrypoints.
const localPublishLockFileName = "publish.lock"

// localPublishMetaFileName persists holder/generation/reuse_after under the
// stable per-stack lock dir so a later same-host process (including after crash)
// cannot enter before grace. Clean Release writes a zero-token / past-reuse
// tombstone (generation kept monotonic); Abandon leaves an active cooldown.
const localPublishMetaFileName = "publish-meta.json"

// localHeldAdmission tracks an in-process flock so Renew/Release can validate
// the token and nested Restack within one Publish does not re-lock.
type localHeldAdmission struct {
	file       *os.File
	token      string
	holder     string
	generation int64
	expiresAt  time.Time
	reuseAfter time.Time
}

// localPublishMeta is the on-disk cooldown / holder record.
type localPublishMeta struct {
	Holder     string    `json:"holder"`
	Generation int64     `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
	ReuseAfter time.Time `json:"reuse_after"`
	TokenHint  string    `json:"token_hint,omitempty"` // first 8 hex chars; not a secret capability
}

var (
	localAdmissionMu sync.Mutex
	localAdmissions  = map[string]*localHeldAdmission{} // lockDir → held

	// testingHookAfterPublishLock, if set, runs after the exclusive flock is
	// acquired and before the authoritative metadata read. Tests only.
	testingHookAfterPublishLock func()
)

// publishLockKey returns a collision-resistant directory name for (ws, stackID).
// SHA-256 of workspace + NUL + stack id avoids lossy sanitization collisions
// across distinct valid IDs/workspaces.
func publishLockKey(ws string, id sl.StackID) string {
	sum := sha256.Sum256([]byte(ws + "\x00" + string(id)))
	return hex.EncodeToString(sum[:])
}

func (s *LocalStore) publishLockDir(ws string, id sl.StackID) (string, error) {
	if s.dir == "" {
		return "", ErrLoomDirMissing
	}
	dir := filepath.Join(s.dir, "stack-publish-locks", publishLockKey(ws, id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("stackstore: create publish lock dir: %w", err)
	}
	return dir, nil
}

func (s *LocalStore) publishMetaPath(lockDir string) string {
	return filepath.Join(lockDir, localPublishMetaFileName)
}

func readPublishMeta(path string) (*localPublishMeta, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path under loom dir
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stackstore: read publish meta: %w", err)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("stackstore: empty publish meta: %w", domain.ErrStackPublishLeaseStoreUnavailable)
	}
	var meta localPublishMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, fmt.Errorf("stackstore: corrupt publish meta: %w", domain.ErrStackPublishLeaseStoreUnavailable)
	}
	if meta.Holder == "" || meta.Generation < 1 || meta.ReuseAfter.IsZero() {
		return nil, fmt.Errorf("stackstore: incomplete publish meta: %w", domain.ErrStackPublishLeaseStoreUnavailable)
	}
	return &meta, nil
}

func writePublishMeta(path string, meta localPublishMeta) error {
	b, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("stackstore: encode publish meta: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("stackstore: write publish meta: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("stackstore: replace publish meta: %w", err)
	}
	return nil
}

// AcquirePublishAdmission takes the per-stack host flock (non-blocking). Busy
// surfaces as a typed retryable error with the recorded holder and retry time.
//
// Cooldown and generation are decided only from metadata read under the
// acquired flock so a concurrent abandon/renew cannot be overwritten by a
// stale pre-lock snapshot.
func (s *LocalStore) AcquirePublishAdmission(_ context.Context, ws string, id sl.StackID, holder string) (*PublishAdmission, error) {
	lockDir, err := s.publishLockDir(ws, id)
	if err != nil {
		return nil, err
	}
	metaPath := s.publishMetaPath(lockDir)

	lockPath := filepath.Join(lockDir, localPublishLockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path under loom dir
	if err != nil {
		return nil, fmt.Errorf("stackstore: open publish lock: %w", err)
	}
	if err := lockfile.TryLockExclusive(f); err != nil {
		_ = f.Close()
		if errors.Is(err, lockfile.ErrLocked) {
			heldMeta, merr := readPublishMeta(metaPath)
			if merr != nil {
				return nil, merr // fail closed rather than invent a synthetic retry
			}
			if heldMeta == nil {
				return nil, fmt.Errorf("stackstore: publish lock held without meta: %w", domain.ErrStackPublishLeaseStoreUnavailable)
			}
			return nil, &domain.StackPublishLeaseBusyError{
				Holder:     heldMeta.Holder,
				Generation: heldMeta.Generation,
				ExpiresAt:  heldMeta.ExpiresAt,
				ReuseAfter: heldMeta.ReuseAfter,
			}
		}
		return nil, fmt.Errorf("stackstore: acquire publish lock: %w", err)
	}

	if testingHookAfterPublishLock != nil {
		testingHookAfterPublishLock()
	}

	// Authoritative cooldown / generation: read only under the held flock.
	meta, err := readPublishMeta(metaPath)
	if err != nil {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
		return nil, err // fail closed on corrupt metadata
	}
	now := time.Now().UTC()
	if meta != nil && now.Before(meta.ReuseAfter) {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
		return nil, &domain.StackPublishLeaseBusyError{
			Holder:     meta.Holder,
			Generation: meta.Generation,
			ExpiresAt:  meta.ExpiresAt,
			ReuseAfter: meta.ReuseAfter,
		}
	}

	token, err := randomToken()
	if err != nil {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
		return nil, err
	}
	expires := now.Add(domain.DefaultStackPublishLeaseTTL)
	reuse := expires.Add(domain.DefaultStackPublishLeaseGrace)

	localAdmissionMu.Lock()
	defer localAdmissionMu.Unlock()
	if existing, held := localAdmissions[lockDir]; held && existing != nil {
		// Same-process re-entry without nested session context: treat as busy.
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
		return nil, &domain.StackPublishLeaseBusyError{
			Holder: existing.holder, Generation: existing.generation,
			ExpiresAt: existing.expiresAt, ReuseAfter: existing.reuseAfter,
		}
	}

	// Monotonic generation from the under-lock record (including past-reuse tombstones).
	var gen int64 = 1
	if meta != nil && meta.Generation > 0 {
		gen = meta.Generation + 1
	}
	admMeta := localPublishMeta{
		Holder: holder, Generation: gen,
		ExpiresAt: expires, ReuseAfter: reuse,
		TokenHint: token[:min(8, len(token))],
	}
	if err := writePublishMeta(metaPath, admMeta); err != nil {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
		return nil, err
	}

	adm := &localHeldAdmission{
		file: f, token: token, holder: holder, generation: gen,
		expiresAt: expires, reuseAfter: reuse,
	}
	localAdmissions[lockDir] = adm

	return &PublishAdmission{
		Token: token, Holder: holder, Generation: gen,
		ExpiresAt: expires, ReuseAfter: reuse,
	}, nil
}

// RenewPublishAdmission extends the local admission window while the flock is held.
func (s *LocalStore) RenewPublishAdmission(_ context.Context, ws string, id sl.StackID, token string) (*PublishAdmission, error) {
	lockDir, err := s.publishLockDir(ws, id)
	if err != nil {
		return nil, err
	}
	localAdmissionMu.Lock()
	defer localAdmissionMu.Unlock()
	held, ok := localAdmissions[lockDir]
	if !ok || held == nil || held.file == nil {
		return nil, fmt.Errorf("stackstore: publish admission not held: %w", domain.ErrStackPublishLeaseLost)
	}
	if held.token != token {
		return nil, fmt.Errorf("stackstore: publish admission token mismatch: %w", domain.ErrStackPublishLeaseTokenMismatch)
	}
	now := time.Now().UTC()
	held.expiresAt = now.Add(domain.DefaultStackPublishLeaseTTL)
	held.reuseAfter = held.expiresAt.Add(domain.DefaultStackPublishLeaseGrace)
	metaPath := s.publishMetaPath(lockDir)
	if err := writePublishMeta(metaPath, localPublishMeta{
		Holder: held.holder, Generation: held.generation,
		ExpiresAt: held.expiresAt, ReuseAfter: held.reuseAfter,
		TokenHint: token[:min(8, len(token))],
	}); err != nil {
		return nil, err
	}
	return &PublishAdmission{
		Token: token, Holder: held.holder, Generation: held.generation,
		ExpiresAt: held.expiresAt, ReuseAfter: held.reuseAfter,
	}, nil
}

// ReleasePublishAdmission drops the per-stack flock and writes a past-reuse
// tombstone so generation stays monotonic while grace is cleared.
// Stale tokens cannot unlock a successor; missing admissions are idempotent success.
func (s *LocalStore) ReleasePublishAdmission(_ context.Context, ws string, id sl.StackID, token string) error {
	return s.endLocalAdmission(ws, id, token, true)
}

// AbandonPublishAdmission drops the flock fd but keeps reuse_after on disk so a
// later process cannot enter before grace. Never leaves the fd held indefinitely.
func (s *LocalStore) AbandonPublishAdmission(_ context.Context, ws string, id sl.StackID, token string) error {
	return s.endLocalAdmission(ws, id, token, false)
}

func (s *LocalStore) endLocalAdmission(ws string, id sl.StackID, token string, clearCooldown bool) error {
	lockDir, err := s.publishLockDir(ws, id)
	if err != nil {
		return err
	}
	localAdmissionMu.Lock()
	defer localAdmissionMu.Unlock()
	held, ok := localAdmissions[lockDir]
	if !ok || held == nil {
		return nil
	}
	if held.token != token {
		return fmt.Errorf("stackstore: publish admission token mismatch: %w", domain.ErrStackPublishLeaseTokenMismatch)
	}
	metaPath := s.publishMetaPath(lockDir)
	if clearCooldown {
		// Past-reuse tombstone: clear grace but keep generation + holder so a
		// successor derives gen+1 and retry metadata stays coherent. Persist
		// failure keeps the flock held (safer than unlocking without a record).
		now := time.Now().UTC()
		past := now.Add(-time.Second)
		if err := writePublishMeta(metaPath, localPublishMeta{
			Holder: held.holder, Generation: held.generation,
			ExpiresAt: past, ReuseAfter: past,
			TokenHint: "",
		}); err != nil {
			return err
		}
	} else {
		// Persist final reuse_after (already written on acquire/renew) so crash
		// / uncertain successors observe the recorded holder and retry time.
		if err := writePublishMeta(metaPath, localPublishMeta{
			Holder: held.holder, Generation: held.generation,
			ExpiresAt: held.expiresAt, ReuseAfter: held.reuseAfter,
			TokenHint: token[:min(8, len(token))],
		}); err != nil {
			return err
		}
	}
	f := held.file
	delete(localAdmissions, lockDir)
	if f != nil {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
	}
	return nil
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("stackstore: mint publish token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

var _ PublishAdmittable = (*LocalStore)(nil)
