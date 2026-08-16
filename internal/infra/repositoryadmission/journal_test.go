package repositoryadmissioninfra

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLocalJournalSurvivesReloadAndBindsExactlyOnce(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "journal")
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	journal, err := NewRepositoryAdmissionJournalAt(dir, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	intent := LocalIntent{
		OperationID:   "operation-1",
		WorkspaceKey:  "WORK",
		WorkspaceName: "Work",
		WorkspacePath: filepath.Join(t.TempDir(), "workspace"),
		Kind:          KindCreateWorkspace,
	}
	prepared, err := journal.Prepare(context.Background(), intent)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared.AdmissionID != "" || prepared.CreatedAt != now || prepared.UpdatedAt != now {
		t.Fatalf("prepared = %#v", prepared)
	}

	now = now.Add(time.Minute)
	const (
		admissionID = "0123456789abcdef0123456789abcdef"
		fingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	)
	bound, err := journal.Bind(
		context.Background(),
		intent.OperationID,
		admissionID,
		fingerprint,
	)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if bound.AdmissionID != admissionID ||
		bound.SpecFingerprint != fingerprint ||
		bound.CreatedAt != prepared.CreatedAt ||
		bound.UpdatedAt != now {
		t.Fatalf("bound = %#v", bound)
	}

	reloaded, err := NewRepositoryAdmissionJournalAt(dir, time.Now)
	if err != nil {
		t.Fatalf("reload journal: %v", err)
	}
	byOperation, err := reloaded.GetByOperation(context.Background(), intent.OperationID)
	if err != nil {
		t.Fatalf("get by operation: %v", err)
	}
	byAdmission, err := reloaded.GetByAdmission(context.Background(), admissionID)
	if err != nil {
		t.Fatalf("get by admission: %v", err)
	}
	if !reflect.DeepEqual(byOperation, bound) ||
		!reflect.DeepEqual(byAdmission, bound) {
		t.Fatalf("reloaded records = %#v, %#v; want %#v", byOperation, byAdmission, bound)
	}

	info, err := os.Stat(filepath.Join(dir, localJournalFileName))
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("journal mode = %o, want 600", got)
	}
}

func TestLocalRepositoryAdmissionAuthorityIsLiveExactAndProcessLocal(
	t *testing.T,
) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "journal")
	journal, err := NewRepositoryAdmissionJournalAt(dir, time.Now)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	intent := LocalIntent{
		OperationID:   "operation-1",
		WorkspaceKey:  "WORK",
		WorkspacePath: filepath.Join(t.TempDir(), "workspace"),
		Kind:          KindAddRepositories,
	}
	if _, err := journal.Prepare(t.Context(), intent); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	const (
		admissionID = "0123456789abcdef0123456789abcdef"
		fingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	)
	if _, err := journal.Bind(
		t.Context(),
		intent.OperationID,
		admissionID,
		fingerprint,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}
	coordinate := Coordinate{
		WorkspaceKey:      intent.WorkspaceKey,
		AdmissionID:       admissionID,
		OperationID:       intent.OperationID,
		OwnerID:           "loom-workspace-admission-owner",
		OwnerGenerationID: "abcdef0123456789abcdef0123456789",
		SpecFingerprint:   fingerprint,
	}
	monotonicNow := time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)
	journal.monotonicNow = func() time.Time { return monotonicNow }

	if _, err := journal.ResolveLocal(
		t.Context(),
		admissionID,
	); !errors.Is(err, ErrLocalNotFound) {
		t.Fatalf("durable journal authorized without live owner generation: %v", err)
	}
	if err := journal.ActivateMaterializationAuthority(
		coordinate,
		monotonicNow.Add(time.Minute),
	); err != nil {
		t.Fatalf("activate exact authority: %v", err)
	}
	projection, err := journal.ResolveLocal(
		t.Context(),
		admissionID,
	)
	if err != nil {
		t.Fatalf("resolve active authority: %v", err)
	}
	if projection.OwnerID != coordinate.OwnerID ||
		projection.OwnerGenerationID != coordinate.OwnerGenerationID ||
		projection.SpecFingerprint != coordinate.SpecFingerprint {
		t.Fatalf("active projection = %#v", projection)
	}

	stale := coordinate
	stale.OwnerGenerationID = "11111111111111111111111111111111"
	if journal.RenewMaterializationAuthority(
		stale,
		monotonicNow.Add(2*time.Minute),
	) {
		t.Fatal("stale owner generation renewed exact authority")
	}
	journal.DeactivateMaterializationAuthority(stale)
	if _, err := journal.ResolveLocal(
		t.Context(),
		admissionID,
	); err != nil {
		t.Fatalf("stale owner generation cleared exact authority: %v", err)
	}

	if !journal.RenewMaterializationAuthority(
		coordinate,
		monotonicNow.Add(10*time.Second),
	) {
		t.Fatal("exact owner generation did not accept shorter renewed deadline")
	}
	monotonicNow = monotonicNow.Add(11 * time.Second)
	if _, err := journal.ResolveLocal(
		t.Context(),
		admissionID,
	); !errors.Is(err, ErrLocalNotFound) {
		t.Fatalf("expired monotonic authority resolved: %v", err)
	}

	if err := journal.ActivateMaterializationAuthority(
		coordinate,
		monotonicNow.Add(time.Minute),
	); err != nil {
		t.Fatalf("reactivate exact authority: %v", err)
	}
	reloaded, err := NewRepositoryAdmissionJournalAt(dir, time.Now)
	if err != nil {
		t.Fatalf("reload journal: %v", err)
	}
	if _, err := reloaded.ResolveLocal(
		t.Context(),
		admissionID,
	); !errors.Is(err, ErrLocalNotFound) {
		t.Fatalf("reloaded durable journal retained process authority: %v", err)
	}
	journal.DeactivateMaterializationAuthority(coordinate)
	if _, err := journal.ResolveLocal(
		t.Context(),
		admissionID,
	); !errors.Is(err, ErrLocalNotFound) {
		t.Fatalf("released authority still resolved: %v", err)
	}
}

