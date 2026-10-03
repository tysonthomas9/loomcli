package review

import "testing"

// A task's PR shows its real state: merged once it landed, the provider saw it
// merged, or its Approve and merge finished; closed when the provider closed it
// without merging; open otherwise.
func TestPRStateReportsMergedAndClosedPRs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		landed      bool
		observed    string
		mergeStatus string
		want        string
	}{
		{name: "open", observed: "open", want: "open"},
		{name: "not observed yet", want: "open"},
		{name: "approved, waiting", observed: "open", mergeStatus: "waiting", want: "open"},
		{name: "landed", landed: true, observed: "open", want: "merged"},
		{name: "provider merged", observed: "merged", want: "merged"},
		{name: "approve and merge finished", mergeStatus: "merged", want: "merged"},
		{name: "closed", observed: "closed", want: "closed"},
		{name: "dependency abandoned", observed: "dependency_abandoned", want: "closed"},
	} {
		if got := PRState(tc.landed, tc.observed, tc.mergeStatus); got != tc.want {
			t.Errorf("%s: PRState = %q, want %q", tc.name, got, tc.want)
		}
	}
}
