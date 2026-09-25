package stackstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// unwrapStore is a store.Store decorator (like cmdstore's tracing layer). It
// embeds the interface, so the decorated value's Stacks() is NOT promoted:
// ForStore must reach it through Unwrap.
type unwrapStore struct{ store.Store }

func (u unwrapStore) Unwrap() store.Store { return u.Store }

// loopStore unwraps to itself forever.
type loopStore struct{ store.Store }

func (l *loopStore) Unwrap() store.Store { return l }

func TestForStore(t *testing.T) {
	client, err := fleetdb.New(fleetdb.Config{BaseURL: "http://127.0.0.1:1"})
	require.NoError(t, err)
	mem := memstore.New()

	cases := []struct {
		name    string
		env     string
		s       store.Store
		want    string // "local", "fleetdb" or "error"
		errText string
	}{
		{name: "nil store", s: nil, want: "local"},
		{name: "memstore", s: mem, want: "local"},
		{name: "fleetdb client", s: client, want: "fleetdb"},
		{name: "decorated fleetdb client", s: unwrapStore{client}, want: "fleetdb"},
		{name: "doubly decorated fleetdb client", s: unwrapStore{unwrapStore{client}}, want: "fleetdb"},
		{name: "decorated memstore", s: unwrapStore{mem}, want: "local"},
		{name: "decorator unwrapping to nil", s: unwrapStore{nil}, want: "local"},
		{name: "unwrap loop is bounded", s: &loopStore{mem}, want: "local"},
		{name: "local forces LocalStore over fleetdb", env: "local", s: client, want: "local"},
		{name: "local is case and space insensitive", env: "  LOCAL ", s: unwrapStore{client}, want: "local"},
		{name: "fleetdb explicit", env: "fleetdb", s: client, want: "fleetdb"},
		{name: "fleetdb explicit through decorator", env: "FleetDB", s: unwrapStore{client}, want: "fleetdb"},
		{name: "fleetdb explicit without fleetdb store", env: "fleetdb", s: mem, want: "error", errText: "not backed by fleet-db"},
		{name: "fleetdb explicit with nil store", env: "fleetdb", s: nil, want: "error", errText: "not backed by fleet-db"},
		{name: "invalid value", env: "sqlite", s: client, want: "error", errText: `LOOM_STACK_STORE="sqlite" is invalid`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("LOOM_CONFIG_DIR", dir)
			t.Setenv(EnvStackStore, tc.env)

			got, err := ForStore(tc.s)
			switch tc.want {
			case "error":
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errText)
				assert.Nil(t, got)
			case "local":
				require.NoError(t, err)
				ls, ok := got.(*LocalStore)
				require.True(t, ok, "want *LocalStore, got %T", got)
				assert.Equal(t, dir, ls.dir, "LocalStore rooted at the loom dir")
			case "fleetdb":
				require.NoError(t, err)
				fs, ok := got.(*FleetDBStore)
				require.True(t, ok, "want *FleetDBStore, got %T", got)
				require.NotNil(t, fs.api)
			}
		})
	}
}

func TestLocalRequested(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{"", false},
		{"local", true},
		{" Local ", true}, // trimmed and case-insensitive
		{"fleetdb", false},
		{"bogus", false},
	} {
		t.Setenv(EnvStackStore, tc.env)
		assert.Equal(t, tc.want, LocalRequested(), "LOOM_STACK_STORE=%q", tc.env)
	}
}
