package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution/authoring"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type workflowRunSubmissionStub struct {
	execution.DriverRunAPI
	commands []execution.SubmitDriverRunCommand
}

type workflowRunCatalogStub struct {
	workflowcatalog.API
	driver                *workflowcatalog.Driver
	version               *workflowcatalog.DriverVersion
	getDriverErr          error
	requestedErr          error
	getDriverWorkspace    string
	getDriverRef          string
	getVersionCalls       int
	requestedVersionCalls int
}

func (stub *workflowRunCatalogStub) GetDriver(_ context.Context, workspace, ref string) (*workflowcatalog.Driver, error) {
	stub.getDriverWorkspace = workspace
	stub.getDriverRef = ref
	return stub.driver, stub.getDriverErr
}

func (stub *workflowRunCatalogStub) GetVersion(context.Context, string, string) (*workflowcatalog.DriverVersion, error) {
	stub.getVersionCalls++
	return stub.version, nil
}

func (stub *workflowRunCatalogStub) ResolveRequestedVersion(
	context.Context,
	authority.OperatorAuthority,
	string,
	string,
	string,
) (*workflowcatalog.RequestedVersion, error) {
	stub.requestedVersionCalls++
	if stub.requestedErr != nil {
		return nil, stub.requestedErr
	}
	return &workflowcatalog.RequestedVersion{Driver: stub.driver, Version: stub.version}, nil
}

type workflowRunCaptureExecution struct {
	execution.DriverRunAPI
	command execution.SubmitDriverRunCommand
}

func (stub *workflowRunCaptureExecution) SubmitDriverRun(
	_ context.Context,
	_ authority.OperatorAuthority,
	command execution.SubmitDriverRunCommand,
) (*execution.DriverRun, error) {
	stub.command = command
	return &execution.DriverRun{
		WorkspaceKey: command.WorkspaceKey, RunID: command.RunID,
		DriverID: command.DriverID, DriverVersionID: command.DriverVersionID,
		Entrypoint: command.Entrypoint, SourceKind: command.SourceKind, SourceRef: command.SourceRef,
		EpicID: command.EpicID, Status: execution.DriverRunQueued,
		Owner:          execution.Owner{ResourceKind: execution.ResourceDriverRun, ResourceID: command.RunID},
		IdempotencyKey: command.RequestID, Payload: append(json.RawMessage(nil), command.Payload...),
	}, nil
}

type workflowRunStoreTestExecution struct {
	execution.DriverRunAPI
	store store.Store
}

func (adapter workflowRunStoreTestExecution) GetDriverRun(
	ctx context.Context,
	workspace, runID string,
) (*execution.DriverRun, error) {
	run, err := adapter.store.DriverRuns().Get(ctx, workspace, runID)
	if err != nil {
		return nil, err
	}
	return workflowExecutionRun(run), nil
}

func (adapter workflowRunStoreTestExecution) ListDriverRuns(
	ctx context.Context,
	query execution.DriverRunQuery,
) ([]*execution.DriverRun, error) {
	runs, err := adapter.store.DriverRuns().List(ctx, query.WorkspaceKey, store.DriverRunFilter{
		DriverID: query.DriverID, EpicID: query.EpicID,
		AgentServiceID: query.AgentServiceID, Status: domain.DriverRunStatus(query.Status),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*execution.DriverRun, 0, len(runs))
	for _, run := range runs {
		if query.ParentRunID != "" && run.ParentRunID != query.ParentRunID {
			continue
		}
		out = append(out, workflowExecutionRun(run))
		if query.Limit > 0 && len(out) == query.Limit {
			break
		}
	}
	return out, nil
}

// workflowRunStoreTestCatalog exposes the in-memory fixture only through the
// current Workflow Catalog port.
type workflowRunStoreTestCatalog struct {
	workflowcatalog.API
	store *memstore.Store
}

func (adapter workflowRunStoreTestCatalog) GetDriver(
	ctx context.Context,
	workspace, driverRef string,
) (*workflowcatalog.Driver, error) {
	record, err := resolveWorkflowDriverForTest(ctx, adapter.store, workspace, driverRef)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, workflowcatalog.ErrNotFound
	}
	if record != nil && record.Revision == 0 {
		record.Revision = 1
	}
	return record, err
}

func (adapter workflowRunStoreTestCatalog) GetVersion(
	ctx context.Context,
	workspace, versionID string,
) (*workflowcatalog.DriverVersion, error) {
	record, err := adapter.store.DriverVersions().Get(ctx, workspace, versionID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, workflowcatalog.ErrNotFound
	}
	return record, err
}

func (adapter workflowRunStoreTestCatalog) AuthorVersion(
	context.Context,
	authority.OperatorAuthority,
	workflowcatalog.AuthorVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	return nil, errors.New("unexpected operator authoring in managed-builtin test adapter")
}

