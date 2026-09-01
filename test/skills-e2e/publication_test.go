//go:build e2e

package skillse2e_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var concurrentTreePublication = registry.Scenario{
	ID:       "concurrent-tree-publication",
	Behavior: "concurrent imports accept one logical tree and select the same revision",
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
	ID:       "lost-publication-response-is-retryable",
	Behavior: "retrying after a committed response is lost returns the accepted tree",
	Cases: []registry.EdgeCase{{
		ID: 66,
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
