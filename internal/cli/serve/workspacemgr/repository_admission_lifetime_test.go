package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

type concurrentRenewalTransport struct {
	infrafleetdb.RepositoryAdmissionTransport
	mu        sync.Mutex
	records   map[string]*infrafleetdb.RepositoryAdmissionRecord
	hangingID string
	hangOnce  sync.Once
	hangStart chan struct{}
	renewed   chan string
}

type saturatedRenewalTransport struct {
	infrafleetdb.RepositoryAdmissionTransport
	mu       sync.Mutex
	records  map[string]*infrafleetdb.RepositoryAdmissionRecord
	hanging  map[string]bool
	attempts []string
	renewed  chan string
}

type shortGrantRenewalTransport struct {
	infrafleetdb.RepositoryAdmissionTransport
	current *infrafleetdb.RepositoryAdmissionRecord
	renewed *infrafleetdb.RepositoryAdmissionRecord
}

func (transport *shortGrantRenewalTransport) GetRepositoryAdmission(
	context.Context,
	string,
	string,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	return cloneTestAdmission(transport.current), nil
}

func (transport *shortGrantRenewalTransport) RenewRepositoryAdmission(
	context.Context,
	infrafleetdb.RepositoryAdmissionRenewInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	transport.current = cloneTestAdmission(transport.renewed)
	return cloneTestAdmission(transport.renewed), nil
}