func (adapter workflowRunStoreTestCatalog) AuthorManagedVersion(
	ctx context.Context,
	_ authority.SystemAuthority,
	command workflowcatalog.AuthorManagedVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	intent := command.AuthorVersionCommand
	driverRecord, err := adapter.store.Drivers().Get(ctx, intent.WorkspaceKey, intent.DriverID)
	createdDriver := false
	if errors.Is(err, domain.ErrNotFound) {
		driverRecord, err = adapter.store.Drivers().Create(ctx, store.DriverCreate{
			WorkspaceKey: intent.WorkspaceKey,
			DriverID:     intent.DriverID,
			Name:         intent.DriverName,
			OwnerType:    workflowcatalog.DriverOwnerSystem,
			Status:       workflowcatalog.DriverStatusDraft,
			TrustLevel:   workflowcatalog.DriverTrustTrusted,
			Metadata:     map[string]string{},
		})
		createdDriver = err == nil
	}
	if err != nil {
		return nil, err
	}

	versionRecord, err := adapter.store.DriverVersions().Get(ctx, intent.WorkspaceKey, intent.VersionID)
	createdVersion := false
	reusedVersion := false
	if errors.Is(err, domain.ErrNotFound) {
		versions, listErr := adapter.store.DriverVersions().List(ctx, intent.WorkspaceKey, store.DriverVersionFilter{
			DriverID: intent.DriverID,
		})
		if listErr != nil {
			return nil, listErr
		}
		nextVersion := 1
		for _, existing := range versions {
			if existing != nil && existing.Version >= nextVersion {
				nextVersion = existing.Version + 1
			}
		}
		manifest := make(map[string]string, len(intent.Manifest)+1)
		for key, value := range intent.Manifest {
			manifest[key] = value
		}
		manifest[workflowcatalog.ManifestTrustLevelKey] = string(workflowcatalog.DriverTrustTrusted)
		versionRecord, err = adapter.store.DriverVersions().Create(ctx, store.DriverVersionCreate{
			WorkspaceKey:     intent.WorkspaceKey,
			VersionID:        intent.VersionID,
			DriverID:         intent.DriverID,
			Version:          nextVersion,
			SourceRef:        intent.SourceRef,
			SourceDigest:     intent.SourceDigest,
			BundleRef:        intent.BundleRef,
			BundleDigest:     intent.BundleDigest,
			Runtime:          intent.Runtime,
			Manifest:         manifest,
			BuildDiagnostics: intent.BuildDiagnostics,
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
			CreatedBy:        "system",
		})
		createdVersion = err == nil
	} else if err == nil {
		if versionRecord.DriverID != intent.DriverID ||
			versionRecord.SourceDigest != intent.SourceDigest ||
			versionRecord.BundleDigest != intent.BundleDigest {
			return nil, workflowcatalog.ErrAuthoringConflict
		}
		reusedVersion = true
	}
	if err != nil {
		return nil, err
	}

	activated := false
	if command.Activate {
		trusted := workflowcatalog.DriverTrustTrusted
		driverRecord, err = adapter.store.Drivers().Update(ctx, intent.WorkspaceKey, intent.DriverID, store.DriverUpdate{
			TrustLevel: &trusted,
		})
		if err != nil {
			return nil, err
		}
		if _, err = adapter.store.ApproveDriverVersionForTest(ctx, intent.WorkspaceKey, intent.DriverID, versionRecord.VersionID); err != nil {
			return nil, err
		}
		driverRecord, err = adapter.store.ActivateDriverVersionForTest(ctx, intent.WorkspaceKey, intent.DriverID, versionRecord.VersionID)
		if err != nil {
			return nil, err
		}
		driverRecord, err = adapter.store.UnapproveDriverVersionForTest(ctx, intent.WorkspaceKey, intent.DriverID, versionRecord.VersionID)
		if err != nil {
			return nil, err
		}
		activated = true
	}
	if driverRecord.Revision == 0 {
		driverRecord.Revision = 1
	}
	return &workflowcatalog.AuthorVersionResult{
		Action:            workflowcatalog.ActionAuthorManagedVersion,
		Driver:            driverRecord,
		Version:           versionRecord,
		CreatedDriver:     createdDriver,
		CreatedVersion:    createdVersion,
		ReusedVersion:     reusedVersion,
		Activated:         activated,
		CommittedRevision: driverRecord.Revision,
		SemanticImpact:    workflowcatalog.SemanticImpactVersionAuthored,
	}, nil
}

type workflowRunManagedBuiltinAuthority struct{}

func (workflowRunManagedBuiltinAuthority) AuthorityForManagedBuiltin(
	context.Context,
	string,
	string,
) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}

func (adapter workflowRunStoreTestExecution) SubmitDriverRun(
	ctx context.Context,
	_ authority.OperatorAuthority,
	command execution.SubmitDriverRunCommand,
) (*execution.DriverRun, error) {
	run, err := adapter.store.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: command.WorkspaceKey, RunID: command.RunID,
		DriverID: command.DriverID, DriverVersionID: command.DriverVersionID,
		Entrypoint: command.Entrypoint, SourceKind: command.SourceKind, SourceRef: command.SourceRef,
		EpicID: command.EpicID, ParentRunID: command.ParentRunID, TriggerBindingID: command.TriggerBindingID,
		IdempotencyKey: command.RequestID, Payload: append(json.RawMessage(nil), command.Payload...),
	})
	if err != nil {
		return nil, err
	}
	return workflowExecutionRun(run), nil
}

