package store_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// WithDerivedRoute is the single canonical rule (applied by every store Create):
// a cron binding's route_key is derived from its unique binding_id, so scheduled
// bindings never collide on a shared hand-picked route. Event sources are never
// derived (they carry a meaningful external route).
func TestTriggerBindingCreate_WithDerivedRoute(t *testing.T) {
	cases := []struct {
		name string
		in   store.TriggerBindingCreate
		want string
	}{
		{
			name: "cron derives route from binding_id",
			in:   store.TriggerBindingCreate{SourceKind: store.CronSourceKind, BindingID: "s1-bug-fix"},
			want: "cron:s1-bug-fix",
		},
		{
			name: "cron keeps an explicitly supplied route",
			in:   store.TriggerBindingCreate{SourceKind: store.CronSourceKind, BindingID: "s1", RouteKey: "custom"},
			want: "custom",
		},
		{
			name: "event source is never derived",
			in:   store.TriggerBindingCreate{SourceKind: "github", BindingID: "b1"},
			want: "",
		},
		{
			name: "cron without a binding_id is left empty (rejected downstream)",
			in:   store.TriggerBindingCreate{SourceKind: store.CronSourceKind},
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
