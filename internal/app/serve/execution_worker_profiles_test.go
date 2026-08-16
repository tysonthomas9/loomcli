package serve

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

var errWorkerProfileResponseLost = errors.New("worker profile response lost after commit")

type lostResponseWorkerProfileStore struct {
	execution.WorkerProfileStore
	loseCreate bool
	loseUpdate bool
	loseDelete bool
}

func (wrapped *lostResponseWorkerProfileStore) Create(ctx context.Context, input execution.WorkerProfileCreate) (*execution.WorkerProfile, error) {
	profile, err := wrapped.WorkerProfileStore.Create(ctx, input)
	if err == nil && wrapped.loseCreate {
		wrapped.loseCreate = false
		return nil, errWorkerProfileResponseLost
	}
	return profile, err
}

func (wrapped *lostResponseWorkerProfileStore) Update(ctx context.Context, workspace, profileID string, patch execution.WorkerProfileUpdate) (*execution.WorkerProfile, error) {
	profile, err := wrapped.WorkerProfileStore.Update(ctx, workspace, profileID, patch)
	if err == nil && wrapped.loseUpdate {
		wrapped.loseUpdate = false
		return nil, errWorkerProfileResponseLost
	}
	return profile, err
}

func (wrapped *lostResponseWorkerProfileStore) Delete(ctx context.Context, workspace, profileID string) error {
	err := wrapped.WorkerProfileStore.Delete(ctx, workspace, profileID)
	if err == nil && wrapped.loseDelete {
		wrapped.loseDelete = false
		return errWorkerProfileResponseLost
	}
	return err
}

func TestExecutionWorkerProfileCreateRetryConvergesAndRejectsDrift(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	adapter := &executionTaskRunPortsAdapter{dependencies: ExecutionTaskRunPortDependencies{WorkerProfiles: st.WorkerProfiles()}}
	enabled := false
	command := execution.CreateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "create-profile-1", ProfileID: "builder", Role: "task",
		Backend: "codex", Repos: []string{"acme/widgets"}, Enabled: &enabled,
		Metadata: map[string]string{"lane": "phase4"},
	}
	first, err := adapter.CreateWorkerProfile(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	retry := command
	retry.RequestID = "create-profile-retry"
	second, err := adapter.CreateWorkerProfile(ctx, retry)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProfileID != second.ProfileID || !first.CreatedAt.Equal(second.CreatedAt) || second.Enabled {
		t.Fatalf("create retry did not return committed profile: first=%+v second=%+v", first, second)
	}

	drifted := command
	drifted.Role = "plan"
	if _, err := adapter.CreateWorkerProfile(ctx, drifted); !errors.Is(err, execution.ErrConflict) {
		t.Fatalf("drifted create error = %v, want ErrConflict", err)
	}
}

func TestExecutionWorkerProfileQueriesProjectStoreRecords(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	adapter := &executionTaskRunPortsAdapter{dependencies: ExecutionTaskRunPortDependencies{WorkerProfiles: st.WorkerProfiles()}}
	if _, err := adapter.CreateWorkerProfile(ctx, execution.CreateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "create-profile", ProfileID: "lead-profile", Role: "lead", ParentEpic: "EPIC-1",
	}); err != nil {
		t.Fatal(err)
	}
	profile, err := adapter.GetWorkerProfile(ctx, "WS", "lead-profile")
	if err != nil || profile.ParentEpic != "EPIC-1" {
		t.Fatalf("profile/error = %+v/%v", profile, err)
	}
	profiles, err := adapter.ListWorkerProfiles(ctx, "WS", execution.WorkerProfileFilter{Role: "lead", Limit: 1})
	if err != nil || len(profiles) != 1 || profiles[0].ProfileID != "lead-profile" {
		t.Fatalf("profiles/error = %+v/%v", profiles, err)
	}
}

func TestExecutionWorkerProfileUpdateAndDeleteRetriesConverge(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	adapter := &executionTaskRunPortsAdapter{dependencies: ExecutionTaskRunPortDependencies{WorkerProfiles: st.WorkerProfiles()}}
	command := execution.CreateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "create-profile-1", ProfileID: "builder", Role: "task",
	}
	if _, err := adapter.CreateWorkerProfile(ctx, command); err != nil {
		t.Fatal(err)
	}
	role := "plan"
	update := execution.UpdateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "update-profile-1", ProfileID: command.ProfileID,
		Patch: execution.WorkerProfilePatch{Role: &role},
	}
	for _, requestID := range []string{"update-profile-1", "update-profile-2"} {
		update.RequestID = requestID
		profile, err := adapter.UpdateWorkerProfile(ctx, update)
		if err != nil {
			t.Fatal(err)
		}
		if profile.Role != role {
			t.Fatalf("updated role = %q, want %q", profile.Role, role)
		}
	}
	deleteCommand := execution.DeleteWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "delete-profile-1", ProfileID: command.ProfileID,
	}
	for _, requestID := range []string{"delete-profile-1", "delete-profile-2"} {
		deleteCommand.RequestID = requestID
		if err := adapter.DeleteWorkerProfile(ctx, deleteCommand); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExecutionWorkerProfileRetriesConvergeAfterCommittedResponseLoss(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	profiles := &lostResponseWorkerProfileStore{
		WorkerProfileStore: st.WorkerProfiles(), loseCreate: true, loseUpdate: true, loseDelete: true,
	}
	adapter := &executionTaskRunPortsAdapter{dependencies: ExecutionTaskRunPortDependencies{WorkerProfiles: profiles}}
	create := execution.CreateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "create-lost", ProfileID: "builder", Role: "task",
		Backend: "codex", Metadata: map[string]string{"lane": "phase4"},
	}
	if _, err := adapter.CreateWorkerProfile(ctx, create); !errors.Is(err, errWorkerProfileResponseLost) {
		t.Fatalf("first create error = %v, want injected response loss", err)
	}
	create.RequestID = "create-retry"
	created, err := adapter.CreateWorkerProfile(ctx, create)
	if err != nil || created.ProfileID != create.ProfileID || created.Backend != create.Backend {
		t.Fatalf("create retry profile/error = %+v/%v", created, err)
	}

	role := "plan"
	update := execution.UpdateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "update-lost", ProfileID: create.ProfileID,
		Patch: execution.WorkerProfilePatch{Role: &role},
	}
	if _, err := adapter.UpdateWorkerProfile(ctx, update); !errors.Is(err, errWorkerProfileResponseLost) {
		t.Fatalf("first update error = %v, want injected response loss", err)
	}
	update.RequestID = "update-retry"
	updated, err := adapter.UpdateWorkerProfile(ctx, update)
	if err != nil || updated.Role != role {
		t.Fatalf("update retry profile/error = %+v/%v", updated, err)
	}

	remove := execution.DeleteWorkerProfileCommand{WorkspaceKey: "WS", RequestID: "delete-lost", ProfileID: create.ProfileID}
	if err := adapter.DeleteWorkerProfile(ctx, remove); !errors.Is(err, errWorkerProfileResponseLost) {
		t.Fatalf("first delete error = %v, want injected response loss", err)
	}
	remove.RequestID = "delete-retry"
	if err := adapter.DeleteWorkerProfile(ctx, remove); err != nil {
		t.Fatalf("delete retry: %v", err)
	}
	if _, err := st.WorkerProfiles().Get(ctx, "WS", create.ProfileID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("deleted profile remained readable: %v", err)
	}
}