func workflowExecutionRun(run *domain.DriverRun) *execution.DriverRun {
	if run == nil {
		return nil
	}
	return &execution.DriverRun{
		WorkspaceKey: run.WorkspaceKey, RunID: run.RunID, DriverID: run.DriverID,
		DriverVersionID: run.DriverVersionID, Entrypoint: run.Entrypoint,
		SourceKind: run.SourceKind, SourceRef: run.SourceRef, EpicID: run.EpicID,
		ParentRunID: run.ParentRunID, TriggerBindingID: run.TriggerBindingID,
		Status:         execution.DriverRunStatus(run.Status),
		Owner:          execution.Owner{ResourceKind: execution.ResourceDriverRun, ResourceID: run.RunID},
		IdempotencyKey: run.IdempotencyKey, Payload: append(json.RawMessage(nil), run.Payload...),
		CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

type backendHealthQueryFunc func(string) (BackendHealth, bool)

func (query backendHealthQueryFunc) BackendHealth(name string) (BackendHealth, bool) {
	return query(name)
}

func newWorkflowTestModule(st *memstore.Store) *Module {
	catalog := &workflowRunStoreTestCatalog{store: st}
	return NewModule(Config{
		Store: st, Catalog: catalog, Authoring: catalog,
		Execution: workflowRunStoreTestExecution{store: st}, OperatorAuthority: workflowOperatorAuthorityStub{},
		PrepareWorkflowTarget: func(ctx context.Context, workspace, workflow string) (*workflowcatalog.Driver, error) {
			if workflowdefs.IsBuiltinWorkflow(workflow) {
				coordinator, err := appworkflowauthoring.New(workflowdefs.NewBundleStager())
				if err != nil {
					return nil, err
				}
				if err := coordinator.EnsureBuiltin(
					ctx,
					catalog,
					catalog,
					workflowRunManagedBuiltinAuthority{},
					workflowdefs.NewBuiltinSupport(),
					workspace,
					workflow,
				); err != nil {
					return nil, err
				}
			}
			return catalog.GetDriver(ctx, workspace, workflow)
		},
		TaskWorkflowRuns: readprojection.NewTaskWorkflowRunReader(
			st.TaskRuns(), st.TriggerEvents(), st.DriverRuns(),
		),
		BackendHealth: backendHealthQueryFunc(func(string) (BackendHealth, bool) {
			return BackendHealth{
				Available: true, Installed: true, APIKeySet: true, Message: "ready",
			}, true
		}),
	})
}

func (stub *workflowRunSubmissionStub) SubmitDriverRun(
	_ context.Context,
	_ authority.OperatorAuthority,
	command execution.SubmitDriverRunCommand,
) (*execution.DriverRun, error) {
	stub.commands = append(stub.commands, command)
	if len(stub.commands) == 1 {
		// Model a durable create whose response was lost before the caller saw
		// the accepted snapshot. The next HTTP call is the client replay.
		return nil, errors.New("simulated lost response")
	}
	return &execution.DriverRun{
		WorkspaceKey:    command.WorkspaceKey,
		RunID:           command.RunID,
		DriverID:        command.DriverID,
		DriverVersionID: command.DriverVersionID,
		Entrypoint:      command.Entrypoint,
		SourceKind:      command.SourceKind,
		SourceRef:       command.SourceRef,
		EpicID:          command.EpicID,
		Status:          execution.DriverRunQueued,
		Owner:           execution.Owner{ResourceKind: execution.ResourceDriverRun, ResourceID: command.RunID},
		IdempotencyKey:  command.RequestID,
		Payload:         append(json.RawMessage(nil), command.Payload...),
	}, nil
}

type workflowOperatorAuthorityStub struct{}

func (workflowOperatorAuthorityStub) ResolveOperatorAuthority(
	*http.Request,
	string,
	authority.Action,
) (authority.OperatorAuthority, error) {
	return authority.OperatorAuthority{}, nil
}

type workflowVersionAuthoringStub struct {
	operatorCommand workflowcatalog.AuthorVersionCommand
	operatorCalls   int
	managedCalls    int
}

func (stub *workflowVersionAuthoringStub) AuthorVersion(
	_ context.Context,
	_ authority.OperatorAuthority,
	command workflowcatalog.AuthorVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	stub.operatorCalls++
	stub.operatorCommand = command
	return &workflowcatalog.AuthorVersionResult{
		Driver: &workflowcatalog.Driver{
			WorkspaceKey: command.WorkspaceKey, DriverID: command.DriverID,
			Name: command.DriverName, Status: workflowcatalog.DriverStatusDraft,
		},
		Version: &workflowcatalog.DriverVersion{
			WorkspaceKey: command.WorkspaceKey, DriverID: command.DriverID, VersionID: command.VersionID,
			SourceRef: command.SourceRef, SourceDigest: command.SourceDigest,
			BundleRef: command.BundleRef, BundleDigest: command.BundleDigest,
			Runtime: command.Runtime, Manifest: command.Manifest, BuildDiagnostics: command.BuildDiagnostics,
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		},
		CreatedDriver: true, CreatedVersion: true,
	}, nil
}

func (stub *workflowVersionAuthoringStub) AuthorManagedVersion(
	context.Context,
	authority.SystemAuthority,
	workflowcatalog.AuthorManagedVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	stub.managedCalls++
	return nil, errors.New("unexpected managed authoring call")
}

type workflowRecordingOperatorAuthorityStub struct {
	actions []authority.Action
	err     error
}

func (stub *workflowRecordingOperatorAuthorityStub) ResolveOperatorAuthority(
	_ *http.Request,
	_ string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	stub.actions = append(stub.actions, action)
	return authority.OperatorAuthority{}, stub.err
}

func TestCreateDriverRunDelegatesResolvedSubmissionToExecution(t *testing.T) {
	const body = `{
		"driver_ref":"demo",
		"driver_version_id":"version-preview",
		"run_id":"run-explicit",
		"idempotency_key":"request-explicit",
		"entrypoint":"preview",
		"cli_command":"workflow-run",
		"epic_id":"epic-explicit",
		"payload":{"epicId":"epic-payload","nested":{"ok":true}}
	}`
	submissions := &workflowRunCaptureExecution{}
	catalog := &workflowRunCatalogStub{
		driver: &workflowcatalog.Driver{
			WorkspaceKey: "TEST", DriverID: "driver-demo", Name: "demo",
			ActiveVersionID: "version-active", Status: workflowcatalog.DriverStatusDraft,
		},
		version: &workflowcatalog.DriverVersion{
			WorkspaceKey: "TEST", DriverID: "driver-demo", VersionID: "version-preview",
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		},
	}
	resolver := &workflowRecordingOperatorAuthorityStub{}
	mux := http.NewServeMux()
	NewModule(Config{
		Catalog: catalog, Execution: submissions, OperatorAuthority: resolver,
	}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/execution/driver-runs", stringsReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	command := submissions.command
	if command.WorkspaceKey != "TEST" || command.DriverID != "driver-demo" || command.DriverVersionID != "version-preview" {
		t.Fatalf("resolved target = %#v", command)
	}
	if command.RunID != "run-explicit" || command.RequestID != "request-explicit" ||
		command.Entrypoint != "preview" || command.SourceKind != "cli" || command.SourceRef != "loom workflow run" ||
		command.EpicID != "epic-explicit" {
		t.Fatalf("preserved submission envelope = %#v", command)
	}
	if got, want := string(command.Payload), `{"epicId":"epic-payload","nested":{"ok":true}}`; got != want {
		t.Fatalf("payload = %s, want %s", got, want)
	}
	if catalog.requestedVersionCalls != 1 || catalog.getVersionCalls != 0 {
		t.Fatalf("requested resolver calls=%d raw GetVersion calls=%d", catalog.requestedVersionCalls, catalog.getVersionCalls)
	}
	if len(resolver.actions) != 2 || resolver.actions[0] != workflowcatalog.ActionResolveRequestedVersion ||
		resolver.actions[1] != execution.ActionSubmitDriverRun {
		t.Fatalf("authority actions = %v", resolver.actions)
	}
}

func TestCreateDriverRunFailsClosedWithoutCapabilityDependencies(t *testing.T) {
	mux := http.NewServeMux()
	NewModule(Config{}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/execution/driver-runs", stringsReader(`{"cli_command":"driver-run","driver_ref":"demo","payload":{}}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
}

func TestCreateDriverRunRejectsTrailingJSON(t *testing.T) {
	mux := http.NewServeMux()
	NewModule(Config{}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/execution/driver-runs", stringsReader(
		`{"cli_command":"driver-run","driver_ref":"demo","payload":{}} {"second":true}`,
	))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestCreateDriverRunDeniesWrongWorkspaceOrActionAuthority(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "wrong workspace", err: authority.ErrWorkspaceMismatch},
		{name: "action denied", err: authority.ErrActionNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			submissions := &workflowRunCaptureExecution{}
			catalog := &workflowRunCatalogStub{
				driver: &workflowcatalog.Driver{
					WorkspaceKey: "TEST", DriverID: "driver-demo", Name: "demo",
					ActiveVersionID: "version-active", Status: workflowcatalog.DriverStatusActive,
				},
				version: &workflowcatalog.DriverVersion{
					WorkspaceKey: "TEST", DriverID: "driver-demo", VersionID: "version-active",
					ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
				},
			}
			resolver := &workflowRecordingOperatorAuthorityStub{err: test.err}
			mux := http.NewServeMux()
			NewModule(Config{Catalog: catalog, Execution: submissions, OperatorAuthority: resolver}).Register(mux)
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/execution/driver-runs", stringsReader(
				`{"cli_command":"driver-run","driver_ref":"demo","payload":{}}`,
			))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body=%s, want 403", rec.Code, rec.Body.String())
			}
			if submissions.command.RunID != "" {
				t.Fatalf("Execution was called after denied authority: %#v", submissions.command)
			}
		})
	}
}

