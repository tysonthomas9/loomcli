package automation_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

// WithDerivedRoute is the single canonical rule (applied by every store Create):
// a cron OR internal binding's route_key is derived from its unique binding_id,
// so scheduled bindings (and pattern-matched internal-event siblings) never
// collide on a shared hand-picked route. External event sources are never derived
// (they carry a meaningful external route).
func TestTriggerBindingCreate_WithDerivedRoute(t *testing.T) {
	cases := []struct {
		name string
		in   automation.TriggerBindingCreate
		want string
	}{
		{
			name: "cron derives route from binding_id",
			in:   automation.TriggerBindingCreate{SourceKind: automation.CronSourceKind, BindingID: "s1-bug-fix"},
			want: "cron:s1-bug-fix",
		},
		{
			name: "cron keeps an explicitly supplied route",
			in:   automation.TriggerBindingCreate{SourceKind: automation.CronSourceKind, BindingID: "s1", RouteKey: "custom"},
			want: "custom",
		},
		{
			name: "internal derives route from binding_id",
			in:   automation.TriggerBindingCreate{SourceKind: automation.InternalSourceKind, BindingID: "ts-planner"},
			want: "internal:ts-planner",
		},
		{
			name: "internal keeps an explicitly supplied route (exact-owner opt-in)",
			in:   automation.TriggerBindingCreate{SourceKind: automation.InternalSourceKind, BindingID: "ts-p", RouteKey: "internal.task.ready"},
			want: "internal.task.ready",
		},
		{
			name: "event source is never derived",
			in:   automation.TriggerBindingCreate{SourceKind: "github", BindingID: "b1"},
			want: "",
		},
		{
			name: "cron without a binding_id is left empty (rejected downstream)",
			in:   automation.TriggerBindingCreate{SourceKind: automation.CronSourceKind},
			want: "",
		},
		{
			name: "internal without a binding_id is left empty (rejected downstream)",
			in:   automation.TriggerBindingCreate{SourceKind: automation.InternalSourceKind},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.WithDerivedRoute().RouteKey; got != tc.want {
				t.Fatalf("RouteKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// DefaultBindingID is the inverse default of WithDerivedRoute: when a caller
// supplies a route_key but no binding_id, the id is derived from the route.
// The derivation must stay deterministic — createBinding's idempotent fast
// path Gets by the derived id, so the same route key has to map to the same
// id on every call and across every create surface (CLI, webui).
func TestDefaultBindingID(t *testing.T) {
	cases := []struct {
		name     string
		routeKey string
		want     string
	}{
		{
			name:     "github event route dots become dashes",
			routeKey: "github.pull_request.opened",
			want:     "binding-github-pull_request-opened",
		},
		{
			name:     "route without dots is prefixed unchanged",
			routeKey: "custom-route",
			want:     "binding-custom-route",
		},
		{
			// Non-empty enforcement is the caller's job (route_key is
			// validated required before derivation); pin the passthrough.
			name:     "empty route yields bare prefix",
			routeKey: "",
			want:     "binding-",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := automation.DefaultBindingID(tc.routeKey)
			if got != tc.want {
				t.Fatalf("DefaultBindingID(%q) = %q, want %q", tc.routeKey, got, tc.want)
			}
			if again := automation.DefaultBindingID(tc.routeKey); again != got {
				t.Fatalf("DefaultBindingID(%q) is not deterministic: %q then %q", tc.routeKey, got, again)
			}
		})
	}
}
