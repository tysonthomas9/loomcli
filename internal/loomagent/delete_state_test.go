package loomagent

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestDeletedAgentReadsDeleted (DEL1): a tombstoned agent keeps its stored
// state (stopping, the state Delete stopped it in), but Get and List show
// it as deleted, with its deleted_at, and never as stopping.
func TestDeletedAgentReadsDeleted(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, svcAgent("a1", "persistent", StateIdle))
	if err := s.Delete(ctx, DeleteRequest{AgentID: "a1"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "a1")
	if err != nil || got.State != "deleted" || got.DeletedAt == nil {
		t.Fatalf("Get = state %q deleted_at %v, %v; want deleted with its deleted_at", got.State, got.DeletedAt, err)
	}
	all, _, err := s.List(ctx, loomstore.AgentFilter{IncludeDeleted: true})
	if err != nil || len(all) != 1 || all[0].State != "deleted" {
		t.Fatalf("List = %+v, %v; want a1 as deleted", all, err)
	}
	if row, _ := s.store.GetAgent(ctx, "a1"); row.State != StateStopping {
		t.Fatalf("stored state %q; want it unchanged (stopping)", row.State)
	}
}
