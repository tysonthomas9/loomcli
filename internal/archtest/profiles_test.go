package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileBoundaryRulesMatchApprovedPackageShape(t *testing.T) {
	graph := validGraph()
	graph.Edges = []GraphEdge{{From: "execution", To: "agents", Kinds: []string{"import"}}}
	tests := []struct {
		name string
		from string
		to   string
		want string
	}{
		{
			name: "platform to capability",
			from: modulePath + "/internal/platform/runtime",
			to:   modulePath + "/internal/modules/workspace",
			want: "platform may not import",
		},
		{
			name: "platform to named application workflow",
			from: modulePath + "/internal/platform/runtime",
			to:   modulePath + "/internal/app/agentprovisioning",
			want: "product/app/legacy internals are forbidden",
		},
		{
			name: "platform to legacy store",
			from: modulePath + "/internal/platform/runtime",
			to:   modulePath + "/internal/store",
			want: "product/app/legacy internals are forbidden",
		},
		{
			name: "platform to other platform mechanism",
			from: modulePath + "/internal/platform/runtime",
			to:   modulePath + "/internal/platform/telemetry",
		},
		{
			name: "platform to neutral standard library",
			from: modulePath + "/internal/platform/runtime",
			to:   "net/http",
		},
		{
			name: "platform to unapproved external implementation",
			from: modulePath + "/internal/platform/runtime",
			to:   "github.com/redis/go-redis/v9",
			want: "platform external import",
		},
		{
			name: "named app core to declared capability public root",
			from: modulePath + "/internal/app/agentprovisioning",
			to:   modulePath + "/internal/modules/agents",
		},
		{
			name: "named app core to capability private package",
			from: modulePath + "/internal/app/agentprovisioning",
			to:   modulePath + "/internal/modules/agents/internal/repository",
			want: "public root",
		},
		{
			name: "named app core to app-local adapter",
			from: modulePath + "/internal/app/agentprovisioning",
			to:   modulePath + "/internal/app/agentprovisioning/fleetdb",
			want: "application implementations",
		},
		{
			name: "named app core to own port package",
			from: modulePath + "/internal/app/agentprovisioning",
			to:   modulePath + "/internal/app/agentprovisioning/ports",
		},
		{
			name: "named app core to infrastructure",
			from: modulePath + "/internal/app/agentprovisioning",
			to:   modulePath + "/internal/infra/fleetdb",
			want: "named application workflow core",
		},
		{
			name: "capability core to own adapter",
			from: modulePath + "/internal/modules/execution/internal/service",
			to:   modulePath + "/internal/modules/execution/fleetdb",
			want: "own concrete adapter",
		},
		{
			name: "capability public root to own adapter",
			from: modulePath + "/internal/modules/execution",
			to:   modulePath + "/internal/modules/execution/httpapi",
			want: "own concrete adapter",
		},
		{
			name: "capability adapter to own public root",
			from: modulePath + "/internal/modules/execution/fleetdb",
			to:   modulePath + "/internal/modules/execution",
		},
		{
			name: "capability adapter to own private core",
			from: modulePath + "/internal/modules/execution/fleetdb",
			to:   modulePath + "/internal/modules/execution/internal/repository",
			want: "only its own public root",
		},
		{
			name: "capability adapter to declared cross capability public root",
			from: modulePath + "/internal/modules/execution/fleetdb",
			to:   modulePath + "/internal/modules/agents",
			want: "only its own public root",
		},
		{
			name: "capability core to arbitrary legacy internal implementation",
			from: modulePath + "/internal/modules/execution/internal/service",
			to:   modulePath + "/internal/leadcontrol",
			want: "internal implementation package",
		},
		{
			name: "capability adapter to arbitrary legacy internal implementation",
			from: modulePath + "/internal/modules/execution/httpapi",
			to:   modulePath + "/internal/trigger",
			want: "only its own public root",
		},
		{
			name: "capability fleetdb adapter to shared transport",
			from: modulePath + "/internal/modules/execution/fleetdb",
			to:   modulePath + "/internal/infra/fleetdb",
		},
		{
			name: "capability http adapter to shared transport",
			from: modulePath + "/internal/modules/execution/httpapi",
			to:   modulePath + "/internal/infra/fleetdb",
			want: "shared FleetDB transport from a fleetdb adapter",
		},
		{
			name: "capability adapter to its own nested adapter package",
			from: modulePath + "/internal/modules/execution/fleetdb",
			to:   modulePath + "/internal/modules/execution/fleetdb/codec",
		},
		{
			name: "capability core to approved platform mechanism",
			from: modulePath + "/internal/modules/execution/internal/service",
			to:   modulePath + "/internal/platform/runtime",
		},
		{
			name: "capability core to neutral standard library",
			from: modulePath + "/internal/modules/execution/internal/service",
			to:   "strings",
		},
		{
			name: "capability core to standard transport implementation",
			from: modulePath + "/internal/modules/execution/internal/service",
			to:   "net/http",
			want: "standard-library infrastructure package",
		},
		{
			name: "capability adapter to standard transport implementation",
			from: modulePath + "/internal/modules/execution/httpapi",
			to:   "net/http",
		},
		{
			name: "capability core to unapproved external storage implementation",
			from: modulePath + "/internal/modules/execution/internal/service",
			to:   "github.com/redis/go-redis/v9",
			want: "core external import",
		},
		{
			name: "capability adapter to unapproved external storage implementation",
			from: modulePath + "/internal/modules/execution/fleetdb",
			to:   "github.com/redis/go-redis/v9",
			want: "adapter external import",
		},
		{
			name: "declared cross capability public import",
			from: modulePath + "/internal/modules/execution",
			to:   modulePath + "/internal/modules/agents",
		},
		{
			name: "private cross capability import",
			from: modulePath + "/internal/modules/execution",
			to:   modulePath + "/internal/modules/workspace/internal/repository",
			want: "public root",
		},
		{
			name: "undeclared cross capability edge",
			from: modulePath + "/internal/modules/workspace",
			to:   modulePath + "/internal/modules/agents",
			want: "edge is not declared",
		},
		{
			name: "serve composition to capability adapter",
			from: modulePath + "/internal/app/serve",
			to:   modulePath + "/internal/modules/execution/fleetdb",
		},
		{
			name: "serve composition to legacy infrastructure",
			from: modulePath + "/internal/app/serve/workspace",
			to:   modulePath + "/internal/infra/fleetdb",
		},
		{
			name: "app-local adapter to own workflow API",
			from: modulePath + "/internal/app/agentprovisioning/fleetdb",
			to:   modulePath + "/internal/app/agentprovisioning",
		},
		{
			name: "app-local adapter to shared FleetDB transport",
			from: modulePath + "/internal/app/agentprovisioning/fleetdb",
			to:   modulePath + "/internal/infra/fleetdb",
		},
		{
			name: "app-local http adapter to shared FleetDB transport",
			from: modulePath + "/internal/app/agentprovisioning/httpapi",
			to:   modulePath + "/internal/infra/fleetdb",
			want: "shared FleetDB transport from a fleetdb adapter",
		},
		{
			name: "app-local adapter to nested own adapter package",
			from: modulePath + "/internal/app/agentprovisioning/fleetdb",
			to:   modulePath + "/internal/app/agentprovisioning/fleetdb/codec",
		},
		{
			name: "app-local adapter to capability public root",
			from: modulePath + "/internal/app/agentprovisioning/fleetdb",
			to:   modulePath + "/internal/modules/agents",
			want: "application adapter may import only",
		},
		{
			name: "named app core to arbitrary legacy internal implementation",
			from: modulePath + "/internal/app/agentprovisioning",
			to:   modulePath + "/internal/leadcontrol",
			want: "only capability public APIs",
		},
		{
			name: "named app core to neutral standard library",
			from: modulePath + "/internal/app/agentprovisioning",
			to:   "strings",
		},
		{
			name: "named app core to standard transport implementation",
			from: modulePath + "/internal/app/agentprovisioning",
			to:   "net/http",
			want: "standard-library infrastructure package",
		},
		{
			name: "named app core to unapproved external storage implementation",
			from: modulePath + "/internal/app/agentprovisioning",
			to:   "github.com/redis/go-redis/v9",
			want: "workflow core external import",
		},
		{
			name: "app-local adapter to unapproved external storage implementation",
			from: modulePath + "/internal/app/agentprovisioning/fleetdb",
			to:   "github.com/redis/go-redis/v9",
			want: "workflow adapter external import",
		},
		{
			name: "similarly prefixed named app is not serve composition",
			from: modulePath + "/internal/app/server",
			to:   modulePath + "/internal/infra/fleetdb",
			want: "named application workflow core",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forbiddenBoundaryImport(tt.from, tt.to, graph)
			if tt.want == "" {
				if got != "" {
					t.Fatalf("forbiddenBoundaryImport() = %q, want allowed import", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("forbiddenBoundaryImport() = %q, want fragment %q", got, tt.want)
			}
		})
	}
}

func TestAnalyzeProfileTypeChecksTagSelectedTestSource(t *testing.T) {
	t.Setenv("GOFLAGS", "-tags=profilefixture")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, root, "internal/example/example.go", "package example\n")
	writeGoFile(t, root, "internal/example/tagged_test.go", `//go:build profilefixture

package example

var _ int = "tag-only type error"
`)

	untagged, err := analyzeProfile(root, AnalysisProfile{
		Name: "untagged", GOOS: "linux", GOARCH: "amd64", Enforced: true,
	}, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if containsViolation(untagged, "cannot use") {
		t.Fatalf("untagged profile selected tagged type error: %v", untagged)
	}

	tagged, err := analyzeProfile(root, AnalysisProfile{
		Name: "tagged", GOOS: "linux", GOARCH: "amd64", Tags: []string{"profilefixture"}, Enforced: true,
	}, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(tagged, "cannot use") {
		t.Fatalf("tagged profile did not report selected test type error: %v", tagged)
	}
}

func TestAnalyzeProfileRequiresSelectedSourceSentinel(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, root, "internal/example/example.go", "package example\n")
	const sentinel = "internal/example/tagged.go"
	writeGoFile(t, root, sentinel, `//go:build profilefixture

package example
`)

	selected, err := analyzeProfile(root, AnalysisProfile{
		Name: "selected", GOOS: "linux", GOARCH: "amd64", Tags: []string{"profilefixture"},
		RequiredFiles: []string{sentinel}, Enforced: true,
	}, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if containsViolation(selected, "did not select required source") {
		t.Fatalf("selected sentinel reported missing: %v", selected)
	}

	missing, err := analyzeProfile(root, AnalysisProfile{
		Name: "missing", GOOS: "linux", GOARCH: "amd64", Tags: []string{"misspelled"},
		RequiredFiles: []string{sentinel}, Enforced: true,
	}, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(missing, "profile missing did not select required source "+sentinel) {
		t.Fatalf("missing sentinel did not fail profile: %v", missing)
	}
}

func TestAnalyzeProfileReportsTransitiveForbiddenDependencyWithProfile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, root, "internal/modules/workspace/api.go", `package workspace
import _ "github.com/tysonthomas9/loomcli/internal/safe"
`)
	writeGoFile(t, root, "internal/safe/safe.go", `package safe
import _ "github.com/tysonthomas9/loomcli/internal/store"
`)
	writeGoFile(t, root, "internal/store/store.go", "package store\n")

	violations, err := analyzeProfile(root, AnalysisProfile{
		Name: "fixture-linux-amd64", GOOS: "linux", GOARCH: "amd64", Enforced: true,
	}, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	want := "profile fixture-linux-amd64 package " + modulePath + "/internal/modules/workspace reaches forbidden transitive dependency " + modulePath + "/internal/store"
	if !containsViolation(violations, want) {
		t.Fatalf("violations = %v, want profile-attributed transitive violation %q", violations, want)
	}
}

func TestAnalyzeProfileRejectsExportedSignatureThroughLocalAlias(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, root, "internal/domain/domain.go", "package domain\ntype Agent struct{}\n")
	writeGoFile(t, root, "internal/modules/workspace/api.go", `package workspace
import "github.com/tysonthomas9/loomcli/internal/domain"
type legacy = domain.Agent
func Leaked() legacy { return legacy{} }
`)

	violations, err := analyzeProfile(root, AnalysisProfile{
		Name: "alias-fixture", GOOS: "linux", GOARCH: "amd64", Enforced: true,
	}, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "exported Leaked leaks forbidden type from "+modulePath+"/internal/domain") {
		t.Fatalf("violations = %v, want semantic alias leakage", violations)
	}
}

func TestAnalyzeProfileRejectsForbiddenTypeInsideAllowedGenericWrapper(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, root, "internal/domain/domain.go", "package domain\ntype Agent struct{}\n")
	writeGoFile(t, root, "internal/neutral/box.go", "package neutral\ntype Box[T any] struct { Value T }\n")
	writeGoFile(t, root, "internal/modules/workspace/api.go", `package workspace
import (
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/neutral"
)
func Leaked() neutral.Box[domain.Agent] { return neutral.Box[domain.Agent]{} }
`)

	violations, err := analyzeProfile(root, AnalysisProfile{
		Name: "generic-fixture", GOOS: "linux", GOARCH: "amd64", Enforced: true,
	}, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "exported Leaked leaks forbidden type from "+modulePath+"/internal/domain") {
		t.Fatalf("violations = %v, want forbidden generic type-argument leakage", violations)
	}
}

