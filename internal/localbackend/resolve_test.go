package localbackend

import "testing"

func TestSourceWireValues(t *testing.T) {
	// These values are serialized on the preflight wire and must never change.
	cases := []struct {
		name   string
		source Source
		want   string
	}{
		{"override", SourceOverride, "override"},
		{"agent", SourceAgent, "agent"},
		{"workspace", SourceWorkspace, "workspace"},
		{"default", SourceDefault, "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(tc.source); got != tc.want {
				t.Fatalf("Source %s wire value = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
