package fleetdb

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store"
)

func intPtr(v int) *int { return &v }

// The run cap has THREE states and the wire has to carry all three, because
// zero is not "unset" here — it disables the cap. A wire that collapses zero
// into absent turns "never kill this agent for running long" into "use the
// daemon default", which is the opposite instruction.
func TestRoleStoreCreate_MaxRunDurationCarriesZero(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    *int
		wantKey  bool
		wantJSON float64
	}{
		{name: "unset sends nothing", value: nil, wantKey: false},
		{name: "zero is sent, not omitted", value: intPtr(0), wantKey: true, wantJSON: 0},
		{name: "a real cap is sent", value: intPtr(14400), wantKey: true, wantJSON: 14400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&sent)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"workspace_key":"ws","name":"planner"}`))
			})
			defer closeFn()

			if _, err := client.Roles().Create(t.Context(), store.RoleCreate{
				WorkspaceKey: "ws", Name: "planner", MaxRunDuration: tc.value,
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}

			got, ok := sent["max_run_duration"]
			if ok != tc.wantKey {
				t.Fatalf("max_run_duration present = %v, want %v (body %v)", ok, tc.wantKey, sent)
			}
			if tc.wantKey && got != tc.wantJSON {
				t.Fatalf("max_run_duration = %v, want %v", got, tc.wantJSON)
			}
		})
	}
}

// Clearing the cap and disabling it are different instructions, and `omitempty`
// on a pointer cannot express the difference on its own — a patch holding a nil
// would serialize to nothing and read on the server as "leave it alone". The
// explicit clear flag is what separates them.
func TestRoleStoreUpdate_MaxRunDurationSetDisableAndClear(t *testing.T) {
	nilCap := (*int)(nil)
	disabled := intPtr(0)
	real := intPtr(3600)

	for _, tc := range []struct {
		name      string
		patch     **int
		wantValue any
		wantClear bool
	}{
		{name: "set a cap", patch: &real, wantValue: float64(3600)},
		{name: "disable it explicitly", patch: &disabled, wantValue: float64(0)},
		{name: "clear it back to the default", patch: &nilCap, wantClear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&sent)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"workspace_key":"ws","name":"planner"}`))
			})
			defer closeFn()

			if _, err := client.Roles().Update(t.Context(), "ws", "planner", store.RoleUpdate{
				MaxRunDuration: tc.patch,
			}); err != nil {
				t.Fatalf("Update: %v", err)
			}

			if tc.wantClear {
				if sent["clear_max_run_duration"] != true {
					t.Fatalf("want clear_max_run_duration, got body %v", sent)
				}
				if _, ok := sent["max_run_duration"]; ok {
					t.Fatalf("a clear must not also send a value: %v", sent)
				}
				return
			}
			if got := sent["max_run_duration"]; got != tc.wantValue {
				t.Fatalf("max_run_duration = %v, want %v (body %v)", got, tc.wantValue, sent)
			}
			if _, ok := sent["clear_max_run_duration"]; ok {
				t.Fatalf("a value must not also send the clear flag: %v", sent)
			}
		})
	}
}

// The response direction: a cap set server-side has to survive back into the
// domain role, including a zero.
func TestRoleStoreGet_MaxRunDurationDecodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want *int
	}{
		{name: "absent stays nil", body: `{"workspace_key":"ws","name":"p"}`, want: nil},
		{name: "zero decodes as zero", body: `{"workspace_key":"ws","name":"p","max_run_duration":0}`, want: intPtr(0)},
		{name: "a cap decodes", body: `{"workspace_key":"ws","name":"p","max_run_duration":900}`, want: intPtr(900)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, closeFn := newRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			})
			defer closeFn()

			role, err := client.Roles().Get(t.Context(), "ws", "p")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			switch {
			case tc.want == nil && role.MaxRunDuration != nil:
				t.Fatalf("MaxRunDuration = %v, want nil", *role.MaxRunDuration)
			case tc.want != nil && role.MaxRunDuration == nil:
				t.Fatalf("MaxRunDuration = nil, want %v", *tc.want)
			case tc.want != nil && *role.MaxRunDuration != *tc.want:
				t.Fatalf("MaxRunDuration = %v, want %v", *role.MaxRunDuration, *tc.want)
			}
		})
	}
}
