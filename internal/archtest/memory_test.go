package archtest

import (
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestRepositoryScalePackageLoadsStaySerialized(t *testing.T) {
	if repositoryScaleLoadConcurrency != 1 {
		t.Fatalf("repository-scale package load concurrency = %d, want 1 to bound peak memory", repositoryScaleLoadConcurrency)
	}
}

func TestDirectWritePackageLoadModeTypesOnlyRequestedRoots(t *testing.T) {
	for _, forbidden := range []packages.LoadMode{packages.NeedDeps, packages.NeedModule} {
		if directWritePackageLoadMode&forbidden != 0 {
			t.Fatalf("direct-write package load mode %v includes forbidden repository-expanding mode %v", directWritePackageLoadMode, forbidden)
		}
	}
	required := packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports
	if directWritePackageLoadMode&required != required {
		t.Fatalf("direct-write package load mode %v does not include required type-analysis modes %v", directWritePackageLoadMode, required)
	}
}
