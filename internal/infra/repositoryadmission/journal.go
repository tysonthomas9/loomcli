package repositoryadmissioninfra

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/repositoryadmission"
	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

const (
	localJournalVersion                    = 1
	localJournalDirName                    = "repository-admissions"
	localJournalFileName                   = "journal.json"
	localRepositoryAdmissionLockDirName    = "materialization-locks"
	maxLocalRepositoryAdmissionOperationID = 200
	repositoryAdmissionLockRetryInterval   = 20 * time.Millisecond
)

type LocalIntent = repositoryadmission.LocalIntent
type LocalRecord = repositoryadmission.LocalRecord
type Coordinate = repositoryadmission.Coordinate

const (
	KindCreateWorkspace = repositoryadmission.KindCreateWorkspace
	KindAddRepositories = repositoryadmission.KindAddRepositories
)

var (
	ErrLocalNotFound = repositoryadmission.ErrLocalNotFound
	ErrLocalConflict = repositoryadmission.ErrLocalConflict
)

type localJournalFile struct {
	Version int                    `json:"version"`
	Records map[string]LocalRecord `json:"records"`
}

type repositoryAdmissionMaterializationAuthority struct {
	coordinate Coordinate
	deadline   time.Time
}

// RepositoryAdmissionJournal is deliberately separate from state.json.
// StateCache is a regenerable UI/local-path hint; this journal is load-bearing
// recovery state and therefore has its own schema, lock, and 0600 atomic file.
type RepositoryAdmissionJournal struct {
	dir string
	now func() time.Time

	authorityMu sync.Mutex
	authorities map[string]repositoryAdmissionMaterializationAuthority
	// monotonicNow must remain independent from the injectable durable journal
	// clock. Production time.Now values retain their monotonic component, so
	// host wall-clock skew cannot mint or extend local materialization authority.
	monotonicNow func() time.Time
}

type repositoryAdmissionMaterializationLock struct {
	files []*os.File
	once  sync.Once
}

func NewRepositoryAdmissionJournal(root string) (*RepositoryAdmissionJournal, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("repository admission journal: Loom directory is unavailable")
	}
	return NewRepositoryAdmissionJournalAt(
		filepath.Join(root, localJournalDirName),
		time.Now,
	)
}

func NewRepositoryAdmissionJournalAt(
	dir string,
	now func() time.Time,
) (*RepositoryAdmissionJournal, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." || !filepath.IsAbs(dir) {
		return nil, errors.New("repository admission journal: absolute directory is required")
	}
	if now == nil {
		return nil, errors.New("repository admission journal: clock is required")
	}
	return &RepositoryAdmissionJournal{
		dir:          dir,
		now:          now,
		authorities:  make(map[string]repositoryAdmissionMaterializationAuthority),
		monotonicNow: time.Now,
	}, nil
}

func validRepositoryAdmissionMaterializationCoordinate(
	coordinate Coordinate,
) bool {
	return strings.TrimSpace(coordinate.WorkspaceKey) != "" &&
		strings.TrimSpace(coordinate.AdmissionID) != "" &&
		strings.TrimSpace(coordinate.OperationID) != "" &&
		strings.TrimSpace(coordinate.OwnerID) != "" &&
		strings.TrimSpace(coordinate.OwnerGenerationID) != "" &&
		strings.TrimSpace(coordinate.SpecFingerprint) != ""
}

func (journal *RepositoryAdmissionJournal) ActivateMaterializationAuthority(
	coordinate Coordinate,
	deadline time.Time,
) error {
	if journal == nil ||
		journal.monotonicNow == nil ||
		!validRepositoryAdmissionMaterializationCoordinate(coordinate) ||
		deadline.IsZero() {
		return fmt.Errorf(
			"repository admission materialization authority is invalid: %w",
			repositoryadmission.ErrInvalid,
		)
	}
	journal.authorityMu.Lock()
	defer journal.authorityMu.Unlock()
	if !deadline.After(journal.monotonicNow()) {
		return fmt.Errorf(
			"repository admission materialization authority is expired: %w",
			repositoryadmission.ErrInvalid,
		)
	}
	if current, exists := journal.authorities[coordinate.AdmissionID]; exists &&
		current.coordinate != coordinate {
		return fmt.Errorf(
			"repository admission %q has a different active owner generation: %w",
			coordinate.AdmissionID,
			repositoryadmission.ErrInvalid,
		)
	}
	journal.authorities[coordinate.AdmissionID] =
		repositoryAdmissionMaterializationAuthority{
			coordinate: coordinate,
			deadline:   deadline,
		}
	return nil
}

