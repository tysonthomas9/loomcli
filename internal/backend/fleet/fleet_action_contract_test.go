package fleet

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime" //nolint:depguard // Contract test intentionally exercises both SSE projections.
)

func TestFleetActionContractParity(t *testing.T) {
	if len(fleetActionContract) == 0 {
		t.Fatal("fleetActionContract is empty; regenerate it from FleetDB")
	}
	seen := make(map[string]struct{}, len(fleetActionContract))
	previous := ""
	for _, row := range fleetActionContract {
		if _, duplicate := seen[row.Action]; duplicate {
			t.Fatalf("fleetActionContract contains duplicate action %q", row.Action)
		}
		if previous != "" && row.Action < previous {
			t.Fatalf("fleetActionContract is not sorted: %q appears after %q", row.Action, previous)
		}
		seen[row.Action] = struct{}{}
		previous = row.Action
		t.Run(row.Action, func(t *testing.T) { assertFleetActionContractRow(t, row) })
	}
}

func assertFleetActionContractRow(t *testing.T, row fleetActionContractRow) {
	t.Helper()
	if !knownFleetCoarseType(row.ExpectedCoarseType) {
		t.Fatalf("FleetDB action %q has unknown or empty expected coarse type %q; update actionToMutationType and regenerate the contract",
			row.Action, row.ExpectedCoarseType)
	}
	event := &fleetMutationEvent{
		ID: "contract-0", Timestamp: time.Unix(1, 0).UTC(), Action: row.Action,
		EntityType: row.EntityType, EntityID: "entity-1",
	}
	mutation := fleetEventToMutationData(event)
	if !knownFleetCoarseType(mutation.Type) {
		t.Fatalf("FleetDB action %q maps to unknown or empty coarse type %q; update actionToMutationType",
			row.Action, mutation.Type)
	}
	if mutation.Type != row.ExpectedCoarseType {
		t.Fatalf("FleetDB action %q maps to coarse type %q, want %q; update actionToMutationType or regenerate the contract",
			row.Action, mutation.Type, row.ExpectedCoarseType)
	}
	assertFleetMutationProjection(t, "fleetEventToMutationData", mutation.EntityType, mutation.Action, mutation.Type, row)
	live := realtime.BackendMutationToPayload(mutation, "contract-workspace")
	assertFleetMutationProjection(t, "live SSE", live.EntityType, live.Action, live.Type, row)
	catchUp := realtime.RPCMutationToPayload(realtime.BackendMutationToRPCEvent(mutation))
	assertFleetMutationProjection(t, "catch-up SSE", catchUp.EntityType, catchUp.Action, catchUp.Type, row)
}

func assertFleetMutationProjection(t *testing.T, projection, entityType, action, coarseType string, row fleetActionContractRow) {
	t.Helper()
	if entityType != row.EntityType || action != row.Action || coarseType != row.ExpectedCoarseType {
		t.Errorf("%s projection for %q = entity_type %q, action %q, coarse type %q; want %q, %q, %q",
			projection, row.Action, entityType, action, coarseType,
			row.EntityType, row.Action, row.ExpectedCoarseType)
	}
}

func knownFleetCoarseType(value string) bool {
	switch value {
	case backend.MutationCreate, backend.MutationUpdate, backend.MutationDelete,
		backend.MutationComment, backend.MutationStatus, backend.MutationRefresh:
		return true
	default:
		return false
	}
}
