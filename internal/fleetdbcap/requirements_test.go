package fleetdbcap

import (
	"strings"
	"testing"
)

func TestRequirementsAreWellFormed(t *testing.T) {
	for _, r := range Requirements() {
		if strings.TrimSpace(r.Capability) == "" {
			t.Errorf("requirement %+v has an empty capability name", r)
			continue
		}
		if strings.TrimSpace(r.Feature) == "" {
			t.Errorf("%s: empty Feature; the boot message prints it as \"needed by\"", r.Capability)
		}
		if strings.TrimSpace(r.Route) == "" {
			t.Errorf("%s: empty Route; the boot message must name the route an operator has to deploy", r.Capability)
		}
	}
}

func TestDegradesRequirementsDescribeTheirEffect(t *testing.T) {
	for _, r := range Requirements() {
		switch r.Level {
		case Degrades:
			if strings.TrimSpace(r.DegradedEffect) == "" {
				t.Errorf("%s is Degrades but has no DegradedEffect; a degradation nobody can read is not a degradation an operator can act on", r.Capability)
			}
		case Required:
			if strings.TrimSpace(r.DegradedEffect) != "" {
				t.Errorf("%s is Required but carries a DegradedEffect; a required capability has no degraded mode", r.Capability)
			}
		}
	}
}

func TestRequirementCapabilitiesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Requirements() {
		if seen[r.Capability] {
			t.Errorf("duplicate requirement for capability %q", r.Capability)
		}
		seen[r.Capability] = true
	}
}

func TestSkillMaterializationLeasesDegradesRatherThanBlocks(t *testing.T) {
	// The judgement this manifest exists to keep in one place: a fleet-db
	// without the lease routes still runs agents, so it must not refuse boot.
	for _, r := range Requirements() {
		if r.Capability == "skill-materialization-leases" {
			if r.Level != Degrades {
				t.Fatalf("skill-materialization-leases must be Degrades, got %v", r.Level)
			}
			return
		}
	}
	t.Fatal("skill-materialization-leases is not in the manifest")
}

func TestRequirementsReturnsACopy(t *testing.T) {
	first := Requirements()
	if len(first) == 0 {
		t.Fatal("no requirements declared")
	}
	first[0].Capability = "mutated"
	if Requirements()[0].Capability == "mutated" {
		t.Fatal("Requirements() exposes the package manifest to mutation")
	}
}

func TestLevelString(t *testing.T) {
	if got := Required.String(); got != "required" {
		t.Errorf("Required.String() = %q", got)
	}
	if got := Degrades.String(); got != "degrades" {
		t.Errorf("Degrades.String() = %q", got)
	}
}
