package admissionstore

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

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/configlock"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

const (
	localRepositoryAdmissionJournalVersion  = 1
	localRepositoryAdmissionJournalDirName  = "repository-admissions"
	localRepositoryAdmissionJournalFileName = "journal.json"
	localRepositoryAdmissionLockDirName     = "materialization-locks"
	maxLocalRepositoryAdmissionOperationID  = 200
	repositoryAdmissionLockRetryInterval    = 20 * time.Millisecond
)

var (
	errLocalRepositoryAdmissionNotFound = errors.New("local repository admission not found")
	errLocalRepositoryAdmissionConflict = errors.New("local repository admission conflict")
)

var (
	ErrLocalRepositoryAdmissionNotFound = errLocalRepositoryAdmissionNotFound
	ErrLocalRepositoryAdmissionConflict = errLocalRepositoryAdmissionConflict
)

type localRepositoryAdmissionKind string

const (
	localRepositoryAdmissionCreateWorkspace localRepositoryAdmissionKind = "create_workspace"
	localRepositoryAdmissionAddRepositories localRepositoryAdmissionKind = "add_repositories"
)

// localRepositoryAdmissionIntent is the correctness-critical machine-local
// half of one durable FleetDB repository-admission process. FleetDB owns the
// token-free repository spec and process state; this record owns the checkout
// root plus the token-free machine-local source coordinates needed to resume
// the exact request after serve restart.
type localRepositoryAdmissionIntent struct {
	OperationID    string                       `json:"operation_id"`
	WorkspaceKey   string                       `json:"workspace_key"`
	WorkspaceName  string                       `json:"workspace_name,omitempty"`
	WorkspacePath  string                       `json:"workspace_path"`
	Kind           localRepositoryAdmissionKind `json:"kind"`
	Branch         string                       `json:"branch,omitempty"`
	CloneURLs      []string                     `json:"clone_urls,omitempty"`
	LocalRepoPaths []string                     `json:"local_repo_paths,omitempty"`
}

type localRepositoryAdmissionRecord struct {
	Intent          localRepositoryAdmissionIntent `json:"intent"`
	AdmissionID     string                         `json:"admission_id,omitempty"`
	SpecFingerprint string                         `json:"spec_fingerprint,omitempty"`
	CreatedAt       time.Time                      `json:"created_at"`
	UpdatedAt       time.Time                      `json:"updated_at"`
}

type (
	LocalRepositoryAdmissionKind   = localRepositoryAdmissionKind
	LocalRepositoryAdmissionIntent = localRepositoryAdmissionIntent
	LocalRepositoryAdmissionRecord = localRepositoryAdmissionRecord
)

const (
	LocalRepositoryAdmissionCreateWorkspace = localRepositoryAdmissionCreateWorkspace
	LocalRepositoryAdmissionAddRepositories = localRepositoryAdmissionAddRepositories
	LocalRepositoryAdmissionJournalFileName = localRepositoryAdmissionJournalFileName
)

type localRepositoryAdmissionJournalFile struct {
	Version int                                       `json:"version"`
	Records map[string]localRepositoryAdmissionRecord `json:"records"`
}

// RepositoryAdmissionCoordinate identifies one exact process-local
// materialization authority. The durable journal is sufficient to recover an
// admission after restart, but only a successful exact Fleet renewal in this
// serve incarnation may authorize Source Control to materialize repositories.
type RepositoryAdmissionCoordinate struct {
	WorkspaceKey      string
	AdmissionID       string
	OperationID       string
	OwnerID           string
	OwnerGenerationID string
	SpecFingerprint   string
}

type repositoryAdmissionCoordinate = RepositoryAdmissionCoordinate