func TestLocalJournalReplaysExactIntentAndRejectsDivergence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	journal, err := NewRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "journal"),
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	intent := LocalIntent{
		OperationID:   " operation-1 ",
		WorkspaceKey:  " WORK ",
		WorkspaceName: " Work ",
		WorkspacePath: filepath.Join(t.TempDir(), "workspace"),
		Kind:          KindAddRepositories,
	}
	first, err := journal.Prepare(context.Background(), intent)
	if err != nil {
		t.Fatalf("prepare first: %v", err)
	}
	now = now.Add(time.Hour)
	replay, err := journal.Prepare(context.Background(), intent)
	if err != nil {
		t.Fatalf("prepare replay: %v", err)
	}
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("replay = %#v, want %#v", replay, first)
	}

	divergent := intent
	divergent.WorkspacePath = filepath.Join(t.TempDir(), "other")
	if _, err := journal.Prepare(context.Background(), divergent); !errors.Is(err, ErrLocalConflict) {
		t.Fatalf("divergent prepare error = %v, want conflict", err)
	}
}

func TestLocalJournalRejectsDivergentBindings(t *testing.T) {
	t.Parallel()
	journal, err := NewRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "journal"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	root := t.TempDir()
	for _, operationID := range []string{"operation-1", "operation-2"} {
		_, err := journal.Prepare(context.Background(), LocalIntent{
			OperationID:   operationID,
			WorkspaceKey:  "WORK",
			WorkspacePath: filepath.Join(root, operationID),
			Kind:          KindAddRepositories,
		})
		if err != nil {
			t.Fatalf("prepare %s: %v", operationID, err)
		}
	}
	const (
		admissionOne = "0123456789abcdef0123456789abcdef"
		admissionTwo = "abcdef0123456789abcdef0123456789"
		fingerprint  = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	)
	if _, err := journal.Bind(context.Background(), "operation-1", admissionOne, fingerprint); err != nil {
		t.Fatalf("bind first: %v", err)
	}
	if _, err := journal.Bind(context.Background(), "operation-1", admissionOne, fingerprint); err != nil {
		t.Fatalf("replay bind: %v", err)
	}
	if _, err := journal.Bind(context.Background(), "operation-1", admissionTwo, fingerprint); !errors.Is(err, ErrLocalConflict) {
		t.Fatalf("divergent operation bind error = %v, want conflict", err)
	}
	if _, err := journal.Bind(context.Background(), "operation-2", admissionOne, fingerprint); !errors.Is(err, ErrLocalConflict) {
		t.Fatalf("duplicate admission bind error = %v, want conflict", err)
	}
	if _, err := journal.Bind(context.Background(), "missing", "fedcba9876543210fedcba9876543210", fingerprint); !errors.Is(err, ErrLocalNotFound) {
		t.Fatalf("missing operation bind error = %v, want not found", err)
	}
	divergentFingerprint := "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if _, err := journal.Bind(context.Background(), "operation-1", admissionOne, divergentFingerprint); !errors.Is(err, ErrLocalConflict) {
		t.Fatalf("divergent fingerprint bind error = %v, want conflict", err)
	}
	rebound, err := journal.Rebind(
		context.Background(),
		"operation-1",
		admissionOne,
		fingerprint,
		admissionTwo,
		divergentFingerprint,
	)
	if err != nil {
		t.Fatalf("exact compare-and-swap rebind: %v", err)
	}
	if rebound.AdmissionID != admissionTwo ||
		rebound.SpecFingerprint != divergentFingerprint ||
		!slices.Equal(rebound.PreviousAdmissionIDs, []string{admissionOne}) {
		t.Fatalf("rebound coordinates = %#v", rebound)
	}
	byPreviousID, err := journal.GetByAdmission(context.Background(), admissionOne)
	if err != nil || byPreviousID.AdmissionID != admissionTwo {
		t.Fatalf("previous admission alias = %#v, err=%v", byPreviousID, err)
	}
	thirdAdmission := "fedcba9876543210fedcba9876543210"
	if _, err := journal.Rebind(
		context.Background(),
		"operation-1",
		admissionOne,
		fingerprint,
		thirdAdmission,
		divergentFingerprint,
	); !errors.Is(err, ErrLocalConflict) {
		t.Fatalf("stale compare-and-swap rebind error = %v, want conflict", err)
	}
	if _, err := journal.Bind(context.Background(), "operation-2", admissionOne, fingerprint); !errors.Is(err, ErrLocalConflict) {
		t.Fatalf("previous admission alias reuse error = %v, want conflict", err)
	}
}

