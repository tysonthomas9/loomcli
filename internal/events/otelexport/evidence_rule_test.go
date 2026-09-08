package otelexport

import "testing"

// Only the rule token becomes a span attribute. detail= and match= carry agent
// output, which would make the attribute unbounded in cardinality.
func TestEvidenceRuleOf(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    string
	}{
		{"empty", "", ""},
		{"source only", "supervisor", ""},
		{"rule after source", "supervisor rule=supervisor.no_work", "supervisor.no_work"},
		{
			"rule before detail and match",
			`harness_marker rule=AuthRequiredMarker exit=1 detail="rule=decoy" match="x"`,
			"AuthRequiredMarker",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evidenceRuleOf(tc.summary); got != tc.want {
				t.Errorf("evidenceRuleOf(%q) = %q, want %q", tc.summary, got, tc.want)
			}
		})
	}
}
