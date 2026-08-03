package workspacemgr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestWorkspaceAdmissionCoordinatorPreparesDurablyBeforeMaterialization(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	transport := sourceControlFor(st)
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	operations := NewStoreBackedWorkspaceAdmissionOperations(
		st,
		transport,
		journal,
		transport,
	)
	request := service.WorkspaceCreateRequest{
		Name: "durable-prepare", Type: "clone",
		CloneURLs: []string{src},
		Path:      filepath.Join(loomDir, "workspaces", "durable-prepare"),
	}

	jobID, err := operations.PrepareCreate(t.Context(), request)
	if err != nil {
		t.Fatalf("prepare create: %v", err)
	}
	if _, err := normalizeLocalRepositoryAdmissionID(jobID); err != nil {
		t.Fatalf("job ID %q is not the exact durable admission ID: %v", jobID, err)
	}
	preparedWorkspace, err := st.Workspaces().Get(t.Context(), "DURABLE-PREPARE")
	if err != nil {
		t.Fatalf("get prepared workspace: %v", err)
	}
	if preparedWorkspace.DefaultBranch != "" {
		t.Fatalf(
			"prepared workspace default branch = %q, want unresolved until clone commit",
			preparedWorkspace.DefaultBranch,
		)
	}
	if len(transport.commands) != 0 {
		t.Fatalf("prepare issued materialization commands: %#v", transport.commands)
	}
	if _, err := os.Stat(filepath.Join(request.Path, "app")); !os.IsNotExist(err) {
		t.Fatalf("prepare materialized a checkout before scheduling: %v", err)
	}
	job, found, err := operations.LookupJob(t.Context(), jobID)
	if err != nil || !found || job.Status != service.JobStatusRunning {
		t.Fatalf("prepared durable job = %#v, found=%t, err=%v", job, found, err)
	}

	result, err := operations.CreateWorkspace(t.Context(), request)
	if err != nil {
		t.Fatalf("run prepared create: %v", err)
	}
	if result.WorkspaceID != "DURABLE-PREPARE" {
		t.Fatalf("workspace ID = %q, want DURABLE-PREPARE", result.WorkspaceID)
	}
	restarted := NewStoreBackedWorkspaceAdmissionOperations(
		st,
		transport,
		journal,
		transport,
	)
	job, found, err = restarted.LookupJob(t.Context(), jobID)
	if err != nil || !found || job.Status != service.JobStatusDone ||
		job.WorkspaceID != "DURABLE-PREPARE" {
		t.Fatalf("restarted durable job = %#v, found=%t, err=%v", job, found, err)
	}
	registrations := restarted.RuntimeRegistrations()
	if len(registrations) != 2 {
		t.Fatalf("runtime registrations = %#v", registrations)
	}
	renewal := workspaceAdmissionRegistration(
		t,
		restarted,
		workspaceRepositoryAdmissionRenewalComponentID,
	)
	if !renewal.Policy.Immediate ||
		renewal.Policy.Cadence != 15*time.Second ||
		renewal.Policy.Timeout != 10*time.Second {
		t.Fatalf("renewal registration = %#v", renewal)
	}
	recovery := workspaceAdmissionRegistration(
		t,
		restarted,
		workspaceRepositoryAdmissionRecoveryComponentID,
	)
	if !recovery.Policy.Immediate ||
		recovery.Policy.Cadence != 15*time.Second ||
		recovery.Policy.Timeout != 6*time.Minute {
		t.Fatalf("recovery registration = %#v", recovery)
	}
}

func TestWorkspaceAdmissionRuntimeRecoversRetryablePartialCheckout(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	alpha := initTestGitRepo(t, t.TempDir(), "alpha")
	beta := initTestGitRepo(t, t.TempDir(), "beta")
	st := memstore.New()
	transport := sourceControlFor(st)
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	request := service.WorkspaceCreateRequest{
		Name: "runtime-recovery", Type: "clone",
		CloneURLs: []string{alpha, beta},
		Path:      filepath.Join(loomDir, "workspaces", "runtime-recovery"),
	}
	transport.failNextRepositoryRef = "beta"
	transport.failNextMaterializeError = sourcecontrol.ErrUnavailable
	first := NewStoreBackedWorkspaceAdmissionOperations(
		st,
		transport,
		journal,
		transport,
	)
	if _, err := first.CreateWorkspace(t.Context(), request); !errors.Is(
		err,
		sourcecontrol.ErrUnavailable,
	) {
		t.Fatalf("first create error = %v, want retryable interruption", err)
	}
	if _, err := os.Stat(filepath.Join(request.Path, "alpha", ".git")); err != nil {
		t.Fatalf("retained partial checkout missing: %v", err)
	}

	restarted := NewStoreBackedWorkspaceAdmissionOperations(
		st,
		transport,
		journal,
		transport,
	)
	registration := workspaceAdmissionRegistration(
		t,
		restarted,
		workspaceRepositoryAdmissionRecoveryComponentID,
	)
	if err := registration.Component.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("runtime recovery pass: %v", err)
	}
	if _, err := os.Stat(filepath.Join(request.Path, "beta", ".git")); err != nil {
		t.Fatalf("runtime recovery did not finish beta checkout: %v", err)
	}
	locals, err := journal.List(t.Context())
	if err != nil || len(locals) != 1 {
		t.Fatalf("local admissions = %#v, err=%v", locals, err)
	}
	job, found, err := restarted.LookupJob(
		t.Context(),
		locals[0].AdmissionID,
	)
	if err != nil || !found || job.Status != service.JobStatusDone {
		t.Fatalf("recovered job = %#v, found=%t, err=%v", job, found, err)
	}
}

