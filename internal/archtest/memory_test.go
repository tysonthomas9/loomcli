package archtest

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestRepositoryScalePackageLoadsStaySerialized(t *testing.T) {
	if repositoryScaleLoadConcurrency != 1 {
		t.Fatalf("repository-scale package load concurrency = %d, want 1 to bound peak memory", repositoryScaleLoadConcurrency)
	}
}

func TestRepositoryProfileCacheIsIsolatedAndRemoved(t *testing.T) {
	inherited := filepath.Join(t.TempDir(), "inherited-cache")
	t.Setenv("GOCACHE", inherited)

	var scoped string
	err := withRepositoryProfileCache(
		AnalysisProfile{Name: "cache-test", GOOS: "plan9", GOARCH: "amd64"},
		func(environment []string) error {
			for _, entry := range environment {
				if strings.HasPrefix(entry, "GOCACHE=") {
					scoped = strings.TrimPrefix(entry, "GOCACHE=")
					break
				}
			}
			if scoped == "" {
				t.Fatal("scoped profile environment has no GOCACHE")
			}
			if scoped == inherited {
				t.Fatalf("scoped GOCACHE = inherited cache %q", inherited)
			}
			return os.WriteFile(filepath.Join(scoped, "proof"), []byte("scoped"), 0o600)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scoped); !os.IsNotExist(err) {
		t.Fatalf("scoped GOCACHE still exists after profile analysis: %v", err)
	}
	if _, err := os.Stat(inherited); !os.IsNotExist(err) {
		t.Fatalf("inherited GOCACHE was mutated: %v", err)
	}
}

func TestRepositoryNativeTaggedRaceProfileReusesExplicitCallerCache(t *testing.T) {
	inherited := t.TempDir()
	t.Setenv("GOCACHE", inherited)

	var scoped string
	err := withRepositoryProfileCache(
		AnalysisProfile{
			Name: "native-cache-test", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			Tags: []string{"integration"}, Race: true,
		},
		func(environment []string) error {
			for _, entry := range environment {
				if strings.HasPrefix(entry, "GOCACHE=") {
					scoped = strings.TrimPrefix(entry, "GOCACHE=")
					break
				}
			}
			return os.WriteFile(filepath.Join(scoped, "proof"), []byte("reused"), 0o600)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if scoped != inherited {
		t.Fatalf("native profile GOCACHE = %q, want inherited %q", scoped, inherited)
	}
	if _, err := os.Stat(filepath.Join(inherited, "proof")); err != nil {
		t.Fatalf("native profile did not reuse caller cache: %v", err)
	}
}

func TestRepositoryProfileCacheIsRemovedAfterAnalysisFailure(t *testing.T) {
	// Force the native-profile reuse check to fail on every host. Otherwise the
	// linux/amd64 CI runner correctly reuses its caller cache and this test
	// incorrectly expects that shared cache to be deleted.
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "missing-cache"))

	sentinel := errors.New("analysis failed")
	var scoped string
	err := withRepositoryProfileCache(
		AnalysisProfile{Name: "failure-test", GOOS: "linux", GOARCH: "amd64"},
		func(environment []string) error {
			for _, entry := range environment {
				if strings.HasPrefix(entry, "GOCACHE=") {
					scoped = strings.TrimPrefix(entry, "GOCACHE=")
					break
				}
			}
			if scoped == "" || !filepath.IsAbs(scoped) {
				t.Fatalf("scoped GOCACHE = %q, want an absolute path", scoped)
			}
			if err := os.WriteFile(filepath.Join(scoped, "proof"), []byte("scoped"), 0o600); err != nil {
				return err
			}
			return sentinel
		},
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("profile analysis error = %v, want %v", err, sentinel)
	}
	if _, err := os.Stat(scoped); !os.IsNotExist(err) {
		t.Fatalf("failed profile GOCACHE still exists: %v", err)
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
	typedRequired := packages.NeedName | packages.NeedCompiledGoFiles | packages.NeedTypes | packages.NeedImports
	if profileTypedRootLoadMode&typedRequired != typedRequired {
		t.Fatalf("typed-root load mode %v does not include %v", profileTypedRootLoadMode, typedRequired)
	}
	expensiveSyntax := packages.NeedSyntax | packages.NeedTypesInfo
	if profileTypedRootLoadMode&expensiveSyntax != 0 {
		t.Fatalf("typed-root load mode %v includes focused syntax fields %v", profileTypedRootLoadMode, expensiveSyntax)
	}
	focusedRequired := typedRequired | packages.NeedSyntax | packages.NeedTypesInfo
	if profileGenericMechanismLoadMode&focusedRequired != focusedRequired {
		t.Fatalf("generic-mechanism load mode %v does not include %v", profileGenericMechanismLoadMode, focusedRequired)
	}
	if profileGenericMechanismLoadMode&packages.NeedDeps != 0 {
		t.Fatalf("generic-mechanism load mode %v includes dependency graph expansion", profileGenericMechanismLoadMode)
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