func (journal *RepositoryAdmissionJournal) RenewMaterializationAuthority(
	coordinate Coordinate,
	deadline time.Time,
) bool {
	if journal == nil ||
		journal.monotonicNow == nil ||
		!validRepositoryAdmissionMaterializationCoordinate(coordinate) ||
		deadline.IsZero() {
		return false
	}
	journal.authorityMu.Lock()
	defer journal.authorityMu.Unlock()
	current, exists := journal.authorities[coordinate.AdmissionID]
	if !exists || current.coordinate != coordinate {
		return false
	}
	if !deadline.After(journal.monotonicNow()) {
		delete(journal.authorities, coordinate.AdmissionID)
		return false
	}
	// The newest exact Fleet grant supersedes the prior grant even when its
	// server-relative window is shorter.
	current.deadline = deadline
	journal.authorities[coordinate.AdmissionID] = current
	return true
}

func (journal *RepositoryAdmissionJournal) DeactivateMaterializationAuthority(
	coordinate Coordinate,
) {
	if journal == nil || strings.TrimSpace(coordinate.AdmissionID) == "" {
		return
	}
	journal.authorityMu.Lock()
	if current, exists := journal.authorities[coordinate.AdmissionID]; exists &&
		current.coordinate == coordinate {
		delete(journal.authorities, coordinate.AdmissionID)
	}
	journal.authorityMu.Unlock()
}

func (journal *RepositoryAdmissionJournal) DeactivateAllMaterializationAuthorities() {
	if journal == nil {
		return
	}
	journal.authorityMu.Lock()
	clear(journal.authorities)
	journal.authorityMu.Unlock()
}

func (journal *RepositoryAdmissionJournal) materializationAuthorityDeadline(
	admissionID string,
) (time.Time, bool) {
	if journal == nil {
		return time.Time{}, false
	}
	journal.authorityMu.Lock()
	defer journal.authorityMu.Unlock()
	authority, active := journal.authorities[admissionID]
	return authority.deadline, active
}

func (journal *RepositoryAdmissionJournal) materializationAuthorityCount() int {
	if journal == nil {
		return 0
	}
	journal.authorityMu.Lock()
	defer journal.authorityMu.Unlock()
	return len(journal.authorities)
}

// acquireMaterializationLock serializes one durable admission and every local
// checkout target it owns across serve processes. The admission lock prevents
// two owner generations of the same process from overlapping; target locks
// additionally prevent unrelated admissions from entering the same canonical
// machine-local path if an upstream reservation invariant regresses.
//
//nolint:funlen // The lock acquisition must keep path containment, in-process exclusion, and OS-lock cleanup in one critical section.
func (journal *RepositoryAdmissionJournal) AcquireMaterializationLock(
	ctx context.Context,
	admissionID string,
	targets []string,
) (func(), error) {
	if journal == nil || ctx == nil {
		return nil, errors.New("repository admission materialization lock is unavailable")
	}
	admissionID, err := repositoryadmission.NormalizeID(admissionID)
	if err != nil {
		return nil, err
	}
	keys := []string{"admission-" + admissionID}
	for _, target := range targets {
		canonical, canonicalErr := canonicalMaterializationLockTarget(target)
		if canonicalErr != nil {
			return nil, canonicalErr
		}
		digest := sha256.Sum256([]byte(canonical))
		keys = append(keys, "target-"+hex.EncodeToString(digest[:]))
	}
	sort.Strings(keys)
	keys = slices.Compact(keys)

	lockDir := filepath.Join(journal.dir, localRepositoryAdmissionLockDirName)
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, fmt.Errorf("repository admission materialization lock: create directory: %w", err)
	}
	if err := os.Chmod(lockDir, 0o700); err != nil { //nolint:gosec // A private directory needs owner execute permission to contain the lock.
		return nil, fmt.Errorf("repository admission materialization lock: protect directory: %w", err)
	}
	held := &repositoryAdmissionMaterializationLock{}
	release := held.release
	for _, key := range keys {
		file, openErr := os.OpenFile( //nolint:gosec // key is a fixed prefix plus validated hex.
			filepath.Join(lockDir, key+".lock"),
			os.O_CREATE|os.O_RDWR,
			0o600,
		)
		if openErr != nil {
			release()
			return nil, fmt.Errorf("repository admission materialization lock: open: %w", openErr)
		}
		if chmodErr := file.Chmod(0o600); chmodErr != nil {
			_ = file.Close()
			release()
			return nil, fmt.Errorf("repository admission materialization lock: protect file: %w", chmodErr)
		}
		if lockErr := acquireRepositoryAdmissionFileLock(ctx, file); lockErr != nil {
			_ = file.Close()
			release()
			return nil, lockErr
		}
		held.files = append(held.files, file)
	}
	return release, nil
}

