package archtest

import (
	"slices"
	"strconv"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestRepositoryScalePackageLoadsStaySerialized(t *testing.T) {
	if repositoryScaleLoadConcurrency != 1 {
		t.Fatalf("repository-scale package load concurrency = %d, want 1 to bound peak memory", repositoryScaleLoadConcurrency)
	}
}

func TestDirectWritePackageLoadModeTypesOnlyRequestedRoots(t *testing.T) {
	if directWritePackageLoadMode&packages.NeedDeps != 0 {
		t.Fatalf("direct-write package load mode %v includes dependency graph expansion", directWritePackageLoadMode)
	}
	required := packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports
	if directWritePackageLoadMode&required != required {
		t.Fatalf("direct-write package load mode %v does not include required type-analysis modes %v", directWritePackageLoadMode, required)
	}
}

func TestProfilePackageLoadModesSeparateTypedRootsFromDependencyMetadata(t *testing.T) {
	if profileTypedRootLoadMode&packages.NeedDeps != 0 {
		t.Fatalf("typed-root load mode %v includes dependency graph expansion", profileTypedRootLoadMode)
	}
	typedRequired := packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports
	if profileTypedRootLoadMode&typedRequired != typedRequired {
		t.Fatalf("typed-root load mode %v does not include %v", profileTypedRootLoadMode, typedRequired)
	}
	dependencyRequired := packages.NeedName | packages.NeedImports | packages.NeedDeps
	if profileDependencyLoadMode&dependencyRequired != dependencyRequired {
		t.Fatalf("dependency load mode %v does not include %v", profileDependencyLoadMode, dependencyRequired)
	}
	forbidden := packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo
	if profileDependencyLoadMode&forbidden != 0 {
		t.Fatalf("dependency load mode %v includes typed syntax fields %v", profileDependencyLoadMode, forbidden)
	}
}

func TestProfileBuildFlagsBoundCompilerParallelismAndPreserveTags(t *testing.T) {
	if repositoryPackageBuildParallelism < 1 || repositoryPackageBuildParallelism > 2 {
		t.Fatalf("repository package build parallelism = %d, want within [1,2]", repositoryPackageBuildParallelism)
	}
	flags := profileBuildFlags(AnalysisProfile{Tags: []string{"integration"}, Race: true})
	for _, want := range []string{"-p=" + strconv.Itoa(repositoryPackageBuildParallelism), "-tags=integration,race"} {
		if !slices.Contains(flags, want) {
			t.Fatalf("profile build flags = %v, want %q", flags, want)
		}
	}
}
