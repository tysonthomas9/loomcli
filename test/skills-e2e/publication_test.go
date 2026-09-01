//go:build e2e

package skillse2e_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var concurrentTreePublication = registry.Scenario{
	ID:        "concurrent-tree-publication",
	Behavior:  "concurrent imports accept one logical tree and select the same revision",
	Owner:     "fleet",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis", "postgres"},
	Providers: []string{"minio"},
	Cases: []registry.EdgeCase{
		{ID: 50, Behavior: "concurrent publication creates one logical tree", Rationale: "real concurrent public imports race at Fleet's revision-keyed command"},
		{ID: 51, Behavior: "concurrent publishers observe one accepted identity", Rationale: "both successful imports expose the same selected revision"},
		{ID: 52, Behavior: "first accepted provenance remains stable", Rationale: "Fleet's real-backend command conformance verifies all callers receive the accepted event"},
	},
}

func TestConcurrentSkillImportsSelectOneTreeRevision(t *testing.T) {
	concurrentTreePublication.Covers(t)
	loom := harness.Open(t)
	source := loom.SkillFixture("concurrent-publication/current")

	first := loom.StartSkillImport(source)
	second := loom.StartSkillImport(source)
	first.Wait()
	second.Wait()

	selected := loom.SkillShow("concurrent-publication")
	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(source, selected.Name)
}

var lostPublicationResponseIsRetryable = registry.Scenario{
	ID:        "lost-publication-response-is-retryable",
	Behavior:  "retrying after a committed response is lost returns the accepted tree",
	Owner:     "fleet",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis", "postgres"},
	Providers: []string{"minio"},
	Cases: []registry.EdgeCase{{
		ID: 66, Behavior: "an ambiguous publication retry does not create a second logical tree", Rationale: "a real proxy drops the successful public response after Fleet commits it",
	}},
}

func TestLostPublicationResponseCanBeRetried(t *testing.T) {
	lostPublicationResponseIsRetryable.Covers(t)
	loom := harness.Open(t)
	source := loom.SkillFixture("lost-response/current")

	dropped := loom.DropNextTreePublicationResponse()
	loom.SkillImportFails(source)
	dropped.RequireActivated()

	loom.SkillImport(source)
	selected := loom.SkillShow("lost-response")
	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(source, selected.Name)
}
