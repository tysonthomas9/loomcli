package memstore

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store"
)

func gateEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The label gate is routing state, so it has to survive create, patch, and the
// defensive copies on the way in and out — a role whose Labels slice is shared
// with its caller can have its routing changed after the store accepted it.
func TestRoleStoreLabelGateCreatePatchAndClear(t *testing.T) {
	ctx := context.Background()
	s := New()

	labels := []string{"needs-design", "area:api"}
	exclude := []string{"wip"}
	role, err := s.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey:  "WS",
		Name:          "architect",
		Labels:        labels,
		ExcludeLabels: exclude,
	})
	if err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if !gateEqual(role.Labels, labels) || !gateEqual(role.ExcludeLabels, exclude) {
		t.Fatalf("created gate = (%v, %v), want (%v, %v)", role.Labels, role.ExcludeLabels, labels, exclude)
	}

	// Mutating the caller's slices must not reach into the store.
	labels[0] = "hijacked"
	got, err := s.Roles().Get(ctx, "WS", "architect")
	if err != nil {
		t.Fatalf("Get role: %v", err)
	}
	if got.Labels[0] != "needs-design" {
		t.Errorf("stored Labels[0] = %q, want needs-design (caller's slice must not alias the store)", got.Labels[0])
	}

	nextLabels := []string{"needs-plan"}
	nextExclude := []string{"blocked", "wip"}
	role, err = s.Roles().Update(ctx, "WS", "architect", store.RoleUpdate{
		Labels:        &nextLabels,
		ExcludeLabels: &nextExclude,
	})
	if err != nil {
		t.Fatalf("Update role gate: %v", err)
	}
	if !gateEqual(role.Labels, nextLabels) || !gateEqual(role.ExcludeLabels, nextExclude) {
		t.Fatalf("patched gate = (%v, %v), want (%v, %v)", role.Labels, role.ExcludeLabels, nextLabels, nextExclude)
	}

	// A nil patch field leaves the gate alone.
	role, err = s.Roles().Update(ctx, "WS", "architect", store.RoleUpdate{})
	if err != nil {
		t.Fatalf("Update role no-op: %v", err)
	}
	if !gateEqual(role.Labels, nextLabels) || !gateEqual(role.ExcludeLabels, nextExclude) {
		t.Fatalf("no-op patch changed the gate: (%v, %v)", role.Labels, role.ExcludeLabels)
	}

	// A pointer to an empty slice clears it, the same convention Skills uses.
	empty := []string{}
	role, err = s.Roles().Update(ctx, "WS", "architect", store.RoleUpdate{
		Labels:        &empty,
		ExcludeLabels: &empty,
	})
	if err != nil {
		t.Fatalf("Update role clear gate: %v", err)
	}
	if len(role.Labels) != 0 || len(role.ExcludeLabels) != 0 {
		t.Fatalf("cleared gate = (%v, %v), want both empty", role.Labels, role.ExcludeLabels)
	}
}