func TestLocalJournalListAndRemoveAreDeterministic(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	journal, err := NewRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "journal"),
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	root := t.TempDir()
	for _, operationID := range []string{"operation-b", "operation-a"} {
		_, err := journal.Prepare(context.Background(), LocalIntent{
			OperationID:   operationID,
			WorkspaceKey:  "WORK",
			WorkspacePath: filepath.Join(root, operationID),
			Kind:          KindAddRepositories,
		})
		if err != nil {
			t.Fatalf("prepare %s: %v", operationID, err)
		}
	}
	records, err := journal.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 2 ||
		records[0].Intent.OperationID != "operation-a" ||
		records[1].Intent.OperationID != "operation-b" {
		t.Fatalf("records = %#v", records)
	}
	if err := journal.Remove(context.Background(), "operation-a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := journal.Remove(context.Background(), "operation-a"); err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
	if _, err := journal.GetByOperation(context.Background(), "operation-a"); !errors.Is(err, ErrLocalNotFound) {
		t.Fatalf("removed get error = %v, want not found", err)
	}
}

func TestLocalJournalFailsClosedOnCorruptOrUnknownFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "journal")
	journal, err := NewRepositoryAdmissionJournalAt(dir, time.Now)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, localJournalFileName)
	if err := os.WriteFile(path, []byte(`{"version":999,"records":{}}`), 0o600); err != nil {
		t.Fatalf("write unknown version: %v", err)
	}
	if _, err := journal.List(context.Background()); err == nil {
		t.Fatal("unknown version list error = nil")
	}
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	if _, err := journal.List(context.Background()); err == nil {
		t.Fatal("corrupt list error = nil")
	}
}

