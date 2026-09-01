//go:build e2e

package skillse2e_test

import (
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var gcsPresignedRoundTrip = registry.Scenario{
	ID:        "gcs-presigned-roundtrip",
	Behavior:  "the production GCS XML path publishes and materializes exact Skill bytes",
	Owner:     "loom",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis"},
	Providers: []string{"gcs"},
}

func TestGCSPresignedRoundTrip(t *testing.T) {
	if os.Getenv("SKILLS_E2E_PROVIDER") != "gcs" {
		t.Skip("requires the existing GCS test bucket")
	}
	gcsPresignedRoundTrip.Covers(t)
	loom := harness.Open(t)
	source := loom.SkillFixture("exact-round-trip/updated")

	loom.SkillImport(source)
	selected := loom.SkillShow("exact-round-trip")
	loom.RequireSkill(selected, "exact-round-trip/expected.json")

	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(source, "exact-round-trip")
}