func TestCreateDriverRunServerStampsWorkflowBindingSourceRef(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	const workspace, driverID, versionID = "TEST", "driver-demo", "version-active"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: workspace}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: workspace, DriverID: driverID, Name: "demo",
		Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: workspace, DriverID: driverID, VersionID: versionID, Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	if _, err := st.ApproveDriverVersionForTest(ctx, workspace, driverID, versionID); err != nil {
		t.Fatalf("approve driver version: %v", err)
	}
	if _, err := st.ActivateDriverVersionForTest(ctx, workspace, driverID, versionID); err != nil {
		t.Fatalf("activate driver version: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: workspace, BindingID: "binding-demo", Name: "binding-demo",
		SourceKind: store.CronSourceKind, DriverID: driverID, DriverVersionID: versionID,
		Schedule: "*/10 * * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("create trigger binding: %v", err)
	}
	catalog := &workflowRunCatalogStub{
		driver: &workflowcatalog.Driver{
			WorkspaceKey: workspace, DriverID: driverID, Name: "demo",
			ActiveVersionID: versionID, Status: workflowcatalog.DriverStatusActive,
		},
		version: &workflowcatalog.DriverVersion{
			WorkspaceKey: workspace, DriverID: driverID, VersionID: versionID,
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		},
	}
	submissions := &workflowRunCaptureExecution{}
	mux := http.NewServeMux()
	NewModule(Config{
		Store: st, Catalog: catalog, Execution: submissions, OperatorAuthority: workflowOperatorAuthorityStub{},
	}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/execution/driver-runs", stringsReader(
		`{"cli_command":"workflow-run","driver_ref":"demo","payload":{"runner":"daytona-task-runner"}}`,
	))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	if got := submissions.command.SourceRef; got != "cron:binding-demo" {
		t.Fatalf("Execution SourceRef = %q, want server-resolved binding route", got)
	}
}

func TestCreateWorkflowRunLostResponseRetryKeepsRunIdentity(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	submissions := &workflowRunSubmissionStub{}
	mux := http.NewServeMux()
	NewModule(Config{
		Store: st, Execution: submissions, OperatorAuthority: workflowOperatorAuthorityStub{},
		PrepareWorkflowTarget: func(ctx context.Context, workspace, workflow string) (*workflowcatalog.Driver, error) {
			return resolveWorkflowDriverForTest(ctx, st, workspace, workflow)
		},
	}).Register(mux)

	const requestID = "workflow-retry-1"
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo", stringsReader(`{"nested":{"ok":true}}`))
		req.Header.Set("Idempotency-Key", requestID)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if first := post(); first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d body=%s, want simulated lost response", first.Code, first.Body.String())
	}
	second := post()
	if second.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d body=%s, want 202", second.Code, second.Body.String())
	}
	if len(submissions.commands) != 2 {
		t.Fatalf("submission calls = %d, want 2", len(submissions.commands))
	}
	wantRunID := workflowSubmissionRunID("TEST", "demo", requestID)
	for index, command := range submissions.commands {
		if command.RunID != wantRunID || command.RequestID != requestID {
			t.Fatalf("command[%d] identity = %q/%q, want %q/%q", index, command.RunID, command.RequestID, wantRunID, requestID)
		}
	}
	var run domain.DriverRun
	if err := json.Unmarshal(second.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode retry run: %v", err)
	}
	if run.RunID != wantRunID {
		t.Fatalf("retry run id = %q, want %q", run.RunID, wantRunID)
	}
}

