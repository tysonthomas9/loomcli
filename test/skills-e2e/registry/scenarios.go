package registry

var Scenarios = []Scenario{
	SkillUpdateRoundTrip,
	StableIdenticalReimport,
	ContentUpdatePreservesBundles,
	RematerializationPrunesStaleFiles,
	DeletionPrunesMaterialization,
	ListShowRevisionAgreement,
}

var SkillUpdateRoundTrip = Scenario{
	ID:        "skill-update-roundtrip",
	Behavior:  "an update selects and materializes the exact new revision",
	Test:      "TestSkillUpdateSelectsAndMaterializesExactRevision",
	Owner:     "loom",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis"},
	Providers: []string{"minio"},
	Status:    "covered",
	Cases: []EdgeCase{
		{ID: 1, Behavior: "binary bytes and executable mode round-trip exactly", Rationale: "the materialized tree is compared byte-for-byte and mode-for-mode"},
		{ID: 2, Behavior: "a zero-byte bundled file round-trips exactly", Rationale: "the fixture contains empty.dat and the exact tree comparison requires zero bytes"},
		{ID: 3, Behavior: "nested slash-separated paths round-trip exactly", Rationale: "the fixture requires exact nested docs, assets, and scripts paths"},
		{ID: 12, Behavior: "non-root files retain arbitrary bytes and executable mode", Rationale: "binary and executable non-root files are checked against literal fixtures"},
		{ID: 15, Behavior: "updating Skill content selects and materializes a new tree revision", Rationale: "two public imports require different revisions and the second literal revision is selected"},
	},
}

var StableIdenticalReimport = Scenario{
	ID:        "stable-identical-reimport",
	Behavior:  "importing identical content retains the content revision",
	Test:      "TestIdenticalSkillReimportKeepsContentRevision",
	Owner:     "loom",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis"},
	Providers: []string{"minio"},
	Status:    "covered",
}

var ContentUpdatePreservesBundles = Scenario{
	ID:        "content-update-preserves-bundles",
	Behavior:  "updating only Skill content preserves every bundled file",
	Test:      "TestSkillContentUpdatePreservesBundledFiles",
	Owner:     "loom",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis"},
	Providers: []string{"minio"},
	Status:    "covered",
}

var RematerializationPrunesStaleFiles = Scenario{
	ID:        "rematerialization-prunes-stale-files",
	Behavior:  "rematerialization removes files absent from the selected revision",
	Test:      "TestSkillRematerializationRemovesStaleFiles",
	Owner:     "loom",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis"},
	Providers: []string{"minio"},
	Status:    "covered",
}

var DeletionPrunesMaterialization = Scenario{
	ID:        "deletion-prunes-materialization",
	Behavior:  "deleting a Skill prunes it from an existing materialization",
	Test:      "TestSkillDeletionPrunesExistingMaterialization",
	Owner:     "loom",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis"},
	Providers: []string{"minio"},
	Status:    "covered",
}

var ListShowRevisionAgreement = Scenario{
	ID:        "list-show-revision-agreement",
	Behavior:  "public list and show results report the same selected revision",
	Test:      "TestSkillListReportsSelectedRevision",
	Owner:     "loom",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis"},
	Providers: []string{"minio"},
	Status:    "covered",
}