func (transport *saturatedRenewalTransport) GetRepositoryAdmission(
	_ context.Context,
	_ string,
	admissionID string,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	record := transport.records[admissionID]
	if record == nil {
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	return cloneTestAdmission(record), nil
}

func (transport *saturatedRenewalTransport) RenewRepositoryAdmission(
	ctx context.Context,
	input infrafleetdb.RepositoryAdmissionRenewInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	transport.mu.Lock()
	transport.attempts = append(transport.attempts, input.AdmissionID)
	hanging := transport.hanging[input.AdmissionID]
	transport.mu.Unlock()
	if hanging {
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}
	transport.mu.Lock()
	record := transport.records[input.AdmissionID]
	if record == nil {
		transport.mu.Unlock()
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	renewed := cloneTestAdmission(record)
	renewed.Version++
	renewed.UpdatedAt = record.UpdatedAt.Add(time.Second)
	renewed.OwnerLeaseExpiresAt = record.OwnerLeaseExpiresAt.Add(input.Lease)
	transport.records[input.AdmissionID] = renewed
	transport.mu.Unlock()
	transport.renewed <- input.AdmissionID
	return cloneTestAdmission(renewed), nil
}

func (transport *concurrentRenewalTransport) GetRepositoryAdmission(
	_ context.Context,
	_ string,
	admissionID string,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	record := transport.records[admissionID]
	if record == nil {
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	return cloneTestAdmission(record), nil
}

func (transport *concurrentRenewalTransport) RenewRepositoryAdmission(
	ctx context.Context,
	input infrafleetdb.RepositoryAdmissionRenewInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	if input.AdmissionID == transport.hangingID {
		transport.hangOnce.Do(func() { close(transport.hangStart) })
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}
	transport.mu.Lock()
	record := transport.records[input.AdmissionID]
	if record == nil {
		transport.mu.Unlock()
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	renewed := cloneTestAdmission(record)
	renewed.Version++
	renewed.UpdatedAt = record.UpdatedAt.Add(time.Second)
	renewed.OwnerLeaseExpiresAt = record.OwnerLeaseExpiresAt.Add(input.Lease)
	transport.records[input.AdmissionID] = renewed
	transport.mu.Unlock()
	transport.renewed <- input.AdmissionID
	return cloneTestAdmission(renewed), nil
}

func TestRepositoryAdmissionRenewalHangDoesNotStarveAnotherOwner(t *testing.T) {
	now := time.Now().UTC()
	first := testPendingRepositoryAdmission(now)
	first.AdmissionID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first.OperationID = "workspace-add_repositories:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first.Spec.OperationID = first.OperationID
	second := cloneTestAdmission(first)
	second.AdmissionID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	second.OperationID = "workspace-add_repositories:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	second.Spec.OperationID = second.OperationID
	second.OwnerGenerationID = "22222222222222222222222222222222"
	transport := &concurrentRenewalTransport{
		records: map[string]*infrafleetdb.RepositoryAdmissionRecord{
			first.AdmissionID: first, second.AdmissionID: second,
		},
		hangingID: first.AdmissionID,
		hangStart: make(chan struct{}),
		renewed:   make(chan string, 1),
	}
	process := &repositoryAdmissionProcess{
		admissions: transport, ownerID: first.OwnerID,
		now: time.Now, leases: newRepositoryAdmissionLeaseState(),
		ownerLease:        repositoryAdmissionLease,
		leaseSafetyMargin: repositoryAdmissionLeaseSafetyMargin,
	}
	for _, record := range []*infrafleetdb.RepositoryAdmissionRecord{first, second} {
		coordinate, err := repositoryAdmissionCoordinateFromRecord(record)
		if err != nil {
			t.Fatalf("coordinate: %v", err)
		}
		_, cancel := context.WithCancelCause(t.Context())
		process.leases.active[record.AdmissionID] = &activeRepositoryAdmission{
			coordinate: coordinate, owned: true, cancel: cancel,
			deadlineUpdates: make(chan time.Time, 1),
		}
	}
	passCtx, cancelPass := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancelPass()
	passResult := make(chan error, 1)
	go func() {
		passResult <- process.renewActiveRepositoryAdmissions(passCtx)
	}()
	select {
	case <-transport.hangStart:
	case <-time.After(time.Second):
		t.Fatal("first renewal did not hang")
	}
	select {
	case admissionID := <-transport.renewed:
		if admissionID != second.AdmissionID {
			t.Fatalf("renewed admission = %q, want second", admissionID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second admission was starved behind hanging first renewal")
	}
	if err := <-passResult; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("renewal pass error = %v, want hanging-attempt deadline", err)
	}
	transport.mu.Lock()
	secondVersion := transport.records[second.AdmissionID].Version
	transport.mu.Unlock()
	if secondVersion != second.Version+1 {
		t.Fatalf("second version = %d, want %d", secondVersion, second.Version+1)
	}
}

func TestRepositoryAdmissionRenewalCursorEscapesSaturatedHungPrefix(
	t *testing.T,
) {
	const hungAdmissions = 2*repositoryAdmissionRenewalConcurrency + 1
	now := time.Now().UTC()
	transport := &saturatedRenewalTransport{
		records: make(map[string]*infrafleetdb.RepositoryAdmissionRecord),
		hanging: make(map[string]bool),
		renewed: make(chan string, 1),
	}
	process := &repositoryAdmissionProcess{
		admissions:        transport,
		ownerID:           "loom-workspace-admission-saturated-renewal",
		now:               time.Now,
		leases:            newRepositoryAdmissionLeaseState(),
		ownerLease:        repositoryAdmissionLease,
		leaseSafetyMargin: repositoryAdmissionLeaseSafetyMargin,
	}
	for index := range hungAdmissions + 1 {
		record := testPendingRepositoryAdmission(now)
		record.AdmissionID = fmt.Sprintf("%032x", index+1)
		record.OperationID = "workspace-add_repositories:" + record.AdmissionID
		record.Spec.OperationID = record.OperationID
		record.OwnerID = process.ownerID
		record.OwnerLeaseExpiresAt = now.Add(time.Minute)
		transport.records[record.AdmissionID] = record
		if index < hungAdmissions {
			transport.hanging[record.AdmissionID] = true
		}
		coordinate, err := repositoryAdmissionCoordinateFromRecord(record)
		if err != nil {
			t.Fatalf("coordinate %d: %v", index, err)
		}
		_, cancel := context.WithCancelCause(t.Context())
		process.leases.active[record.AdmissionID] = &activeRepositoryAdmission{
			coordinate:      coordinate,
			owned:           true,
			cancel:          cancel,
			deadlineUpdates: make(chan time.Time, 1),
		}
	}
	healthyID := fmt.Sprintf("%032x", hungAdmissions+1)
	for pass := range 3 {
		passCtx, cancelPass := context.WithTimeout(
			t.Context(),
			100*time.Millisecond,
		)
		err := process.renewActiveRepositoryAdmissions(passCtx)
		cancelPass()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("renewal pass %d error = %v, want saturated deadline", pass+1, err)
		}
		if pass < 2 {
			select {
			case admissionID := <-transport.renewed:
				t.Fatalf(
					"healthy admission %q renewed before cursor reached saturated suffix",
					admissionID,
				)
			default:
			}
		}
	}
	select {
	case admissionID := <-transport.renewed:
		if admissionID != healthyID {
			t.Fatalf("renewed admission = %q, want healthy suffix %q", admissionID, healthyID)
		}
	default:
		t.Fatal("rotating renewal cursor never serviced healthy admission after hung prefix")
	}
	transport.mu.Lock()
	attempts := append([]string(nil), transport.attempts...)
	healthyVersion := transport.records[healthyID].Version
	transport.mu.Unlock()
	if healthyVersion != testPendingRepositoryAdmission(now).Version+1 {
		t.Fatalf("healthy admission version = %d, want one renewal", healthyVersion)
	}
	if len(attempts) <= 2*repositoryAdmissionRenewalConcurrency {
		t.Fatalf("renewal attempts = %d, saturation regression did not exceed two worker waves", len(attempts))
	}
}

func TestRepositoryAdmissionAuthorityDeadlineUsesShortFleetGrant(t *testing.T) {
	serverNow := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	previous := testPendingRepositoryAdmission(serverNow)
	previous.OwnerLeaseExpiresAt = serverNow.Add(time.Second)
	renewed := cloneTestAdmission(previous)
	renewed.Version++
	renewed.UpdatedAt = serverNow.Add(900 * time.Millisecond)
	renewed.OwnerLeaseExpiresAt = renewed.UpdatedAt.Add(2 * time.Second)
	if _, err := (&repositoryAdmissionProcess{
		ownerLease: time.Minute,
	}).materializationLeaseDeadline(
		renewed,
		time.Now(),
	); !errors.Is(err, infrafleetdb.ErrRepositoryAdmissionInvalid) {
		t.Fatalf(
			"default 5s safety margin accepted a 2s Fleet grant: %v",
			err,
		)
	}
	transport := &shortGrantRenewalTransport{
		current: previous,
		renewed: renewed,
	}
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	if _, err := journal.prepare(t.Context(), localRepositoryAdmissionIntent{
		OperationID:   previous.OperationID,
		WorkspaceKey:  previous.WorkspaceKey,
		WorkspacePath: workspacePath,
		Kind:          localRepositoryAdmissionAddRepositories,
	}); err != nil {
		t.Fatalf("prepare local admission: %v", err)
	}
	if _, err := journal.bind(
		t.Context(),
		previous.OperationID,
		previous.AdmissionID,
		previous.SpecFingerprint,
	); err != nil {
		t.Fatalf("bind local admission: %v", err)
	}
	process := &repositoryAdmissionProcess{
		admissions:        transport,
		journal:           journal,
		ownerID:           previous.OwnerID,
		now:               time.Now,
		leases:            newRepositoryAdmissionLeaseState(),
		ownerLease:        time.Minute,
		leaseSafetyMargin: 500 * time.Millisecond,
	}
	started := time.Now()
	_, _, release, err := process.beginMaterialization(
		t.Context(),
		previous,
		workspacePath,
	)
	if err != nil {
		t.Fatalf("begin short-grant materialization: %v", err)
	}
	defer release()

	deadline, active := journal.materializationAuthorityDeadline(previous.AdmissionID)
	if !active {
		t.Fatal("short Fleet grant did not activate local authority")
	}
	windowFromCallStart := deadline.Sub(started)
	if windowFromCallStart <= 500*time.Millisecond ||
		windowFromCallStart > 2*time.Second {
		t.Fatalf(
			"short 2s Fleet grant produced local authority window %s; want about 1.5s, never the configured 59.5s",
			windowFromCallStart,
		)
	}
}

func TestRepositoryAdmissionBlockedLocalWorktreeIsCanceledAndCleaned(t *testing.T) {
	repository := initTestGitRepo(t, t.TempDir(), "local")
	gitDir := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "--git-dir"))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repository, gitDir)
	}
	started := filepath.Join(t.TempDir(), "hook-started")
	hook := "#!/bin/sh\n" +
		"touch '" + started + "'\n" +
		"while :; do sleep 1; done\n"
	hookPath := filepath.Join(gitDir, "hooks", "post-checkout")
	if err := os.WriteFile(hookPath, []byte(hook), 0o755); err != nil {
		t.Fatalf("write blocking post-checkout hook: %v", err)
	}
	target := filepath.Join(t.TempDir(), "workspace", "local")
	branch := strings.TrimSpace(gitOutput(t, repository, "branch", "--show-current"))
	ctx, cancel := context.WithCancelCause(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := createWorkspaceWorktreeContext(
			ctx,
			resolvedRepo{path: repository, name: "local"},
			target,
			branch,
		)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("blocking worktree hook did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	fenceErr := infrafleetdb.ErrRepositoryAdmissionFenceLost
	cancel(fenceErr)
	select {
	case err := <-result:
		if !errors.Is(err, fenceErr) {
			t.Fatalf("blocked worktree error = %v, want fence loss", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked local worktree did not stop after fence loss")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("canceled local worktree target remains: stat error = %v", err)
	}
}

func TestRepositoryAdmissionBlockedCloneCancelsBeforeTakeoverAndCannotPublish(
	t *testing.T,
) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	alpha := initTestGitRepo(t, t.TempDir(), "alpha")
	beta := initTestGitRepo(t, t.TempDir(), "beta")
	gamma := initTestGitRepo(t, t.TempDir(), "gamma")
	st := memstore.New()
	transport := sourceControlFor(st)
	// The stale owner must make real durable progress before it loses its
	// lease. Alpha completes, beta blocks until the local watchdog fences the
	// operation, and gamma must remain completely untouched by that owner.
	transport.blockNextRepositoryRef = "beta"
	transport.blockMaterializeStarted = make(chan struct{})
	transport.blockMaterializeCanceled = make(chan struct{})
	transport.blockMaterializeRelease = make(chan struct{})
	journalDir := filepath.Join(t.TempDir(), "admissions")
	firstJournal, err := newLocalRepositoryAdmissionJournalAt(journalDir, time.Now)
	if err != nil {
		t.Fatalf("new first journal: %v", err)
	}
	secondJournal, err := newLocalRepositoryAdmissionJournalAt(journalDir, time.Now)
	if err != nil {
		t.Fatalf("new second journal: %v", err)
	}
	first := newTestStoreBackedWorkspaceAdmissionOperations(t,
		st,
		managedAgentsForTest(st),
		transport,
		firstJournal,
		transport,
	)
	second := newTestStoreBackedWorkspaceAdmissionOperations(t,
		st,
		managedAgentsForTest(st),
		transport,
		secondJournal,
		transport,
	)
	first.process.ownerLease = 200 * time.Millisecond
	first.process.leaseSafetyMargin = 10 * time.Millisecond
	// Recovery receives a normal-length generation so race instrumentation and
	// three real Git checkouts cannot accidentally turn this stale-owner test
	// into a second, unrelated successor-expiry test.
	second.process.ownerLease = 5 * time.Second
	second.process.leaseSafetyMargin = 500 * time.Millisecond
	request := workspacecoord.WorkspaceCreateRequest{
		Name: "fenced-clone", Type: "clone",
		Path:      filepath.Join(loomDir, "workspaces", "fenced-clone"),
		CloneURLs: []string{alpha, beta, gamma},
	}

	firstResult := make(chan error, 1)
	go func() {
		_, createErr := first.CreateWorkspace(context.Background(), request)
		firstResult <- createErr
	}()
	select {
	case <-transport.blockMaterializeStarted:
	case <-time.After(time.Second):
		t.Fatal("second clone did not block")
	}
	alphaPath := filepath.Join(request.Path, "alpha")
	if _, err := os.Stat(filepath.Join(alphaPath, ".git")); err != nil {
		t.Fatalf("first repository did not complete before second blocked: %v", err)
	}
	select {
	case <-transport.blockMaterializeCanceled:
	case <-time.After(time.Second):
		t.Fatal("owner watchdog did not cancel the blocked clone")
	}

	secondResult := make(chan error, 1)
	go func() {
		_, createErr := second.CreateWorkspace(context.Background(), request)
		secondResult <- createErr
	}()
	// Keep the stale clone stack deliberately blocked after it observes
	// cancellation. The successor must remain behind the OS lock and therefore
	// cannot claim a new Fleet generation or publish any local projection.
	time.Sleep(30 * time.Millisecond)
	transport.mu.Lock()
	claimsBeforeRelease := transport.recoveryClaims
	commandCountBeforeRelease := len(transport.commands)
	commandsBeforeRelease := append(
		[]sourcecontrol.RepositoryAdmissionCheckoutCommand(nil),
		transport.commands...,
	)
	transport.mu.Unlock()
	if claimsBeforeRelease != 0 {
		t.Fatalf("successor claimed before stale owner unwound: %d claims", claimsBeforeRelease)
	}
	if commandCountBeforeRelease != 2 {
		t.Fatalf(
			"multi-repo stale owner reached %d materializers, want completed alpha and blocked beta",
			commandCountBeforeRelease,
		)
	}
	if commandsBeforeRelease[0].RepositoryRef != "alpha" ||
		commandsBeforeRelease[1].RepositoryRef != "beta" {
		t.Fatalf(
			"stale owner command sequence = %#v, want alpha then beta",
			commandsBeforeRelease,
		)
	}
	if _, err := os.Stat(filepath.Join(request.Path, "gamma")); !os.IsNotExist(err) {
		t.Fatalf("stale owner touched third repository: stat error = %v", err)
	}
	select {
	case err := <-secondResult:
		t.Fatalf("successor escaped cross-process lock before stale unwind: %v", err)
	default:
	}
	cache, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state before takeover: %v", err)
	}
	if workspace, ok := cache.Workspaces["FENCED-CLONE"]; !ok ||
		len(workspace.Repos) != 0 {
		t.Fatalf("stale clone published repository state: %#v", workspace)
	}
	roles, err := st.Roles().List(t.Context(), "FENCED-CLONE")
	if err != nil {
		t.Fatalf("list roles before takeover: %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("stale clone published roles before takeover: %#v", roles)
	}
	if _, err := os.Stat(filepath.Join(request.Path, ".loom", "prompts")); !os.IsNotExist(err) {
		t.Fatalf("stale clone published prompt files: stat error = %v", err)
	}

	// The fake Fleet clock is wall time. Waiting beyond the exact 200ms lease
	// makes the owner-fenced recovery claim eligible while the stale stack is
	// still prevented from returning by blockMaterializeRelease.
	time.Sleep(30 * time.Millisecond)
	close(transport.blockMaterializeRelease)
	if err := <-firstResult; !errors.Is(
		err,
		infrafleetdb.ErrRepositoryAdmissionFenceLost,
	) {
		t.Fatalf("stale clone result = %v, want fence loss", err)
	}
	select {
	case err := <-secondResult:
		if err != nil {
			t.Fatalf("successor recovery failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("successor recovery did not finish")
	}
	transport.mu.Lock()
	finalClaims := transport.recoveryClaims
	finalCommands := len(transport.commands)
	finalPrepared := append(
		[]testPreparedRepositoryCheckout(nil),
		transport.prepared...,
	)
	transport.mu.Unlock()
	if finalClaims != 1 {
		t.Fatalf("recovery claims = %d, want one exact successor generation", finalClaims)
	}
	if finalCommands != 5 {
		t.Fatalf(
			"materializer commands = %d, want stale alpha/beta plus successor alpha/beta/gamma",
			finalCommands,
		)
	}
	var alphaReceipts []testPreparedRepositoryCheckout
	for _, prepared := range finalPrepared {
		if prepared.command.RepositoryRef == "alpha" {
			alphaReceipts = append(alphaReceipts, prepared)
		}
	}
	if len(alphaReceipts) != 2 {
		t.Fatalf("completed alpha materializations = %#v, want stale plus successor", alphaReceipts)
	}
	if alphaReceipts[0].receipt.Reused {
		t.Fatal("stale owner unexpectedly reused alpha instead of creating it")
	}
	if !alphaReceipts[1].receipt.Reused {
		t.Fatal("successor did not reuse the exact alpha checkout from stale owner")
	}
	if alphaReceipts[0].receipt.CheckoutPath != alphaReceipts[1].receipt.CheckoutPath ||
		alphaReceipts[1].receipt.CheckoutPath != alphaPath {
		t.Fatalf("alpha recovery paths diverged: %#v", alphaReceipts)
	}
	if alphaReceipts[0].command.OwnerGenerationID ==
		alphaReceipts[1].command.OwnerGenerationID {
		t.Fatalf("alpha was not retried under a successor generation: %#v", alphaReceipts)
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := os.Stat(filepath.Join(request.Path, name, ".git")); err != nil {
			t.Fatalf("successor checkout %q missing: %v", name, err)
		}
	}
	cache, err = bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load final state: %v", err)
	}
	if workspace, ok := cache.Workspaces["FENCED-CLONE"]; !ok ||
		len(workspace.Repos) != 3 {
		t.Fatalf("successor did not publish all repository state: %#v", workspace)
	}
	repositories, err := st.Repos().List(t.Context(), "FENCED-CLONE")
	if err != nil || len(repositories) != 3 {
		t.Fatalf("successor repository records = %#v, err=%v", repositories, err)
	}
	roles, err = st.Roles().List(t.Context(), "FENCED-CLONE")
	if err != nil || len(roles) == 0 {
		t.Fatalf("successor roles = %#v, err=%v", roles, err)
	}
}

func TestRepositoryAdmissionLocalBatchFenceRetainsWorktreeWithoutPartialState(
	t *testing.T,
) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	st := memstore.New()
	workspacePath := filepath.Join(loomDir, "workspaces", "local-batch")
	if _, err := buildTestCreateWorkspace(t, st)(
		t.Context(),
		workspacecoord.WorkspaceCreateRequest{
			Name: "local-batch", Type: "empty", Path: workspacePath,
		},
	); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}
	firstRepository := initTestGitRepo(t, t.TempDir(), "first")
	secondRepository := initTestGitRepo(t, t.TempDir(), "second")
	secondGitDir := strings.TrimSpace(
		gitOutput(t, secondRepository, "rev-parse", "--git-dir"),
	)
	if !filepath.IsAbs(secondGitDir) {
		secondGitDir = filepath.Join(secondRepository, secondGitDir)
	}
	hookStarted := filepath.Join(t.TempDir(), "second-hook-started")
	hook := "#!/bin/sh\n" +
		"touch '" + hookStarted + "'\n" +
		"while :; do sleep 1; done\n"
	if err := os.WriteFile(
		filepath.Join(secondGitDir, "hooks", "post-checkout"),
		[]byte(hook),
		0o755,
	); err != nil {
		t.Fatalf("write second blocking hook: %v", err)
	}

	transport := sourceControlFor(st)
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	operations := newTestStoreBackedWorkspaceAdmissionOperations(t,
		st,
		managedAgentsForTest(st),
		transport,
		journal,
		transport,
	)
	operations.process.ownerLease = time.Second
	operations.process.leaseSafetyMargin = 100 * time.Millisecond
	result := make(chan error, 1)
	go func() {
		_, addErr := operations.AddWorkspaceRepos(
			context.Background(),
			workspacecoord.WorkspaceAddReposRequest{
				WorkspaceID: "LOCAL-BATCH",
				Repos:       []string{firstRepository, secondRepository},
				Branch:      "local-batch-work",
			},
		)
		result <- addErr
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(hookStarted); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second local worktree did not reach blocking checkout hook")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-result:
		if !errors.Is(err, infrafleetdb.ErrRepositoryAdmissionFenceLost) {
			t.Fatalf("local batch result = %v, want fence loss", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local batch did not stop at its monotonic lease deadline")
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "first", ".git")); err != nil {
		t.Fatalf("first worktree retry progress was not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "second")); !os.IsNotExist(err) {
		t.Fatalf("canceled second worktree was not cleaned: %v", err)
	}
	state, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state after local fence: %v", err)
	}
	local := state.Workspaces["LOCAL-BATCH"]
	if _, published := local.Repos["first"]; published {
		t.Fatalf("first local worktree leaked into partial state: %#v", local)
	}
	if _, published := local.Repos["second"]; published {
		t.Fatalf("second local worktree leaked into partial state: %#v", local)
	}
	repositories, err := st.Repos().List(t.Context(), "LOCAL-BATCH")
	if err != nil || len(repositories) != 0 {
		t.Fatalf("partial local batch persisted repos = %#v, err=%v", repositories, err)
	}
}

func TestRepositoryAdmissionStopCancelsAndWaitsForBlockedClone(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	repository := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	transport := sourceControlFor(st)
	transport.blockNextRepositoryRef = "app"
	transport.blockMaterializeStarted = make(chan struct{})
	transport.blockMaterializeCanceled = make(chan struct{})
	transport.blockMaterializeRelease = make(chan struct{})
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	operations := newTestStoreBackedWorkspaceAdmissionOperations(t,
		st,
		managedAgentsForTest(st),
		transport,
		journal,
		transport,
	)
	request := workspacecoord.WorkspaceCreateRequest{
		Name: "stop-clone", Type: "clone",
		Path:      filepath.Join(loomDir, "workspaces", "stop-clone"),
		CloneURLs: []string{repository},
	}
	createResult := make(chan error, 1)
	go func() {
		_, createErr := operations.CreateWorkspace(context.Background(), request)
		createResult <- createErr
	}()
	select {
	case <-transport.blockMaterializeStarted:
	case <-time.After(time.Second):
		t.Fatal("clone did not block")
	}
	stopCtx, cancelStop := context.WithTimeout(t.Context(), time.Second)
	defer cancelStop()
	stopResult := make(chan error, 1)
	go func() {
		stopResult <- operations.Stop(stopCtx)
	}()
	select {
	case <-transport.blockMaterializeCanceled:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the owner-scoped clone context")
	}
	select {
	case err := <-stopResult:
		t.Fatalf("Stop returned before blocked clone unwound: %v", err)
	default:
	}
	close(transport.blockMaterializeRelease)
	if err := <-stopResult; err != nil {
		t.Fatalf("Stop after clone unwind: %v", err)
	}
	if err := <-createResult; !errors.Is(err, errRepositoryAdmissionProcessStopped) {
		t.Fatalf("stopped clone result = %v, want process-stopped cause", err)
	}
	activeAuthorities := journal.materializationAuthorityCount()
	if activeAuthorities != 0 {
		t.Fatalf("Stop retained %d local materialization authorities", activeAuthorities)
	}
}

func TestRepositoryAdmissionConcurrentBeginCannotEscapeStop(t *testing.T) {
	base := t.TempDir()
	for iteration := range 24 {
		st := memstore.New()
		transport := sourceControlFor(st)
		journal, err := newLocalRepositoryAdmissionJournalAt(
			filepath.Join(base, fmt.Sprintf("admissions-%02d", iteration)),
			time.Now,
		)
		if err != nil {
			t.Fatalf("iteration %d: new journal: %v", iteration, err)
		}
		operations := newTestStoreBackedWorkspaceAdmissionOperations(t,
			st,
			managedAgentsForTest(st),
			transport,
			journal,
			transport,
		)
		now := time.Now().UTC()
		record := testPendingRepositoryAdmission(now)
		record.AdmissionID = fmt.Sprintf("%032x", iteration+1)
		record.OperationID = "workspace-add_repositories:" + record.AdmissionID
		record.Spec.OperationID = record.OperationID
		record.OwnerID = operations.process.ownerID
		record.OwnerLeaseExpiresAt = now.Add(30 * time.Second)
		transport.records[record.AdmissionID] = cloneTestAdmission(record)

		start := make(chan struct{})
		beginResult := make(chan error, 1)
		go func() {
			<-start
			materializationCtx, _, release, beginErr :=
				operations.process.beginMaterialization(
					context.Background(),
					record,
					filepath.Join(base, fmt.Sprintf("workspace-%02d", iteration)),
				)
			if beginErr == nil {
				<-materializationCtx.Done()
				beginErr = context.Cause(materializationCtx)
				release()
			}
			beginResult <- beginErr
		}()
		stopResults := make(chan error, 2)
		for range 2 {
			go func() {
				<-start
				stopResults <- operations.Stop(t.Context())
			}()
		}
		close(start)

		for stopIndex := range 2 {
			if stopErr := <-stopResults; stopErr != nil {
				t.Fatalf(
					"iteration %d stop %d: %v",
					iteration,
					stopIndex,
					stopErr,
				)
			}
		}
		if beginErr := <-beginResult; !errors.Is(
			beginErr,
			errRepositoryAdmissionProcessStopped,
		) {
			t.Fatalf(
				"iteration %d racing begin = %v, want process stopped",
				iteration,
				beginErr,
			)
		}

		_, _, release, beginErr := operations.process.beginMaterialization(
			t.Context(),
			record,
			filepath.Join(base, fmt.Sprintf("workspace-%02d", iteration)),
		)
		if release != nil ||
			!errors.Is(beginErr, errRepositoryAdmissionProcessStopped) {
			t.Fatalf(
				"iteration %d post-stop begin = release %v, err %v",
				iteration,
				release != nil,
				beginErr,
			)
		}
		if recoveryErr := operations.recoverRepositoryAdmissions(t.Context()); !errors.Is(
			recoveryErr,
			errRepositoryAdmissionProcessStopped,
		) {
			t.Fatalf(
				"iteration %d post-stop recovery = %v, want process stopped",
				iteration,
				recoveryErr,
			)
		}
		operations.process.leases.activeMu.RLock()
		activeCount := len(operations.process.leases.active)
		operations.process.leases.activeMu.RUnlock()
		if activeCount != 0 {
			t.Fatalf("iteration %d active admissions after Stop = %d", iteration, activeCount)
		}
	}
}