func TestAllFilesRejectsExportedSignatureThroughLocalAlias(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/workspace/api_windows.go", `//go:build windows

package workspace
import "github.com/tysonthomas9/loomcli/internal/domain"
type legacy = domain.Agent
func Leaked() legacy { return legacy{} }
`)
	matrix := AnalysisMatrix{AST: ASTProfile{IncludeTests: true, ExcludeGenerated: true}}
	violations, err := analyzeAllGoFiles(root, matrix, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "exports local alias legacy of forbidden type from "+modulePath+"/internal/domain") {
		t.Fatalf("violations = %v, want all-files alias leakage", violations)
	}
}

func TestAllFilesRejectsCompositeStoreInjectionAndExportLeak(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/workspace/api.go", `package workspace
import "github.com/tysonthomas9/loomcli/internal/store"
type API struct { Store store.Store }
`)
	matrix := AnalysisMatrix{AST: ASTProfile{IncludeTests: true, ExcludeGenerated: true}}
	violations, err := analyzeAllGoFiles(root, matrix, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"injects composite store.Store", "exports legacy or implementation type store.Store"} {
		if !containsViolation(violations, want) {
			t.Fatalf("violations = %v, want %q", violations, want)
		}
	}
}

func TestAllFilesRequiresCurrentExplicitIgnoreEntry(t *testing.T) {
	root := t.TempDir()
	path := "internal/modules/workspace/legacy.go"
	writeGoFile(t, root, path, "//go:build ignore\n\npackage workspace\n")
	matrix := AnalysisMatrix{AST: ASTProfile{IncludeTests: true, ExcludeGenerated: true}}
	violations, err := analyzeAllGoFiles(root, matrix, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "unallowlisted //go:build ignore") {
		t.Fatalf("violations = %v, want unallowlisted-ignore rejection", violations)
	}

	matrix.AST.Ignore = []IgnoreException{{
		Path: path, Reason: "fixture", Owner: "architecture-test", Expiry: "2099-12-31",
	}}
	violations, err = analyzeAllGoFiles(root, matrix, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("current explicit ignore produced violations: %v", violations)
	}

	matrix.AST.Ignore[0].Path = "internal/modules/workspace/removed.go"
	violations, err = analyzeAllGoFiles(root, matrix, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "stale AST ignore exception") {
		t.Fatalf("violations = %v, want stale-ignore rejection", violations)
	}
}