func (lock *repositoryAdmissionMaterializationLock) release() {
	if lock == nil {
		return
	}
	lock.once.Do(func() {
		for index := len(lock.files) - 1; index >= 0; index-- {
			_ = lockfile.FlockUnlock(lock.files[index])
			_ = lock.files[index].Close()
		}
		lock.files = nil
	})
}

func acquireRepositoryAdmissionFileLock(ctx context.Context, file *os.File) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-timer.C:
		}
		err := lockfile.TryLockExclusive(file)
		if err == nil {
			return nil
		}
		if !errors.Is(err, lockfile.ErrLocked) {
			return fmt.Errorf("repository admission materialization lock: acquire: %w", err)
		}
		timer.Reset(repositoryAdmissionLockRetryInterval)
	}
}

func canonicalMaterializationLockTarget(target string) (string, error) {
	target = filepath.Clean(strings.TrimSpace(target))
	if target == "" ||
		target == "." ||
		target == string(filepath.Separator) ||
		!filepath.IsAbs(target) {
		return "", errors.New("repository admission materialization lock: safe absolute target is required")
	}
	existing := target
	missing := make([]string, 0, 2)
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("repository admission materialization lock: inspect target: %w", err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", errors.New("repository admission materialization lock: no existing target ancestor")
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("repository admission materialization lock: resolve target: %w", err)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return filepath.Clean(resolved), nil
}

func (journal *RepositoryAdmissionJournal) Prepare(
	ctx context.Context,
	intent LocalIntent,
) (LocalRecord, error) {
	if journal == nil {
		return LocalRecord{}, errors.New("repository admission journal is unavailable")
	}
	normalized, err := normalizeLocalRepositoryAdmissionIntent(intent)
	if err != nil {
		return LocalRecord{}, err
	}
	var result LocalRecord
	err = journal.withLockedFile(ctx, func(file *localJournalFile) (bool, error) {
		if current, ok := file.Records[normalized.OperationID]; ok {
			if !equalLocalRepositoryAdmissionIntent(current.Intent, normalized) {
				return false, fmt.Errorf(
					"%w: operation %q was already prepared with different local coordinates",
					ErrLocalConflict,
					normalized.OperationID,
				)
			}
			result = current
			return false, nil
		}
		now := journal.now().UTC()
		result = LocalRecord{
			Intent: normalized, CreatedAt: now, UpdatedAt: now,
		}
		file.Records[normalized.OperationID] = result
		return true, nil
	})
	return result, err
}

//nolint:funlen // RepositoryAdmissionJournal binding atomically checks immutable intent, operation identity, and admission coordinates before persistence.
func (journal *RepositoryAdmissionJournal) Bind(
	ctx context.Context,
	operationID,
	admissionID,
	specFingerprint string,
) (LocalRecord, error) {
	if journal == nil {
		return LocalRecord{}, errors.New("repository admission journal is unavailable")
	}
	operationID, err := normalizeLocalRepositoryAdmissionOperationID(operationID)
	if err != nil {
		return LocalRecord{}, err
	}
	admissionID, err = repositoryadmission.NormalizeID(admissionID)
	if err != nil {
		return LocalRecord{}, err
	}
	specFingerprint, err = normalizeLocalRepositoryAdmissionFingerprint(specFingerprint)
	if err != nil {
		return LocalRecord{}, err
	}
	var result LocalRecord
	err = journal.withLockedFile(ctx, func(file *localJournalFile) (bool, error) {
		current, ok := file.Records[operationID]
		if !ok {
			return false, fmt.Errorf(
				"%w: operation %q",
				ErrLocalNotFound,
				operationID,
			)
		}
		if current.AdmissionID != "" {
			if current.AdmissionID != admissionID ||
				current.SpecFingerprint != specFingerprint {
				return false, fmt.Errorf(
					"%w: operation %q is already bound to a different admission identity",
					ErrLocalConflict,
					operationID,
				)
			}
			result = current
			return false, nil
		}
		for otherOperationID, other := range file.Records {
			if otherOperationID != operationID && localRecordContainsAdmissionID(other, admissionID) {
				return false, fmt.Errorf(
					"%w: admission %q is already bound to operation %q",
					ErrLocalConflict,
					admissionID,
					otherOperationID,
				)
			}
		}
		current.AdmissionID = admissionID
		current.SpecFingerprint = specFingerprint
		current.UpdatedAt = journal.now().UTC()
		file.Records[operationID] = current
		result = current
		return true, nil
	})
	return result, err
}

//nolint:funlen // Rebinding is a fail-closed compare-and-swap over both generations of durable identity.
func (journal *RepositoryAdmissionJournal) Rebind(
	ctx context.Context,
	operationID,
	expectedAdmissionID,
	expectedSpecFingerprint,
	replacementAdmissionID,
	replacementSpecFingerprint string,
) (LocalRecord, error) {
	if journal == nil {
		return LocalRecord{}, errors.New("repository admission journal is unavailable")
	}
	operationID, err := normalizeLocalRepositoryAdmissionOperationID(operationID)
	if err != nil {
		return LocalRecord{}, err
	}
	expectedAdmissionID, err = repositoryadmission.NormalizeID(expectedAdmissionID)
	if err != nil {
		return LocalRecord{}, err
	}
	replacementAdmissionID, err = repositoryadmission.NormalizeID(replacementAdmissionID)
	if err != nil {
		return LocalRecord{}, err
	}
	expectedSpecFingerprint, err = normalizeLocalRepositoryAdmissionFingerprint(expectedSpecFingerprint)
	if err != nil {
		return LocalRecord{}, err
	}
	replacementSpecFingerprint, err = normalizeLocalRepositoryAdmissionFingerprint(replacementSpecFingerprint)
	if err != nil {
		return LocalRecord{}, err
	}
	var result LocalRecord
	err = journal.withLockedFile(ctx, func(file *localJournalFile) (bool, error) {
		current, ok := file.Records[operationID]
		if !ok {
			return false, fmt.Errorf("%w: operation %q", ErrLocalNotFound, operationID)
		}
		if current.AdmissionID == replacementAdmissionID &&
			current.SpecFingerprint == replacementSpecFingerprint {
			result = current
			return false, nil
		}
		if current.AdmissionID != expectedAdmissionID ||
			current.SpecFingerprint != expectedSpecFingerprint {
			return false, fmt.Errorf(
				"%w: operation %q durable coordinates changed before replacement",
				ErrLocalConflict,
				operationID,
			)
		}
		for otherOperationID, other := range file.Records {
			if otherOperationID != operationID && localRecordContainsAdmissionID(other, replacementAdmissionID) {
				return false, fmt.Errorf(
					"%w: replacement admission %q is already bound to operation %q",
					ErrLocalConflict,
					replacementAdmissionID,
					otherOperationID,
				)
			}
		}
		if !slices.Contains(current.PreviousAdmissionIDs, current.AdmissionID) {
			current.PreviousAdmissionIDs = append(current.PreviousAdmissionIDs, current.AdmissionID)
			sort.Strings(current.PreviousAdmissionIDs)
		}
		current.AdmissionID = replacementAdmissionID
		current.SpecFingerprint = replacementSpecFingerprint
		current.UpdatedAt = journal.now().UTC()
		file.Records[operationID] = current
		result = current
		return true, nil
	})
	if err == nil {
		journal.authorityMu.Lock()
		delete(journal.authorities, expectedAdmissionID)
		delete(journal.authorities, replacementAdmissionID)
		journal.authorityMu.Unlock()
	}
	return result, err
}

func localRecordContainsAdmissionID(record LocalRecord, admissionID string) bool {
	return record.AdmissionID == admissionID || slices.Contains(record.PreviousAdmissionIDs, admissionID)
}

func (journal *RepositoryAdmissionJournal) GetByOperation(
	ctx context.Context,
	operationID string,
) (LocalRecord, error) {
	if journal == nil {
		return LocalRecord{}, errors.New("repository admission journal is unavailable")
	}
	operationID, err := normalizeLocalRepositoryAdmissionOperationID(operationID)
	if err != nil {
		return LocalRecord{}, err
	}
	var result LocalRecord
	err = journal.withLockedFile(ctx, func(file *localJournalFile) (bool, error) {
		current, ok := file.Records[operationID]
		if !ok {
			return false, fmt.Errorf(
				"%w: operation %q",
				ErrLocalNotFound,
				operationID,
			)
		}
		result = current
		return false, nil
	})
	return result, err
}

func (journal *RepositoryAdmissionJournal) GetByAdmission(
	ctx context.Context,
	admissionID string,
) (LocalRecord, error) {
	if journal == nil {
		return LocalRecord{}, errors.New("repository admission journal is unavailable")
	}
	admissionID, err := repositoryadmission.NormalizeID(admissionID)
	if err != nil {
		return LocalRecord{}, err
	}
	var result LocalRecord
	err = journal.withLockedFile(ctx, func(file *localJournalFile) (bool, error) {
		for _, current := range file.Records {
			if localRecordContainsAdmissionID(current, admissionID) {
				result = current
				return false, nil
			}
		}
		return false, fmt.Errorf(
			"%w: admission %q",
			ErrLocalNotFound,
			admissionID,
		)
	})
	return result, err
}

//nolint:funlen // Recovery resolution validates both durable coordinates and the complete local checkout intent before returning it.
func (journal *RepositoryAdmissionJournal) ResolveLocal(
	ctx context.Context,
	admissionID string,
) (repositoryadmission.LocalProjection, error) {
	if journal == nil || journal.monotonicNow == nil {
		return repositoryadmission.LocalProjection{}, repositoryadmission.ErrLocalNotFound
	}
	record, err := journal.GetByAdmission(ctx, admissionID)
	if err != nil {
		return repositoryadmission.LocalProjection{}, err
	}
	journal.authorityMu.Lock()
	authority, active := journal.authorities[record.AdmissionID]
	if active && !authority.deadline.After(journal.monotonicNow()) {
		delete(journal.authorities, record.AdmissionID)
		active = false
	}
	if !active {
		journal.authorityMu.Unlock()
		return repositoryadmission.LocalProjection{}, fmt.Errorf(
			"repository admission %q has no live local owner generation: %w",
			record.AdmissionID,
			repositoryadmission.ErrLocalNotFound,
		)
	}
	if authority.coordinate.WorkspaceKey != record.Intent.WorkspaceKey ||
		authority.coordinate.OperationID != record.Intent.OperationID ||
		authority.coordinate.AdmissionID != record.AdmissionID ||
		authority.coordinate.SpecFingerprint != record.SpecFingerprint {
		journal.authorityMu.Unlock()
		return repositoryadmission.LocalProjection{}, fmt.Errorf(
			"repository admission %q local authority diverges from its durable journal: %w",
			record.AdmissionID,
			repositoryadmission.ErrInvalid,
		)
	}
	coordinate := authority.coordinate
	journal.authorityMu.Unlock()
	return repositoryadmission.LocalProjection{
		WorkspaceKey:      record.Intent.WorkspaceKey,
		OperationID:       record.Intent.OperationID,
		AdmissionID:       record.AdmissionID,
		OwnerID:           coordinate.OwnerID,
		OwnerGenerationID: coordinate.OwnerGenerationID,
		SpecFingerprint:   record.SpecFingerprint,
		WorkspacePath:     record.Intent.WorkspacePath,
	}, nil
}

func (journal *RepositoryAdmissionJournal) List(
	ctx context.Context,
) ([]LocalRecord, error) {
	if journal == nil {
		return nil, errors.New("repository admission journal is unavailable")
	}
	var result []LocalRecord
	err := journal.withLockedFile(ctx, func(file *localJournalFile) (bool, error) {
		result = make([]LocalRecord, 0, len(file.Records))
		for _, record := range file.Records {
			result = append(result, record)
		}
		sort.Slice(result, func(i, k int) bool {
			if result[i].CreatedAt.Equal(result[k].CreatedAt) {
				return result[i].Intent.OperationID < result[k].Intent.OperationID
			}
			return result[i].CreatedAt.Before(result[k].CreatedAt)
		})
		return false, nil
	})
	return result, err
}

func (journal *RepositoryAdmissionJournal) Remove(
	ctx context.Context,
	operationID string,
) error {
	if journal == nil {
		return errors.New("repository admission journal is unavailable")
	}
	operationID, err := normalizeLocalRepositoryAdmissionOperationID(operationID)
	if err != nil {
		return err
	}
	return journal.withLockedFile(ctx, func(file *localJournalFile) (bool, error) {
		if _, ok := file.Records[operationID]; !ok {
			return false, nil
		}
		delete(file.Records, operationID)
		return true, nil
	})
}

func (journal *RepositoryAdmissionJournal) withLockedFile(
	ctx context.Context,
	mutate func(*localJournalFile) (bool, error),
) error {
	if ctx == nil {
		return errors.New("repository admission journal: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if mutate == nil {
		return errors.New("repository admission journal: mutation is required")
	}
	if err := os.MkdirAll(journal.dir, 0o700); err != nil {
		return fmt.Errorf("repository admission journal: create directory: %w", err)
	}
	if err := os.Chmod(journal.dir, 0o700); err != nil { //nolint:gosec // A private journal directory needs owner execute permission for recovery files.
		return fmt.Errorf("repository admission journal: protect directory: %w", err)
	}
	return withFileLock(filepath.Join(journal.dir, "journal.lock"), func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := journal.loadLocked()
		if err != nil {
			return err
		}
		changed, err := mutate(file)
		if err != nil || !changed {
			return err
		}
		return journal.saveLocked(file)
	})
}

func withFileLock(path string, run func() error) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path is rooted in the private journal directory.
	if err != nil {
		return fmt.Errorf("repository admission journal: open lock: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("repository admission journal: protect lock: %w", err)
	}
	if err := lockfile.FlockExclusiveBlocking(file); err != nil {
		return fmt.Errorf("repository admission journal: acquire lock: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(file) }()
	return run()
}

func (journal *RepositoryAdmissionJournal) loadLocked() (*localJournalFile, error) {
	path := filepath.Join(journal.dir, localJournalFileName)
	data, err := os.ReadFile(path) //nolint:gosec // path is rooted in the protected Loom data directory.
	if err != nil {
		if os.IsNotExist(err) {
			return &localJournalFile{
				Version: localJournalVersion,
				Records: make(map[string]LocalRecord),
			}, nil
		}
		return nil, fmt.Errorf("repository admission journal: read: %w", err)
	}
	var file localJournalFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("repository admission journal: parse: %w", err)
	}
	if file.Version != localJournalVersion {
		return nil, fmt.Errorf(
			"repository admission journal: unsupported version %d",
			file.Version,
		)
	}
	if file.Records == nil {
		file.Records = make(map[string]LocalRecord)
	}
	return &file, nil
}

func (journal *RepositoryAdmissionJournal) saveLocked(
	file *localJournalFile,
) error {
	if file == nil {
		return errors.New("repository admission journal: nil file")
	}
	file.Version = localJournalVersion
	if file.Records == nil {
		file.Records = make(map[string]LocalRecord)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("repository admission journal: marshal: %w", err)
	}
	path := filepath.Join(journal.dir, localJournalFileName)
	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("repository admission journal: write: %w", err)
	}
	return nil
}

//nolint:funlen // Canonicalization validates the full restart-safe repository batch and its containment-sensitive fields together.
func normalizeLocalRepositoryAdmissionIntent(
	intent LocalIntent,
) (LocalIntent, error) {
	var err error
	intent.OperationID, err = normalizeLocalRepositoryAdmissionOperationID(intent.OperationID)
	if err != nil {
		return LocalIntent{}, err
	}
	intent.WorkspaceKey = strings.TrimSpace(intent.WorkspaceKey)
	if intent.WorkspaceKey == "" {
		return LocalIntent{}, errors.New("repository admission journal: workspace key is required")
	}
	intent.WorkspaceName = strings.TrimSpace(intent.WorkspaceName)
	intent.WorkspacePath = filepath.Clean(strings.TrimSpace(intent.WorkspacePath))
	if intent.WorkspacePath == "" ||
		intent.WorkspacePath == "." ||
		intent.WorkspacePath == string(filepath.Separator) ||
		!filepath.IsAbs(intent.WorkspacePath) {
		return LocalIntent{}, errors.New("repository admission journal: safe absolute workspace path is required")
	}
	switch intent.Kind {
	case KindCreateWorkspace, KindAddRepositories:
	default:
		return LocalIntent{}, fmt.Errorf(
			"repository admission journal: unsupported operation kind %q",
			intent.Kind,
		)
	}
	intent.Branch = strings.TrimSpace(intent.Branch)
	intent.CloneURLs = append([]string(nil), intent.CloneURLs...)
	for index, rawRemote := range intent.CloneURLs {
		remote, err := repositoryadmission.ValidateTokenFreeRemote(rawRemote)
		if err != nil {
			return LocalIntent{}, fmt.Errorf(
				"repository admission journal: clone source %d is not canonical and token-free: %w",
				index,
				err,
			)
		}
		intent.CloneURLs[index] = remote
	}
	intent.LocalRepoPaths = append([]string(nil), intent.LocalRepoPaths...)
	for index, rawPath := range intent.LocalRepoPaths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" ||
			path == "." ||
			path == string(filepath.Separator) ||
			!filepath.IsAbs(path) {
			return LocalIntent{}, fmt.Errorf(
				"repository admission journal: local repository source %d must be a safe absolute path",
				index,
			)
		}
		intent.LocalRepoPaths[index] = path
	}
	return intent, nil
}