type repositoryAdmissionMaterializationAuthority struct {
	coordinate RepositoryAdmissionCoordinate
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

func NewRepositoryAdmissionJournal() (*RepositoryAdmissionJournal, error) {
	root := strings.TrimSpace(bootstrap.LoomDir())
	if root == "" {
		return nil, errors.New("repository admission journal: Loom directory is unavailable")
	}
	return newLocalRepositoryAdmissionJournalAt(
		filepath.Join(root, localRepositoryAdmissionJournalDirName),
		time.Now,
	)
}

func newLocalRepositoryAdmissionJournalAt(
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

func NewRepositoryAdmissionJournalAt(
	dir string,
	now func() time.Time,
) (*RepositoryAdmissionJournal, error) {
	return newLocalRepositoryAdmissionJournalAt(dir, now)
}

// NormalizeLocalRepositoryAdmissionID validates and normalizes an admission ID
// before it is used as a durable journal key.
func NormalizeLocalRepositoryAdmissionID(value string) (string, error) {
	return normalizeLocalRepositoryAdmissionID(value)
}

func validRepositoryAdmissionMaterializationCoordinate(
	coordinate repositoryAdmissionCoordinate,
) bool {
	return strings.TrimSpace(coordinate.WorkspaceKey) != "" &&
		strings.TrimSpace(coordinate.AdmissionID) != "" &&
		strings.TrimSpace(coordinate.OperationID) != "" &&
		strings.TrimSpace(coordinate.OwnerID) != "" &&
		strings.TrimSpace(coordinate.OwnerGenerationID) != "" &&
		strings.TrimSpace(coordinate.SpecFingerprint) != ""
}

func (journal *RepositoryAdmissionJournal) activateMaterializationAuthority(
	coordinate repositoryAdmissionCoordinate,
	deadline time.Time,
) error {
	if journal == nil ||
		journal.monotonicNow == nil ||
		!validRepositoryAdmissionMaterializationCoordinate(coordinate) ||
		deadline.IsZero() {
		return fmt.Errorf(
			"repository admission materialization authority is invalid: %w",
			sourcecontrol.ErrInvalidMaterialization,
		)
	}
	journal.authorityMu.Lock()
	defer journal.authorityMu.Unlock()
	if !deadline.After(journal.monotonicNow()) {
		return fmt.Errorf(
			"repository admission materialization authority is expired: %w",
			sourcecontrol.ErrInvalidMaterialization,
		)
	}
	if current, exists := journal.authorities[coordinate.AdmissionID]; exists &&
		current.coordinate != coordinate {
		return fmt.Errorf(
			"repository admission %q has a different active owner generation: %w",
			coordinate.AdmissionID,
			sourcecontrol.ErrInvalidMaterialization,
		)
	}
	journal.authorities[coordinate.AdmissionID] =
		repositoryAdmissionMaterializationAuthority{
			coordinate: coordinate,
			deadline:   deadline,
		}
	return nil
}

func (journal *RepositoryAdmissionJournal) renewMaterializationAuthority(
	coordinate repositoryAdmissionCoordinate,
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

func (journal *RepositoryAdmissionJournal) deactivateMaterializationAuthority(
	coordinate repositoryAdmissionCoordinate,
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

func (journal *RepositoryAdmissionJournal) deactivateAllMaterializationAuthorities() {
	if journal == nil {
		return
	}
	journal.authorityMu.Lock()
	clear(journal.authorities)
	journal.authorityMu.Unlock()
}

func (journal *RepositoryAdmissionJournal) ActivateMaterializationAuthority(
	coordinate RepositoryAdmissionCoordinate,
	deadline time.Time,
) error {
	return journal.activateMaterializationAuthority(coordinate, deadline)
}

func (journal *RepositoryAdmissionJournal) RenewMaterializationAuthority(
	coordinate RepositoryAdmissionCoordinate,
	deadline time.Time,
) bool {
	return journal.renewMaterializationAuthority(coordinate, deadline)
}

func (journal *RepositoryAdmissionJournal) DeactivateMaterializationAuthority(
	coordinate RepositoryAdmissionCoordinate,
) {
	journal.deactivateMaterializationAuthority(coordinate)
}

func (journal *RepositoryAdmissionJournal) DeactivateAllMaterializationAuthorities() {
	journal.deactivateAllMaterializationAuthorities()
}

func (journal *RepositoryAdmissionJournal) MaterializationAuthorityDeadline(
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

func (journal *RepositoryAdmissionJournal) MaterializationAuthorityCount() int {
	if journal == nil {
		return 0
	}
	journal.authorityMu.Lock()
	defer journal.authorityMu.Unlock()
	return len(journal.authorities)
}

// AcquireMaterializationLock serializes one durable admission and every local
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
	admissionID, err := normalizeLocalRepositoryAdmissionID(admissionID)
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
	intent localRepositoryAdmissionIntent,
) (localRepositoryAdmissionRecord, error) {
	if journal == nil {
		return localRepositoryAdmissionRecord{}, errors.New("repository admission journal is unavailable")
	}
	normalized, err := normalizeLocalRepositoryAdmissionIntent(intent)
	if err != nil {
		return localRepositoryAdmissionRecord{}, err
	}
	var result localRepositoryAdmissionRecord
	err = journal.withLockedFile(ctx, func(file *localRepositoryAdmissionJournalFile) (bool, error) {
		if current, ok := file.Records[normalized.OperationID]; ok {
			if !equalLocalRepositoryAdmissionIntent(current.Intent, normalized) {
				return false, fmt.Errorf(
					"%w: operation %q was already prepared with different local coordinates",
					errLocalRepositoryAdmissionConflict,
					normalized.OperationID,
				)
			}
			result = current
			return false, nil
		}
		now := journal.now().UTC()
		result = localRepositoryAdmissionRecord{
			Intent: normalized, CreatedAt: now, UpdatedAt: now,
		}
		file.Records[normalized.OperationID] = result
		return true, nil
	})
	return result, err
}

//nolint:funlen // Journal binding atomically checks immutable intent, operation identity, and admission coordinates before persistence.
func (journal *RepositoryAdmissionJournal) Bind(
	ctx context.Context,
	operationID,
	admissionID,
	specFingerprint string,
) (localRepositoryAdmissionRecord, error) {
	if journal == nil {
		return localRepositoryAdmissionRecord{}, errors.New("repository admission journal is unavailable")
	}
	operationID, err := normalizeLocalRepositoryAdmissionOperationID(operationID)
	if err != nil {
		return localRepositoryAdmissionRecord{}, err
	}
	admissionID, err = normalizeLocalRepositoryAdmissionID(admissionID)
	if err != nil {
		return localRepositoryAdmissionRecord{}, err
	}
	specFingerprint, err = normalizeLocalRepositoryAdmissionFingerprint(specFingerprint)
	if err != nil {
		return localRepositoryAdmissionRecord{}, err
	}
	var result localRepositoryAdmissionRecord
	err = journal.withLockedFile(ctx, func(file *localRepositoryAdmissionJournalFile) (bool, error) {
		current, ok := file.Records[operationID]
		if !ok {
			return false, fmt.Errorf(
				"%w: operation %q",
				errLocalRepositoryAdmissionNotFound,
				operationID,
			)
		}
		if current.AdmissionID != "" {
			if current.AdmissionID != admissionID ||
				current.SpecFingerprint != specFingerprint {
				return false, fmt.Errorf(
					"%w: operation %q is already bound to a different admission identity",
					errLocalRepositoryAdmissionConflict,
					operationID,
				)
			}
			result = current
			return false, nil
		}
		for otherOperationID, other := range file.Records {
			if otherOperationID != operationID && other.AdmissionID == admissionID {
				return false, fmt.Errorf(
					"%w: admission %q is already bound to operation %q",
					errLocalRepositoryAdmissionConflict,
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

func (journal *RepositoryAdmissionJournal) GetByOperation(
	ctx context.Context,
	operationID string,
) (localRepositoryAdmissionRecord, error) {
	if journal == nil {
		return localRepositoryAdmissionRecord{}, errors.New("repository admission journal is unavailable")
	}
	operationID, err := normalizeLocalRepositoryAdmissionOperationID(operationID)
	if err != nil {
		return localRepositoryAdmissionRecord{}, err
	}
	var result localRepositoryAdmissionRecord
	err = journal.withLockedFile(ctx, func(file *localRepositoryAdmissionJournalFile) (bool, error) {
		current, ok := file.Records[operationID]
		if !ok {
			return false, fmt.Errorf(
				"%w: operation %q",
				errLocalRepositoryAdmissionNotFound,
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
) (localRepositoryAdmissionRecord, error) {
	if journal == nil {
		return localRepositoryAdmissionRecord{}, errors.New("repository admission journal is unavailable")
	}
	admissionID, err := normalizeLocalRepositoryAdmissionID(admissionID)
	if err != nil {
		return localRepositoryAdmissionRecord{}, err
	}
	var result localRepositoryAdmissionRecord
	err = journal.withLockedFile(ctx, func(file *localRepositoryAdmissionJournalFile) (bool, error) {
		for _, current := range file.Records {
			if current.AdmissionID == admissionID {
				result = current
				return false, nil
			}
		}
		return false, fmt.Errorf(
			"%w: admission %q",
			errLocalRepositoryAdmissionNotFound,
			admissionID,
		)
	})
	return result, err
}

//nolint:funlen // Recovery resolution validates both durable coordinates and the complete local checkout intent before returning it.
func (journal *RepositoryAdmissionJournal) ResolveLocalRepositoryAdmission(
	ctx context.Context,
	admissionID string,
) (sourcecontrol.RepositoryAdmissionLocalProjection, error) {
	if journal == nil || journal.monotonicNow == nil {
		return sourcecontrol.RepositoryAdmissionLocalProjection{},
			sourcecontrol.ErrRepositoryAdmissionNotFound
	}
	record, err := journal.GetByAdmission(ctx, admissionID)
	if err != nil {
		if errors.Is(err, errLocalRepositoryAdmissionNotFound) {
			return sourcecontrol.RepositoryAdmissionLocalProjection{}, fmt.Errorf(
				"%w: %v",
				sourcecontrol.ErrRepositoryAdmissionNotFound,
				err,
			)
		}
		return sourcecontrol.RepositoryAdmissionLocalProjection{}, err
	}
	journal.authorityMu.Lock()
	authority, active := journal.authorities[record.AdmissionID]
	if active && !authority.deadline.After(journal.monotonicNow()) {
		delete(journal.authorities, record.AdmissionID)
		active = false
	}
	if !active {
		journal.authorityMu.Unlock()
		return sourcecontrol.RepositoryAdmissionLocalProjection{}, fmt.Errorf(
			"repository admission %q has no live local owner generation: %w",
			record.AdmissionID,
			sourcecontrol.ErrRepositoryAdmissionNotFound,
		)
	}
	if authority.coordinate.WorkspaceKey != record.Intent.WorkspaceKey ||
		authority.coordinate.OperationID != record.Intent.OperationID ||
		authority.coordinate.AdmissionID != record.AdmissionID ||
		authority.coordinate.SpecFingerprint != record.SpecFingerprint {
		journal.authorityMu.Unlock()
		return sourcecontrol.RepositoryAdmissionLocalProjection{}, fmt.Errorf(
			"repository admission %q local authority diverges from its durable journal: %w",
			record.AdmissionID,
			sourcecontrol.ErrInvalidMaterialization,
		)
	}
	coordinate := authority.coordinate
	journal.authorityMu.Unlock()
	return sourcecontrol.RepositoryAdmissionLocalProjection{
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
) ([]localRepositoryAdmissionRecord, error) {
	if journal == nil {
		return nil, errors.New("repository admission journal is unavailable")
	}
	var result []localRepositoryAdmissionRecord
	err := journal.withLockedFile(ctx, func(file *localRepositoryAdmissionJournalFile) (bool, error) {
		result = make([]localRepositoryAdmissionRecord, 0, len(file.Records))
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
	return journal.withLockedFile(ctx, func(file *localRepositoryAdmissionJournalFile) (bool, error) {
		if _, ok := file.Records[operationID]; !ok {
			return false, nil
		}
		delete(file.Records, operationID)
		return true, nil
	})
}

func (journal *RepositoryAdmissionJournal) withLockedFile(
	ctx context.Context,
	mutate func(*localRepositoryAdmissionJournalFile) (bool, error),
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
	return configlock.WithLock(journal.dir, func() error {
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

func (journal *RepositoryAdmissionJournal) loadLocked() (*localRepositoryAdmissionJournalFile, error) {
	path := filepath.Join(journal.dir, localRepositoryAdmissionJournalFileName)
	data, err := os.ReadFile(path) //nolint:gosec // path is rooted in the protected Loom data directory.
	if err != nil {
		if os.IsNotExist(err) {
			return &localRepositoryAdmissionJournalFile{
				Version: localRepositoryAdmissionJournalVersion,
				Records: make(map[string]localRepositoryAdmissionRecord),
			}, nil
		}
		return nil, fmt.Errorf("repository admission journal: read: %w", err)
	}
	var file localRepositoryAdmissionJournalFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("repository admission journal: parse: %w", err)
	}
	if file.Version != localRepositoryAdmissionJournalVersion {
		return nil, fmt.Errorf(
			"repository admission journal: unsupported version %d",
			file.Version,
		)
	}
	if file.Records == nil {
		file.Records = make(map[string]localRepositoryAdmissionRecord)
	}
	return &file, nil
}

func (journal *RepositoryAdmissionJournal) saveLocked(
	file *localRepositoryAdmissionJournalFile,
) error {
	if file == nil {
		return errors.New("repository admission journal: nil file")
	}
	file.Version = localRepositoryAdmissionJournalVersion
	if file.Records == nil {
		file.Records = make(map[string]localRepositoryAdmissionRecord)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("repository admission journal: marshal: %w", err)
	}
	path := filepath.Join(journal.dir, localRepositoryAdmissionJournalFileName)
	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("repository admission journal: write: %w", err)
	}
	return nil
}

//nolint:funlen // Canonicalization validates the full restart-safe repository batch and its containment-sensitive fields together.
func normalizeLocalRepositoryAdmissionIntent(
	intent localRepositoryAdmissionIntent,
) (localRepositoryAdmissionIntent, error) {
	var err error
	intent.OperationID, err = normalizeLocalRepositoryAdmissionOperationID(intent.OperationID)
	if err != nil {
		return localRepositoryAdmissionIntent{}, err
	}
	intent.WorkspaceKey = strings.TrimSpace(intent.WorkspaceKey)
	if intent.WorkspaceKey == "" {
		return localRepositoryAdmissionIntent{}, errors.New("repository admission journal: workspace key is required")
	}
	intent.WorkspaceName = strings.TrimSpace(intent.WorkspaceName)
	intent.WorkspacePath = filepath.Clean(strings.TrimSpace(intent.WorkspacePath))
	if intent.WorkspacePath == "" ||
		intent.WorkspacePath == "." ||
		intent.WorkspacePath == string(filepath.Separator) ||
		!filepath.IsAbs(intent.WorkspacePath) {
		return localRepositoryAdmissionIntent{}, errors.New("repository admission journal: safe absolute workspace path is required")
	}
	switch intent.Kind {
	case localRepositoryAdmissionCreateWorkspace, localRepositoryAdmissionAddRepositories:
	default:
		return localRepositoryAdmissionIntent{}, fmt.Errorf(
			"repository admission journal: unsupported operation kind %q",
			intent.Kind,
		)
	}
	intent.Branch = strings.TrimSpace(intent.Branch)
	intent.CloneURLs = append([]string(nil), intent.CloneURLs...)
	for index, rawRemote := range intent.CloneURLs {
		remote, err := sourcecontrol.ValidateTokenFreeRemote(rawRemote)
		if err != nil {
			return localRepositoryAdmissionIntent{}, fmt.Errorf(
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
			return localRepositoryAdmissionIntent{}, fmt.Errorf(
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
	right localRepositoryAdmissionIntent,
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

func normalizeLocalRepositoryAdmissionID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != 32 {
		return "", errors.New("repository admission journal: admission ID must be 32 lowercase hex characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 16 || strings.ToLower(value) != value {
		return "", errors.New("repository admission journal: admission ID must be 32 lowercase hex characters")
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