func TestLocalJournalValidatesCoordinatesAndCancellation(t *testing.T) {
	t.Parallel()
	journal, err := NewRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "journal"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	valid := LocalIntent{
		OperationID:   "operation-1",
		WorkspaceKey:  "WORK",
		WorkspacePath: filepath.Join(t.TempDir(), "workspace"),
		Kind:          KindCreateWorkspace,
	}
	for name, mutate := range map[string]func(*LocalIntent){
		"operation": func(intent *LocalIntent) { intent.OperationID = " " },
		"workspace": func(intent *LocalIntent) { intent.WorkspaceKey = " " },
		"path":      func(intent *LocalIntent) { intent.WorkspacePath = "relative" },
		"root":      func(intent *LocalIntent) { intent.WorkspacePath = string(filepath.Separator) },
		"kind":      func(intent *LocalIntent) { intent.Kind = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			intent := valid
			mutate(&intent)
			if _, err := journal.Prepare(context.Background(), intent); err == nil {
				t.Fatal("prepare error = nil")
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := journal.Prepare(ctx, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prepare error = %v, want context canceled", err)
	}
}

func TestLocalJournalRejectsCredentialBearingCloneSourceWithoutPersistence(
	t *testing.T,
) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "journal")
	journal, err := NewRepositoryAdmissionJournalAt(dir, time.Now)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	const secret = "phase5-secret-token"
	_, err = journal.Prepare(
		context.Background(),
		LocalIntent{
			OperationID:   "operation-secret",
			WorkspaceKey:  "WORK",
			WorkspaceName: "Work",
			WorkspacePath: filepath.Join(t.TempDir(), "workspace"),
			Kind:          KindCreateWorkspace,
			CloneURLs: []string{
				"https://" + secret + "@example.com/private.git",
			},
		},
	)
	if err == nil {
		t.Fatal("credential-bearing clone source was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("credential leaked through validation error: %v", err)
	}
	if _, statErr := os.Stat(
		filepath.Join(dir, localJournalFileName),
	); !os.IsNotExist(statErr) {
		t.Fatalf(
			"credential-bearing intent reached durable journal: stat error = %v",
			statErr,
		)
	}
}

func TestRepositoryAdmissionMaterializationLockSerializesAcrossJournalInstances(
	t *testing.T,
) {
	dir := filepath.Join(t.TempDir(), "journal")
	first, err := NewRepositoryAdmissionJournalAt(dir, time.Now)
	if err != nil {
		t.Fatalf("new first journal: %v", err)
	}
	second, err := NewRepositoryAdmissionJournalAt(dir, time.Now)
	if err != nil {
		t.Fatalf("new second journal: %v", err)
	}
	const admissionID = "0123456789abcdef0123456789abcdef"
	target := filepath.Join(t.TempDir(), "workspace", "repo")
	releaseFirst, err := first.AcquireMaterializationLock(
		context.Background(),
		admissionID,
		[]string{target},
	)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancelWait()
	if _, err := second.AcquireMaterializationLock(
		waitCtx,
		admissionID,
		[]string{target},
	); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended lock error = %v, want deadline exceeded", err)
	}
	releaseFirst()

	releaseSecond, err := second.AcquireMaterializationLock(
		context.Background(),
		admissionID,
		[]string{target},
	)
	if err != nil {
		t.Fatalf("acquire released lock: %v", err)
	}
	releaseSecond()
}

func TestRepositoryAdmissionMaterializationLockSerializesCanonicalTargetAcrossAdmissions(
	t *testing.T,
) {
	dir := filepath.Join(t.TempDir(), "journal")
	first, err := NewRepositoryAdmissionJournalAt(dir, time.Now)
	if err != nil {
		t.Fatalf("new first journal: %v", err)
	}
	second, err := NewRepositoryAdmissionJournalAt(dir, time.Now)
	if err != nil {
		t.Fatalf("new second journal: %v", err)
	}
	targetRoot := t.TempDir()
	target := filepath.Join(targetRoot, "workspace", "repo")
	releaseFirst, err := first.AcquireMaterializationLock(
		context.Background(),
		"0123456789abcdef0123456789abcdef",
		[]string{target},
	)
	if err != nil {
		t.Fatalf("acquire first target lock: %v", err)
	}
	defer releaseFirst()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancelWait()
	if _, err := second.AcquireMaterializationLock(
		waitCtx,
		"abcdef0123456789abcdef0123456789",
		[]string{filepath.Join(targetRoot, "workspace", ".", "repo")},
	); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canonical target contention error = %v, want deadline exceeded", err)
	}
}
