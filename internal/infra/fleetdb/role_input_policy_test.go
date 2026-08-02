package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func newRoleTestClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	ts := httptest.NewServer(handler)
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		ts.Close()
		t.Fatalf("New: %v", err)
	}
	return client, ts.Close
}

// Create sends input_policy in fleet-db's shape, and the response decodes back
// into a domain policy that still resolves the same way. Both directions in one
// test because a wire type that only works one way is the failure mode.
func TestRoleStoreCreate_InputPolicyRoundTripsOnTheWire(t *testing.T) {
	var sent map[string]any
	client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(roleWire{
			WorkspaceKey: "WS",
			Name:         "task",
			InputPolicy: &domain.RoleInputPolicy{
				Default: domain.RoleInputDeny,
				Kinds:   map[string]string{"trust_prompt": domain.RoleInputAllow},
			},
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		})
	})
	defer closeFn()

	got, err := client.Roles().Create(t.Context(), store.RoleCreate{
		WorkspaceKey: "WS",
		Name:         "task",
		InputPolicy: &domain.RoleInputPolicy{
			Default: domain.RoleInputDeny,
			Kinds:   map[string]string{"trust_prompt": domain.RoleInputAllow},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	policy, ok := sent["input_policy"].(map[string]any)
	if !ok {
		t.Fatalf("create body input_policy = %#v, want an object", sent["input_policy"])
	}
	if policy["default"] != domain.RoleInputDeny {
		t.Errorf("wire default = %#v, want %q", policy["default"], domain.RoleInputDeny)
	}
	kinds, ok := policy["kinds"].(map[string]any)
	if !ok || kinds["trust_prompt"] != domain.RoleInputAllow {
		t.Errorf("wire kinds = %#v, want trust_prompt=allow", policy["kinds"])
	}

	if got.InputPolicy.DispositionFor("trust_prompt") != domain.RoleInputAllow {
		t.Errorf("decoded policy lost the allow: %+v", got.InputPolicy)
	}
	if got.InputPolicy.DispositionFor("anything_else") != domain.RoleInputDeny {
		t.Errorf("decoded policy is permissive for an unnamed kind: %+v", got.InputPolicy)
	}
}

// A role the server returns WITHOUT input_policy must decode to nil, which
// resolves to deny — not to an empty-but-present policy and never to allow.
func TestRoleStoreGet_AbsentInputPolicyDecodesToDeny(t *testing.T) {
	client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(roleWire{WorkspaceKey: "WS", Name: "task"})
	})
	defer closeFn()

	got, err := client.Roles().Get(t.Context(), "WS", "task")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.InputPolicy != nil {
		t.Fatalf("InputPolicy = %+v, want nil", got.InputPolicy)
	}
	if d := got.InputPolicy.DispositionFor("trust_prompt"); d != domain.RoleInputDeny {
		t.Fatalf("absent policy resolved to %q, want %q", d, domain.RoleInputDeny)
	}
}

// The patch contract: setting sends the object, clearing sends the explicit
// clear flag. Without the flag a &nil patch would serialize to nothing (the
// field is omitempty) and the server would read it as "leave it alone",
// silently keeping a policy the operator asked to remove.
func TestRoleStoreUpdate_InputPolicySetAndClear(t *testing.T) {
	tests := []struct {
		name       string
		patch      store.RoleUpdate
		wantPolicy bool
		wantClear  bool
	}{
		{
			name: "set",
			patch: store.RoleUpdate{InputPolicy: ptrTo(&domain.RoleInputPolicy{
				Kinds: map[string]string{"trust_prompt": domain.RoleInputAllow},
			})},
			wantPolicy: true,
		},
		{
			name:      "clear",
			patch:     store.RoleUpdate{InputPolicy: ptrTo[*domain.RoleInputPolicy](nil)},
			wantClear: true,
		},
		{name: "untouched", patch: store.RoleUpdate{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent map[string]any
			client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				_ = json.NewEncoder(w).Encode(roleWire{WorkspaceKey: "WS", Name: "task"})
			})
			defer closeFn()

			if _, err := client.Roles().Update(t.Context(), "WS", "task", tt.patch); err != nil {
				t.Fatalf("Update: %v", err)
			}
			_, hasPolicy := sent["input_policy"]
			if hasPolicy != tt.wantPolicy {
				t.Errorf("input_policy present = %v, want %v (body %#v)", hasPolicy, tt.wantPolicy, sent)
			}
			clear, _ := sent["clear_input_policy"].(bool)
			if clear != tt.wantClear {
				t.Errorf("clear_input_policy = %v, want %v (body %#v)", clear, tt.wantClear, sent)
			}
		})
	}
}

func ptrTo[T any](v T) *T { return &v }