func equalLocalRepositoryAdmissionIntent(
	left,
	right LocalIntent,
) bool {
	return left.OperationID == right.OperationID &&
		left.WorkspaceKey == right.WorkspaceKey &&
		left.WorkspaceName == right.WorkspaceName &&
		left.WorkspacePath == right.WorkspacePath &&
		left.Kind == right.Kind &&
		left.Branch == right.Branch &&
		slices.Equal(left.CloneURLs, right.CloneURLs) &&
		slices.Equal(left.LocalRepoPaths, right.LocalRepoPaths)
}

func normalizeLocalRepositoryAdmissionOperationID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("repository admission journal: operation ID is required")
	}
	if len(value) > maxLocalRepositoryAdmissionOperationID ||
		strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", fmt.Errorf(
			"repository admission journal: operation ID must be at most %d bytes without control characters",
			maxLocalRepositoryAdmissionOperationID,
		)
	}
	return value, nil
}

func normalizeLocalRepositoryAdmissionFingerprint(value string) (string, error) {
	value = strings.TrimSpace(value)
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return "", errors.New("repository admission journal: spec fingerprint must be canonical sha256")
	}
	encoded := strings.TrimPrefix(value, prefix)
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || strings.ToLower(encoded) != encoded {
		return "", errors.New("repository admission journal: spec fingerprint must be canonical sha256")
	}
	return value, nil
}