func resolveWorkflowDriverForTest(
	ctx context.Context,
	st store.Store,
	workspace, workflow string,
) (*workflowcatalog.Driver, error) {
	record, err := st.Drivers().Get(ctx, workspace, workflow)
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	records, err := st.Drivers().List(ctx, workspace, store.DriverFilter{Name: workflow, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, domain.ErrNotFound
	}
	return records[0], nil
}

func TestCreateWorkflowRunPassesRawPayload(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo", stringsReader(`{"nested":{"ok":true},"items":[1,2]}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	var run domain.DriverRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	stored, err := st.DriverRuns().Get(ctx, "TEST", run.RunID)
	if err != nil {
		t.Fatalf("get stored run: %v", err)
	}
	if string(stored.Payload) != `{"nested":{"ok":true},"items":[1,2]}` {
		t.Fatalf("payload = %s, want raw request JSON", stored.Payload)
	}
}

func TestGetWorkflowSourceReturnsBuiltinFiles(t *testing.T) {
	mux := http.NewServeMux()
	newWorkflowTestModule(memstore.New()).Register(mux)

	req := httptest.NewRequest(http.MethodGet,
		"/api/workspaces/WS/workflows/"+workflowdefs.BuiltinBugFixAgentWorkflowName+"/source", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("source status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Name       string            `json:"name"`
		Builtin    bool              `json:"builtin"`
		Entrypoint string            `json:"entrypoint"`
		Files      map[string]string `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode source: %v", err)
	}
	if !got.Builtin || got.Entrypoint == "" || len(got.Files) == 0 {
		t.Fatalf("unexpected source response: %+v", got)
	}
}

func TestGetWorkflowSourceUnknownIs404(t *testing.T) {
	mux := http.NewServeMux()
	newWorkflowTestModule(memstore.New()).Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/workflows/not-a-workflow/source", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown source status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateWorkflowRunRegistersBuiltinEpicRunner(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1","requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	var run domain.DriverRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.DriverID != BuiltinEpicRunnerWorkflowName || string(run.Payload) != `{"epicId":"EPIC-1","requestedBy":"ui"}` {
		t.Fatalf("run = %+v payload=%s, want built-in epic runner with raw payload", run, run.Payload)
	}
	driverRecord, err := st.Drivers().Get(ctx, "TEST", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get built-in driver: %v", err)
	}
	if driverRecord.Status != workflowcatalog.DriverStatusActive || driverRecord.ActiveVersionID == "" {
		t.Fatalf("driver = %+v, want active built-in driver", driverRecord)
	}
	version, err := st.DriverVersions().Get(ctx, "TEST", driverRecord.ActiveVersionID)
	if err != nil {
		t.Fatalf("get built-in version: %v", err)
	}
	if !strings.HasPrefix(version.SourceRef, "builtin://workflows/"+BuiltinEpicRunnerWorkflowName+"/versions/") || version.CreatedBy != "system" {
		t.Fatalf("version = %+v, want system built-in source", version)
	}
}

func TestCreateWorkflowRunRefreshesStaleBuiltinRunnerManifest(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	spec, ok := workflowdefs.BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	digest, err := workflowdefs.SourceDigest(spec.Files)
	if err != nil {
		t.Fatalf("digest epic-runner source: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     BuiltinEpicRunnerWorkflowName,
		Name:         BuiltinEpicRunnerWorkflowName,
		OwnerType:    workflowcatalog.DriverOwnerUser,
		Status:       workflowcatalog.DriverStatusActive,
		TrustLevel:   workflowcatalog.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("create stale built-in driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "stale-version",
		DriverID:         BuiltinEpicRunnerWorkflowName,
		Version:          1,
		SourceRef:        "builtin://workflows/" + BuiltinEpicRunnerWorkflowName + "/versions/" + digest,
		SourceDigest:     digest,
		BundleDigest:     "sha256:stale",
		Runtime:          driver.RuntimeFlueNode,
		Manifest:         map[string]string{"workflow_name": BuiltinEpicRunnerWorkflowName},
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		CreatedBy:        "system",
	}); err != nil {
		t.Fatalf("create stale built-in version: %v", err)
	}
	if _, err := st.ApproveDriverVersionForTest(ctx, "TEST", BuiltinEpicRunnerWorkflowName, "stale-version"); err != nil {
		t.Fatalf("approve stale built-in version: %v", err)
	}
	if _, err := st.ActivateDriverVersionForTest(ctx, "TEST", BuiltinEpicRunnerWorkflowName, "stale-version"); err != nil {
		t.Fatalf("activate stale built-in version: %v", err)
	}

	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1","requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	driverRecord, err := st.Drivers().Get(ctx, "TEST", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get refreshed built-in driver: %v", err)
	}
	if driverRecord.ActiveVersionID == "stale-version" {
		t.Fatalf("active version was not refreshed")
	}
	version, err := st.DriverVersions().Get(ctx, "TEST", driverRecord.ActiveVersionID)
	if err != nil {
		t.Fatalf("get refreshed built-in version: %v", err)
	}
	if !strings.Contains(version.Manifest["runners"], "local-task-runner") {
		t.Fatalf("refreshed manifest runners = %q, want local-task-runner", version.Manifest["runners"])
	}
}

// TestCreateWorkflowRunPromotesPayloadEpicID proves the HTTP-triggered run
// path mirrors the `loom epic run` CLI: payload.epicId is promoted onto the
// DriverRun. Without it, terminal task transitions never fire the lead-task
// outbox (createLeadTaskOutbox gates on the run's EpicID), so webhook/HTTP
// epics silently skip lead notifications. The outbox assertion is end-to-end:
// a row only appears when createWorkflowRun set EpicID on the run.
func TestCreateWorkflowRunPromotesPayloadEpicID(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		wantEpicID string
		leadEpicID string // "" means no lead agent is bound
		wantOutbox int
	}{
		{
			name:       "epicId with lead bound creates lead-task outbox",
			payload:    `{"epicId":"EPIC-42","requestedBy":"webhook"}`,
			wantEpicID: "EPIC-42",
			leadEpicID: "EPIC-42",
			wantOutbox: 1,
		},
		{
			name:       "epicId without lead skips outbox",
			payload:    `{"epicId":"EPIC-42"}`,
			wantEpicID: "EPIC-42",
			leadEpicID: "",
			wantOutbox: 0,
		},
		{
			name:       "no epicId leaves run unbound and skips outbox",
			payload:    `{"requestedBy":"webhook"}`,
			wantEpicID: "",
			leadEpicID: "EPIC-42",
			wantOutbox: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := seededWorkflowStore(t, ctx)
			mux := http.NewServeMux()
			newWorkflowTestModule(st).Register(mux)

			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo", stringsReader(tc.payload))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
			}
			var run domain.DriverRun
			if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
				t.Fatalf("decode run: %v", err)
			}
			stored, err := st.DriverRuns().Get(ctx, "TEST", run.RunID)
			if err != nil {
				t.Fatalf("get stored run: %v", err)
			}
			if stored.EpicID != tc.wantEpicID {
				t.Fatalf("stored run EpicID = %q, want %q", stored.EpicID, tc.wantEpicID)
			}

			if tc.leadEpicID != "" {
				bindWorkflowEpicLead(t, ctx, st, "epic-lead-1", tc.leadEpicID)
			}
			driveTerminalTaskRun(t, ctx, st, stored.RunID)

			rows := listWorkflowOutboxRows(t, ctx, st)
			if len(rows) != tc.wantOutbox {
				t.Fatalf("outbox rows = %d (%+v), want %d", len(rows), rows, tc.wantOutbox)
			}
			if tc.wantOutbox == 0 {
				return
			}
			row := rows[0]
			if row.Kind != domain.OutboxKindLeadTaskMessage || row.TargetAgent != "epic-lead-1" || row.EpicID != tc.wantEpicID {
				t.Fatalf("outbox row = %+v, want leadTaskMessage targeting epic-lead-1 under %q", row, tc.wantEpicID)
			}
		})
	}
}

// driveTerminalTaskRun creates a terminal fixture and then invokes the real
// Execution convergence command. Store writes stay test-only; the production
// projection and lead-notification policy remains under Execution.
func driveTerminalTaskRun(t *testing.T, ctx context.Context, st store.Store, driverRunID string) {
	t.Helper()
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "TEST",
		NodeID:          "wf-node-1",
		RuntimeProvider: domain.RuntimeProviderLocal,
		Capabilities:    []string{"driver-runner", "task-runner", "local-noop"},
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Minute,
	}); err != nil {
		t.Fatalf("create worker node: %v", err)
	}
	taskRunID := "wf-task-run-1"
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    "TEST",
		TaskRunID:       taskRunID,
		DriverRunID:     driverRunID,
		TaskID:          "WF-TASK-1",
		ProviderProfile: "local-noop",
		Status:          domain.TaskRunQueued,
	}); err != nil {
		t.Fatalf("create queued task run: %v", err)
	}
	const leaseID = "wf-task-run-lease-1"
	const leaseToken = "wf-task-run-token-1"
	claimed, err := st.TaskRuns().ClaimQueued(ctx, "TEST", store.TaskRunClaim{
		TaskRunID: taskRunID, NodeID: "wf-node-1", LeaseID: leaseID, LeaseToken: leaseToken,
		SupportedProviders: []string{"local-noop"}, ClaimedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("claim queued task run fixture: %v", err)
	}
	finishedAt := time.Now().UTC()
	if _, err := st.TaskRuns().Finish(ctx, "TEST", taskRunID, store.TaskRunFinish{
		NodeID: claimed.NodeID, LeaseID: claimed.LeaseID, LeaseToken: leaseToken,
		FencingToken: claimed.FencingToken, Status: domain.TaskRunCompleted, FinishedAt: finishedAt,
	}); err != nil {
		t.Fatalf("finish task run fixture: %v", err)
	}

	ports := &workflowTaskRunConvergencePorts{store: st}
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(execution.OperationRules()...)
	if err != nil {
		t.Fatalf("create Execution admission: %v", err)
	}
	service, err := execution.New(execution.Dependencies{Convergence: execution.TaskRunConvergenceDependencies{
		Source: ports, Checkpoints: ports, Events: ports, LeadResolver: ports, Notifications: ports,
	}}, admission)
	if err != nil {
		t.Fatalf("create Execution service: %v", err)
	}
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "workflow-test-convergence", Class: authority.ClassSystem, Workspace: "TEST",
		Actions: []authority.Action{execution.ActionConvergeTaskRun}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("derive convergence principal: %v", err)
	}
	auth, err := issuer.IssueSystem(principal, "TEST", execution.ActionConvergeTaskRun, "workflow handler convergence test")
	if err != nil {
		t.Fatalf("issue convergence authority: %v", err)
	}
	if _, err := service.ConvergeTaskRun(ctx, auth, execution.ConvergeTaskRunCommand{
		WorkspaceKey: "TEST", RequestID: "workflow-test-converge-" + taskRunID,
		TaskRunID: taskRunID, ObservedAt: finishedAt,
	}); err != nil {
		t.Fatalf("converge terminal task run: %v", err)
	}
}

type workflowTaskRunConvergencePorts struct {
	store store.Store
}

func (ports *workflowTaskRunConvergencePorts) GetTerminalTaskRun(ctx context.Context, workspace, taskRunID string) (*execution.TerminalTaskRunRecord, error) {
	run, err := ports.store.TaskRuns().Get(ctx, workspace, taskRunID)
	if err != nil {
		return nil, err
	}
	if run.FinishedAt == nil {
		return nil, execution.ErrConflict
	}
	status := execution.StatusFailed
	switch run.Status {
	case domain.TaskRunCompleted:
		status = execution.StatusSucceeded
	case domain.TaskRunCancelled:
		status = execution.StatusCancelled
	}
	record := &execution.TerminalTaskRunRecord{
		WorkspaceKey: run.WorkspaceKey, TaskRunID: run.TaskRunID, DriverRunID: run.DriverRunID,
		DriverStepID: run.DriverStepID, WorkItemID: run.TaskID, Status: status,
		ErrorClass: run.ErrorClass, ErrorMessage: run.ErrorMessage, LogsRef: run.LogsRef,
		ArtifactsRef: run.ArtifactsRef, FinishedAt: *run.FinishedAt,
	}
	if run.DriverRunID == "" {
		return record, nil
	}
	parent, err := ports.store.DriverRuns().Get(ctx, workspace, run.DriverRunID)
	if err != nil {
		return nil, err
	}
	record.EpicID = parent.EpicID
	return record, nil
}

func (*workflowTaskRunConvergencePorts) ListTaskRunConvergenceCandidates(context.Context, execution.TaskRunConvergenceCandidateQuery) (execution.TaskRunConvergenceCandidatePage, error) {
	return execution.TaskRunConvergenceCandidatePage{}, nil
}

func (ports *workflowTaskRunConvergencePorts) CompleteTaskRunTerminalConvergence(
	ctx context.Context,
	command execution.CompleteTaskRunTerminalConvergence,
) (execution.TaskRunTerminalConvergenceCheckpoint, error) {
	checkpoints, ok := ports.store.TaskRuns().(store.TaskRunTerminalConvergenceStore)
	if !ok {
		return execution.TaskRunTerminalConvergenceCheckpoint{}, execution.ErrUnavailable
	}
	result, err := checkpoints.CompleteTaskRunTerminalConvergence(ctx, store.TaskRunTerminalConvergenceComplete{
		WorkspaceKey: command.WorkspaceKey, TaskRunID: command.TaskRunID,
		RequiredVersion: command.RequiredVersion, CompletedAt: command.CompletedAt,
	})
	if err != nil {
		return execution.TaskRunTerminalConvergenceCheckpoint{}, err
	}
	if result == nil || result.TaskRun == nil || result.TaskRun.TerminalConvergedAt == nil {
		return execution.TaskRunTerminalConvergenceCheckpoint{}, execution.ErrConflict
	}
	return execution.TaskRunTerminalConvergenceCheckpoint{
		WorkspaceKey: result.TaskRun.WorkspaceKey,
		TaskRunID:    result.TaskRun.TaskRunID,
		Version:      result.TaskRun.TerminalConvergenceVersion,
		CompletedAt:  *result.TaskRun.TerminalConvergedAt,
		Replayed:     result.Replayed,
	}, nil
}

func (ports *workflowTaskRunConvergencePorts) EnsureTaskRunTerminalEvent(ctx context.Context, event execution.TaskRunTerminalEvent) error {
	eventType := domain.TaskRunEventFailed
	status := domain.TaskRunFailed
	switch event.Type {
	case execution.TaskRunTerminalCompleted:
		eventType = domain.TaskRunEventCompleted
		status = domain.TaskRunCompleted
	case execution.TaskRunTerminalCancelled:
		eventType = domain.TaskRunEventCancelled
		status = domain.TaskRunCancelled
	}
	_, err := ports.store.TaskRunEvents().Append(ctx, store.TaskRunEventAppend{
		WorkspaceKey: event.WorkspaceKey, EventID: event.EventID, EpicID: event.EpicID,
		DriverRunID: event.DriverRunID, TaskID: event.WorkItemID, TaskRunID: event.TaskRunID,
		Type: eventType, Status: status, SchedulerState: event.SchedulerState, Attempt: event.Attempt,
		ErrorClass: event.ErrorClass, ErrorMessage: event.ErrorMessage, LogsRef: event.LogsRef,
		ArtifactsRef: event.ArtifactsRef, OccurredAt: event.OccurredAt,
	})
	return err
}

func (ports *workflowTaskRunConvergencePorts) ResolveEpicLead(ctx context.Context, workspace, epicID string) (string, error) {
	agents, err := ports.store.AgentServices().List(ctx, workspace, store.AgentServiceFilter{})
	if err != nil {
		return "", err
	}
	for _, agent := range agents {
		if agent == nil || (agent.RoleName != "lead" && agent.RoleName != "orchestrator") || agent.ProfileName == "" {
			continue
		}
		profile, profileErr := ports.store.WorkerProfiles().Get(ctx, workspace, agent.ProfileName)
		if profileErr != nil {
			return "", profileErr
		}
		if profile.ParentEpic == epicID {
			return agent.ServiceID, nil
		}
	}
	return "", nil
}

func (ports *workflowTaskRunConvergencePorts) EnsureLeadTaskNotification(ctx context.Context, notification execution.LeadTaskNotification) error {
	_, err := ports.store.Outbox().Create(ctx, store.OutboxCreate{
		WorkspaceKey: notification.WorkspaceKey, Kind: domain.OutboxKindLeadTaskMessage,
		EpicID: notification.EpicID, DriverRunID: notification.DriverRunID, TaskRunID: notification.TaskRunID,
		TargetAgent: notification.TargetAgent, Body: "terminal task run", DedupeKey: notification.DedupeKey,
	})
	return err
}

func bindWorkflowEpicLead(t *testing.T, ctx context.Context, st store.Store, name, epicID string) {
	t.Helper()
	profileID := name + "-profile"
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST", Name: "lead",
	}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("create lead role: %v", err)
	}
	if _, err := st.WorkerProfiles().Create(ctx, store.WorkerProfileCreate{
		WorkspaceKey: "TEST", ProfileID: profileID, Name: profileID, Role: "lead", ParentEpic: epicID,
	}); err != nil {
		t.Fatalf("create lead profile: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "TEST", ServiceID: name, Name: name, RoleName: "lead", ProfileName: profileID,
		Kind: domain.AgentServiceKindLead, DesiredState: domain.AgentServiceDesiredRunning, MaxInstances: 1,
	}); err != nil {
		t.Fatalf("create lead agent service: %v", err)
	}
}

func listWorkflowOutboxRows(t *testing.T, ctx context.Context, st store.Store) []*domain.OutboxRecord {
	t.Helper()
	rows, err := st.Outbox().ListDue(ctx, "TEST", store.OutboxDueFilter{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("list due outbox: %v", err)
	}
	return rows
}

func TestGetRunEventsReturnsDriverRunEvents(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	run, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "TEST",
		RunID:           "run-1",
		DriverID:        "demo",
		DriverVersionID: "version-1",
		Payload:         json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST/runs/"+run.RunID+"/events", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var page domain.PlatformEventsPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode events page: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].EntityID != "run-1" || page.Events[0].EntityType != "driver_run" {
		t.Fatalf("events page = %+v, want one driver_run event", page)
	}
}

func TestGetRunEmbedsDriverSteps(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	run, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "TEST",
		RunID:           "run-with-steps",
		DriverID:        "demo",
		DriverVersionID: "version-1",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := st.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "TEST",
		StepID:       "step-1",
		DriverRunID:  run.RunID,
		StepKind:     "exec_task",
		Status:       domain.DriverStepRunning,
		TaskRunID:    "task-run-1",
	}); err != nil {
		t.Fatalf("create driver step: %v", err)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "TEST",
		TaskRunID:    "task-run-1",
		DriverRunID:  run.RunID,
		DriverStepID: "step-1",
		TaskID:       "TASK-1",
		Status:       domain.TaskRunRunning,
	}); err != nil {
		t.Fatalf("create task run: %v", err)
	}

	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST/runs/"+run.RunID, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var out struct {
		RunID string `json:"run_id"`
		Steps []struct {
			ID        string `json:"id"`
			StepKind  string `json:"step_kind"`
			TaskRunID string `json:"task_run_id"`
			TaskID    string `json:"task_id"`
			Status    string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode run detail: %v", err)
	}
	if out.RunID != run.RunID || len(out.Steps) != 1 {
		t.Fatalf("run detail = %+v, want run with one step", out)
	}
	step := out.Steps[0]
	if step.ID != "step-1" || step.StepKind != "exec_task" || step.TaskRunID != "task-run-1" || step.TaskID != "TASK-1" || step.Status != "running" {
		t.Fatalf("step summary = %+v, want step/task-run linkage", step)
	}
}

func TestCreateWorkflowVersionRejectsPackageManifest(t *testing.T) {
	st := memstore.New()
	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	body := `{"files":{"package.json":"{}","workflows/demo.ts":"export async function run(){ return {}; }"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions", stringsReader(body))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestCreateWorkflowVersionRejectsInlineActivation(t *testing.T) {
	st := memstore.New()
	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	body := `{"files":{"workflows/demo.ts":"export async function run(){ return {}; }"},"activate":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions", stringsReader(body))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "approve and activate the version through the workflow catalog lifecycle API") {
		t.Fatalf("body = %s, want lifecycle API guidance", rec.Body.String())
	}
	if _, err := st.Drivers().Get(context.Background(), "TEST", "demo"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("activation bypass registered driver: %v", err)
	}
}

func TestCreateWorkflowVersionRegistersWithoutActivation(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())
	catalog := &workflowRunCatalogStub{getDriverErr: workflowcatalog.ErrNotFound}
	authoring := &workflowVersionAuthoringStub{}
	mux := http.NewServeMux()
	NewModule(Config{
		Store: st, Catalog: catalog, Authoring: authoring,
		CatalogOperatorAuthority: workflowOperatorAuthorityStub{},
	}).Register(mux)

	body := `{"files":{"workflows/demo.ts":"export async function run(){ return {}; }"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ALIAS/workflows/demo/versions", stringsReader(body))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST"))
	req.Header.Set("Idempotency-Key", "workflow-build-request-1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var response struct {
		Activated bool                           `json:"activated"`
		Driver    *workflowcatalog.Driver        `json:"driver"`
		Version   *workflowcatalog.DriverVersion `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Activated || response.Driver == nil || response.Version == nil {
		t.Fatalf("response = %+v, want registered inactive version", response)
	}
	if authoring.operatorCalls != 1 || authoring.managedCalls != 0 {
		t.Fatalf("authoring calls = operator:%d managed:%d", authoring.operatorCalls, authoring.managedCalls)
	}
	command := authoring.operatorCommand
	if command.WorkspaceKey != "TEST" || command.DriverID != "demo" ||
		command.RequestID != "workflow-build-request-1" || command.ExpectedRevision != 0 {
		t.Fatalf("author command = %+v, want canonical workspace and caller replay key", command)
	}
	if catalog.getDriverWorkspace != "TEST" || catalog.getDriverRef != "demo" {
		t.Fatalf("catalog lookup = %q/%q, want TEST/demo", catalog.getDriverWorkspace, catalog.getDriverRef)
	}
	if _, err := st.Drivers().Get(ctx, "TEST", "demo"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("HTTP adapter wrote generic Driver store: %v", err)
	}
}

func seededWorkflowStore(t *testing.T, ctx context.Context) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     "demo",
		Name:         "demo",
		OwnerType:    workflowcatalog.DriverOwnerUser,
		Status:       workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "version-1",
		DriverID:         "demo",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	if _, err := st.ApproveDriverVersionForTest(ctx, "TEST", "demo", "version-1"); err != nil {
		t.Fatalf("approve driver version: %v", err)
	}
	if _, err := st.ActivateDriverVersionForTest(ctx, "TEST", "demo", "version-1"); err != nil {
		t.Fatalf("activate driver version: %v", err)
	}
	return st
}

func stringsReader(s string) *strings.Reader {
	return strings.NewReader(s)
}

func installFakeFlueBuild(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-flue")
	body := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then
    shift
    out="$1"
  fi
  shift
done
if [ "$out" = "" ]; then
  echo "missing --output" >&2
  exit 1
fi
mkdir -p "$out"
cat > "$out/server.mjs" <<'EOF'
export async function run() { return {}; }
EOF
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake flue: %v", err)
	}
	sdkRoot := filepath.Join(dir, "sdk")
	if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
		t.Fatalf("create fake sdk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sdkRoot, "package.json"), []byte(`{"name":"@loom/sdk"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fake sdk package: %v", err)
	}
	runtimeRoot := filepath.Join(dir, "runtime")
	for _, dep := range []string{
		runtimeRoot,
		filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"),
		filepath.Join(runtimeRoot, "node_modules", "hono"),
		filepath.Join(runtimeRoot, "node_modules", "valibot"),
	} {
		if err := os.MkdirAll(dep, 0o755); err != nil {
			t.Fatalf("create fake runtime dependency %s: %v", dep, err)
		}
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "package.json"), []byte(`{"name":"@flue/runtime"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fake runtime package: %v", err)
	}
	t.Setenv("LOOM_REAL_FLUE_CMD", script)
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
}