func TestWorkspaceAdmissionLeaseRenewalTracksOnlyActiveMaterialization(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	transport := sourceControlFor(st)
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	operations := NewStoreBackedWorkspaceAdmissionOperations(
		st,
		transport,
		journal,
		transport,
	)
	request := service.WorkspaceCreateRequest{
		Name: "lease-renewal", Type: "clone",
		CloneURLs: []string{src},
		Path:      filepath.Join(loomDir, "workspaces", "lease-renewal"),
	}
	jobID, err := operations.PrepareCreate(t.Context(), request)
	if err != nil {
		t.Fatalf("prepare create: %v", err)
	}
	if len(transport.beginLeases) != 1 ||
		transport.beginLeases[0] != repositoryAdmissionLease {
		t.Fatalf(
			"begin leases = %v, want one %s lease",
			transport.beginLeases,
			repositoryAdmissionLease,
		)
	}

	transport.mu.Lock()
	transport.records[jobID].OwnerLeaseExpiresAt = time.Now().Add(10 * time.Second)
	transport.mu.Unlock()
	record, err := transport.GetRepositoryAdmission(
		t.Context(),
		"LEASE-RENEWAL",
		jobID,
	)
	if err != nil {
		t.Fatalf("get admission: %v", err)
	}
	staleVersion := record.Version
	renewal := workspaceAdmissionRegistration(
		t,
		operations,
		workspaceRepositoryAdmissionRenewalComponentID,
	)

	// Prepared is durable but is not yet a running materialization. The
	// renewal worker must not keep such an orphan alive indefinitely.
	if err := renewal.Component.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("renew unstarted pass: %v", err)
	}
	if transport.renewals != 0 {
		t.Fatalf("unstarted admission renewals = %d, want 0", transport.renewals)
	}

	_, record, release, err := operations.process.beginMaterialization(
		t.Context(),
		record,
		request.Path,
	)
	if err != nil {
		t.Fatalf("begin materialization: %v", err)
	}
	defer release()
	if err := renewal.Component.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("renew active pass: %v", err)
	}
	renewed, err := transport.GetRepositoryAdmission(
		t.Context(),
		"LEASE-RENEWAL",
		jobID,
	)
	if err != nil {
		t.Fatalf("get renewed admission: %v", err)
	}
	if transport.renewals != 2 ||
		renewed.Version != staleVersion+2 ||
		renewed.OwnerID != record.OwnerID ||
		renewed.OwnerGenerationID != record.OwnerGenerationID ||
		renewed.OwnerLeaseExpiresAt.Before(time.Now().Add(50*time.Second)) {
		t.Fatalf(
			"renewed admission = %#v, renewals=%d, stale=%#v",
			renewed,
			transport.renewals,
			record,
		)
	}

	// A terminal mutation that still holds the pre-renewal version refreshes
	// the exact owner generation under the local mutation lock.
	cause := sourcecontrol.ErrUnavailable
	if err := operations.process.fail(t.Context(), record, cause); !errors.Is(err, cause) {
		t.Fatalf("fail after renewal = %v, want original cause", err)
	}
	failed, err := transport.GetRepositoryAdmission(
		t.Context(),
		"LEASE-RENEWAL",
		jobID,
	)
	if err != nil || failed.State != "retryable_failed" ||
		failed.Version != staleVersion+3 {
		t.Fatalf("failed admission = %#v, err=%v", failed, err)
	}
}

