package fleetdb

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// fleet-db already owns the label-gate semantics; loomcli only mirrors them, so
// the one thing that can go wrong on this hop is the key names. They are
// `labels` / `exclude_labels` (fleet-db/internal/models/role.go:200-211).
func TestRoleStoreUpdate_LabelGatePatchKeys(t *testing.T) {
	labels := []string{"needs-design", "area:api"}
	exclude := []string{"wip"}
	empty := []string{}

	for _, tc := range []struct {
		name        string
		patch       store.RoleUpdate
		wantLabels  any
		wantExclude any
	}{
		{
			name:  "nil leaves both alone",
			patch: store.RoleUpdate{},
		},
		{
			name:        "set both",
			patch:       store.RoleUpdate{Labels: &labels, ExcludeLabels: &exclude},
			wantLabels:  []any{"needs-design", "area:api"},
			wantExclude: []any{"wip"},
		},
		{
			name:        "clear both with an empty list",
			patch:       store.RoleUpdate{Labels: &empty, ExcludeLabels: &empty},
			wantLabels:  []any{},
			wantExclude: []any{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&sent)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"workspace_key":"ws","name":"architect"}`))
			})
			defer closeFn()

			if _, err := client.Roles().Update(t.Context(), "ws", "architect", tc.patch); err != nil {
				t.Fatalf("Update: %v", err)
			}

			for key, want := range map[string]any{"labels": tc.wantLabels, "exclude_labels": tc.wantExclude} {
				got, present := sent[key]
				if want == nil {
					if present {
						t.Fatalf("%s present in body %v, want absent", key, sent)
					}
					continue
				}
				if !present {
					t.Fatalf("%s absent from body %v", key, sent)
				}
				gotList, _ := got.([]any)
				wantList, _ := want.([]any)
				if len(gotList) != len(wantList) {
					t.Fatalf("%s = %v, want %v", key, got, want)
				}
				for i := range wantList {
					if gotList[i] != wantList[i] {
						t.Fatalf("%s = %v, want %v", key, got, want)
					}
				}
			}
		})
	}
}

// The response direction: a gate set server-side has to land on the domain role.
func TestRoleStoreGet_LabelGateDecodes(t *testing.T) {
	client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspace_key":"ws","name":"architect",` +
			`"labels":["needs-design","area:api"],"exclude_labels":["wip"]}`))
	})
	defer closeFn()

	role, err := client.Roles().Get(t.Context(), "ws", "architect")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(role.Labels) != 2 || role.Labels[0] != "needs-design" || role.Labels[1] != "area:api" {
		t.Errorf("Labels = %v, want [needs-design area:api]", role.Labels)
	}
	if len(role.ExcludeLabels) != 1 || role.ExcludeLabels[0] != "wip" {
		t.Errorf("ExcludeLabels = %v, want [wip]", role.ExcludeLabels)
	}
}