func TestWorkspaceAdmissionRuntimeRecoversUnstartedPreparedJobWithoutRenewingIt(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	transport := sourceControlFor(st)
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		func() time.Time { return time.Now().Add(-time.Minute) },
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	operations := NewStoreBackedWorkspaceAdmissionOperations(
		st,
		transport,
		journal,
		transport,
	)
	now := time.Now()
	operations.process.now = func() time.Time { return now }
	transport.now = func() time.Time { return now }
	request := service.WorkspaceCreateRequest{
		Name: "unstarted-recovery", Type: "clone",
		CloneURLs: []string{src},
		Path:      filepath.Join(loomDir, "workspaces", "unstarted-recovery"),
	}
	jobID, err := operations.PrepareCreate(t.Context(), request)
	if err != nil {
		t.Fatalf("prepare create: %v", err)
	}
	renewal := workspaceAdmissionRegistration(
		t,
		operations,
		workspaceRepositoryAdmissionRenewalComponentID,
	)
	if err := renewal.Component.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("renewal pass: %v", err)
	}
	if transport.renewals != 0 {
		t.Fatalf("unstarted job renewals = %d, want 0", transport.renewals)
	}

	recovery := workspaceAdmissionRegistration(
		t,
		operations,
		workspaceRepositoryAdmissionRecoveryComponentID,
	)
	if err := recovery.Component.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("recently prepared recovery pass: %v", err)
	}
	if len(transport.commands) != 0 {
		t.Fatalf(
			"recently prepared job materialized before scheduling grace: %#v",
			transport.commands,
		)
	}
	if transport.renewals != 0 {
		t.Fatalf(
			"recently prepared job was kept alive before scheduling grace: renewals=%d",
			transport.renewals,
		)
	}
	now = now.Add(workspaceRepositoryAdmissionStartGrace + time.Second)
	if err := recovery.Component.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("orphan recovery pass: %v", err)
	}
	record, err := transport.GetRepositoryAdmission(
		t.Context(),
		"UNSTARTED-RECOVERY",
		jobID,
	)
	if err != nil || record.State != "committed" {
		t.Fatalf("recovered admission = %#v, err=%v", record, err)
	}
	if transport.renewals != 1 || transport.recoveryClaims != 0 {
		t.Fatalf(
			"unstarted recovery renewals=%d claims=%d, want one start renewal and same-owner replay",
			transport.renewals,
			transport.recoveryClaims,
		)
	}
}

func TestWorkspaceAdmissionRuntimeClaimsExpiredOwnerAfterHardCrash(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	transport := sourceControlFor(st)
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	request := service.WorkspaceCreateRequest{
		Name: "hard-crash", Type: "clone",
		CloneURLs: []string{src},
		Path:      filepath.Join(loomDir, "workspaces", "hard-crash"),
	}
	first := NewStoreBackedWorkspaceAdmissionOperations(
		st,
		transport,
		journal,
		transport,
	)
	jobID, err := first.PrepareCreate(t.Context(), request)
	if err != nil {
		t.Fatalf("prepare create: %v", err)
	}
	old, err := transport.GetRepositoryAdmission(
		t.Context(),
		"HARD-CRASH",
		jobID,
	)
	if err != nil {
		t.Fatalf("get old admission: %v", err)
	}
	if len(transport.beginLeases) != 1 ||
		transport.beginLeases[0] != time.Minute {
		t.Fatalf("hard-crash lease = %v, want 1m", transport.beginLeases)
	}
	oldGuard := repositoryAdmissionGuard(old)

	// A new serve incarnation cannot steal a live lease.
	restarted := NewStoreBackedWorkspaceAdmissionOperations(
		st,
		transport,
		journal,
		transport,
	)
	recovery := workspaceAdmissionRegistration(
		t,
		restarted,
		workspaceRepositoryAdmissionRecoveryComponentID,
	)
	if err := recovery.Component.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("live-owner recovery pass: %v", err)
	}
	if transport.recoveryClaims != 0 || len(transport.commands) != 0 {
		t.Fatalf(
			"live owner was disturbed: claims=%d commands=%#v",
			transport.recoveryClaims,
			transport.commands,
		)
	}

	// Expiry bounds hard-crash takeover to at most the 60s lease plus the
	// 15s recovery cadence. Recovery mints a new owner generation.
	transport.mu.Lock()
	transport.records[jobID].OwnerLeaseExpiresAt = time.Now().Add(-time.Second)
	transport.mu.Unlock()
	restarted.process.now = func() time.Time {
		return time.Now().Add(-24 * time.Hour)
	}
	if err := recovery.Component.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("authoritative expired-owner recovery with skewed Loom clock: %v", err)
	}
	recovered, err := transport.GetRepositoryAdmission(
		t.Context(),
		"HARD-CRASH",
		jobID,
	)
	if err != nil || recovered.State != "committed" {
		t.Fatalf("recovered admission = %#v, err=%v", recovered, err)
	}
	if transport.recoveryClaims != 1 ||
		recovered.OwnerID != restarted.process.ownerID ||
		recovered.OwnerID == old.OwnerID ||
		recovered.OwnerGenerationID == old.OwnerGenerationID {
		t.Fatalf(
			"recovery fence = %#v, old=%#v, claims=%d",
			recovered,
			old,
			transport.recoveryClaims,
		)
	}
	if _, err := transport.RenewRepositoryAdmission(
		t.Context(),
		infrafleetdb.RepositoryAdmissionRenewInput{
			RepositoryAdmissionGuard: oldGuard,
			Lease:                    repositoryAdmissionLease,
		},
	); !errors.Is(err, infrafleetdb.ErrRepositoryAdmissionFenceLost) {
		t.Fatalf("old owner renewal error = %v, want fence lost", err)
	}
}

func workspaceAdmissionRegistration(
	t *testing.T,
	operations *StoreBackedWorkspaceAdmissionOperations,
	id platformruntime.ComponentID,
) platformruntime.Registration {
	t.Helper()
	for _, registration := range operations.RuntimeRegistrations() {
		if registration.Component.ID() == id {
			return registration
		}
	}
	t.Fatalf("runtime registration %q not found", id)
	return platformruntime.Registration{}
}
