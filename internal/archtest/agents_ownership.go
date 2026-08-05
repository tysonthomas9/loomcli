package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

const (
	legacyAgentServiceStoreReceiver  = "github.com/tysonthomas9/loomcli/internal/store.AgentServiceStore"
	legacyRoleStoreReceiver          = "github.com/tysonthomas9/loomcli/internal/store.RoleStore"
	agentsAgentIdentityStoreReceiver = "github.com/tysonthomas9/loomcli/internal/modules/agents.AgentIdentityStore"
	agentsDesiredStateStoreReceiver  = "github.com/tysonthomas9/loomcli/internal/modules/agents.DesiredStateStore"
	agentsLifecycleStoreReceiver     = "github.com/tysonthomas9/loomcli/internal/modules/agents.LifecycleStore"
	agentsOwnershipStoreReceiver     = "github.com/tysonthomas9/loomcli/internal/modules/agents.OwnershipStore"
	agentsRolePromptStoreReceiver    = "github.com/tysonthomas9/loomcli/internal/modules/agents.RolePromptRepairStore"
	agentsRoleStoreReceiver          = "github.com/tysonthomas9/loomcli/internal/modules/agents.RoleStore"
)

var phase5AgentsMutationMethods = map[string]map[string]struct{}{
	legacyAgentServiceStoreReceiver: {
		"Create": {},
		"Delete": {},
		"Update": {},
	},
	legacyRoleStoreReceiver: {
		"Create": {},
		"Delete": {},
		"Update": {},
	},
	agentsAgentIdentityStoreReceiver: {
		"ArchiveAgent": {},
		"CreateAgent":  {},
		"UpdateAgent":  {},
	},
	agentsDesiredStateStoreReceiver: {
		"SetDesiredState":      {},
		"SetDesiredStateOwned": {},
	},
	agentsLifecycleStoreReceiver: {
		"ApplyLifecycle": {},
	},
	agentsOwnershipStoreReceiver: {
		"AcquireOwnership": {},
		"ReleaseOwnership": {},
		"RenewOwnership":   {},
	},
	agentsRolePromptStoreReceiver: {
		"SetPromptFileIfEmpty": {},
	},
	agentsRoleStoreReceiver: {
		"CreateRole": {},
		"DeleteRole": {},
		"UpdateRole": {},
	},
}

// phase5AgentsReadMethods completes the classification of every method on the
// nine persistence receiver families above. The completeness test loads the
// real interface definitions and fails whenever a method is added, removed, or
// silently changes classification. Without this second half, a newly added
// mutator whose name is absent from phase5AgentsMutationMethods would evade the
// candidate scan entirely.
var phase5AgentsReadMethods = map[string]map[string]struct{}{
	legacyAgentServiceStoreReceiver: {
		"Get":  {},
		"List": {},
	},
	legacyRoleStoreReceiver: {
		"Get":  {},
		"List": {},
	},
	agentsAgentIdentityStoreReceiver: {},
	agentsDesiredStateStoreReceiver:  {},
	agentsLifecycleStoreReceiver:     {},
	agentsOwnershipStoreReceiver: {
		"GetOwnership":  {},
		"ListOwnership": {},
	},
	agentsRolePromptStoreReceiver: {},
	agentsRoleStoreReceiver: {
		"GetRole":   {},
		"ListRoles": {},
	},
}

type phase5AgentsMutationIdentity struct {
	file     string
	line     int
	column   int
	receiver string
	method   string
}

type phase5AgentsMutation struct {
	phase5AgentsMutationIdentity
	profiles map[string]struct{}
}

func (mutation phase5AgentsMutation) withProfiles() string {
	profiles := make([]string, 0, len(mutation.profiles))
	for profile := range mutation.profiles {
		profiles = append(profiles, profile)
	}
	slices.Sort(profiles)
	return fmt.Sprintf(
		"%s:%d:%d: %s.%s (profiles: %s)",
		mutation.file,
		mutation.line,
		mutation.column,
		mutation.receiver,
		mutation.method,
		strings.Join(profiles, ", "),
	)
}

// snapshotPhase5AgentsMutations deliberately does not reuse the generic
// direct-write inventory. That inventory is default-deny for every declared
// persistence surface, while this Phase 5 ratchet has one narrower question:
// where do production callers reference the mutating methods of the legacy
// Agent/AgentService/Role stores or any Agents-owned persistence port?
//
// A cheap all-files AST pass first finds candidate production packages. Each
// supported build profile then type-checks only those candidates, so aliases,
// embedded interfaces, and method values resolve to the declaring interface
// without loading syntax for the whole repository.
//
//nolint:funlen // Keep the bounded per-profile cache lifetime, concurrent scan, and deterministic merge as one auditable analysis transaction.
func snapshotPhase5AgentsMutations(
	root string,
	matrix AnalysisMatrix,
) ([]phase5AgentsMutation, error) {
	patterns, err := phase5AgentsMutationCandidatePatterns(root)
	if err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		return nil, nil
	}

	profiles := append(append([]AnalysisProfile{}, matrix.Release...), matrix.Tagged...)
	if len(profiles) == 0 {
		return nil, fmt.Errorf("phase 5 Agents ownership analysis requires at least one build profile")
	}
	type result struct {
		mutations []phase5AgentsMutation
		err       error
	}
	results := make([]result, len(profiles))
	semaphore := make(chan struct{}, repositoryScaleLoadConcurrency)
	var wg sync.WaitGroup
	for index, profile := range profiles {
		index, profile := index, profile
		wg.Add(1)
		go func() {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			results[index].err = withRepositoryProfileCache(profile, func(environment []string) error {
				var loadErr error
				results[index].mutations, loadErr = snapshotPhase5AgentsMutationProfile(
					root,
					profile,
					patterns,
					environment,
				)
				return loadErr
			})
		}()
	}
	wg.Wait()

	merged := map[phase5AgentsMutationIdentity]phase5AgentsMutation{}
	for index, result := range results {
		if result.err != nil {
			return nil, fmt.Errorf(
				"scan Phase 5 Agents ownership for profile %s: %w",
				profiles[index].Name,
				result.err,
			)
		}
		for _, mutation := range result.mutations {
			current, ok := merged[mutation.phase5AgentsMutationIdentity]
			if !ok {
				current = mutation
				current.profiles = map[string]struct{}{}
			}
			current.profiles[profiles[index].Name] = struct{}{}
			merged[mutation.phase5AgentsMutationIdentity] = current
		}
	}

	mutations := make([]phase5AgentsMutation, 0, len(merged))
	for _, mutation := range merged {
		mutations = append(mutations, mutation)
	}
	slices.SortFunc(mutations, func(left, right phase5AgentsMutation) int {
		if compared := strings.Compare(left.file, right.file); compared != 0 {
			return compared
		}
		if left.line != right.line {
			return left.line - right.line
		}
		if left.column != right.column {
			return left.column - right.column
		}
		if compared := strings.Compare(left.receiver, right.receiver); compared != 0 {
			return compared
		}
		return strings.Compare(left.method, right.method)
	})
	return mutations, nil
}

func phase5AgentsMutationCandidatePatterns(root string) ([]string, error) {
	candidates := map[string]struct{}{}
	internalRoot := filepath.Join(root, "internal")
	if err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != internalRoot && excludedWalkDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		candidate, err := sourceContainsPhase5AgentsMutationSelector(path)
		if err != nil {
			return err
		}
		if !candidate {
			return nil
		}
		relativeDirectory, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		candidates["./"+filepath.ToSlash(relativeDirectory)] = struct{}{}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("find Phase 5 Agents ownership candidates: %w", err)
	}

	patterns := make([]string, 0, len(candidates))
	for pattern := range candidates {
		patterns = append(patterns, pattern)
	}
	slices.Sort(patterns)
	return patterns, nil
}

func sourceContainsPhase5AgentsMutationSelector(path string) (bool, error) {
	contents, err := os.ReadFile(path) //nolint:gosec // repository walk controls the path
	if err != nil {
		return false, err
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, contents, parser.SkipObjectResolution)
	if err != nil {
		// The focused type-check will produce the authoritative syntax error.
		return true, nil
	}
	candidate := false
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || !isPhase5AgentsMutationMethodName(selector.Sel.Name) {
			return !candidate
		}
		candidate = true
		return false
	})
	return candidate, nil
}

func isPhase5AgentsMutationMethodName(method string) bool {
	for _, methods := range phase5AgentsMutationMethods {
		if _, ok := methods[method]; ok {
			return true
		}
	}
	return false
}

func snapshotPhase5AgentsMutationProfile(
	root string,
	profile AnalysisProfile,
	patterns, environment []string,
) ([]phase5AgentsMutation, error) {
	loaded, err := packages.Load(
		&packages.Config{
			Mode:       directWritePackageLoadMode,
			Dir:        root,
			Env:        environment,
			Tests:      false,
			BuildFlags: profileBuildFlags(profile),
		},
		patterns...,
	)
	if err != nil {
		return nil, err
	}
	var mutations []phase5AgentsMutation
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("load package %s: %s", pkg.PkgPath, pkg.Errors[0].Msg)
		}
		mutations = append(mutations, collectPhase5AgentsMutationPackage(root, pkg)...)
	}
	return mutations, nil
}

func collectPhase5AgentsMutationPackage(
	root string,
	pkg *packages.Package,
) []phase5AgentsMutation {
	var mutations []phase5AgentsMutation
	for _, file := range pkg.Syntax {
		position := pkg.Fset.Position(file.Pos())
		relative, err := filepath.Rel(root, position.Filename)
		if err != nil {
			continue
		}
		relative = filepath.ToSlash(relative)
		if !strings.HasPrefix(relative, "internal/") || strings.HasSuffix(relative, "_test.go") {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || !isPhase5AgentsMutationMethodName(selector.Sel.Name) {
				return true
			}
			selection := pkg.TypesInfo.Selections[selector]
			if selection == nil {
				return true
			}
			method, ok := selection.Obj().(*types.Func)
			if !ok {
				return true
			}
			_, _, receiver, ok := persistenceMethodReceiver(method)
			receiver = strings.TrimPrefix(receiver, "*")
			methods, target := phase5AgentsMutationMethods[receiver]
			if !ok || !target {
				return true
			}
			if _, mutating := methods[method.Name()]; !mutating {
				return true
			}
			callPosition := pkg.Fset.Position(selector.Sel.Pos())
			mutations = append(mutations, phase5AgentsMutation{
				phase5AgentsMutationIdentity: phase5AgentsMutationIdentity{
					file:     relative,
					line:     callPosition.Line,
					column:   callPosition.Column,
					receiver: receiver,
					method:   method.Name(),
				},
			})
			return true
		})
	}
	return mutations
}

func isPhase5AgentsMutationAllowed(mutation phase5AgentsMutation) bool {
	switch mutation.receiver {
	case agentsRoleStoreReceiver:
		// Role writes originate only in the public role commands or the
		// system-only exact-definition provisioning command.
		return mutation.file == "internal/modules/agents/provisioning.go" ||
			mutation.file == "internal/modules/agents/role_management.go"
	case agentsAgentIdentityStoreReceiver:
		// Identity writes originate in operator identity commands and the
		// system-only exact-definition provisioning command.
		return mutation.file == "internal/modules/agents/provisioning.go" ||
			mutation.file == "internal/modules/agents/service.go"
	case agentsDesiredStateStoreReceiver,
		agentsOwnershipStoreReceiver:
		// Desired-state, aggregate-lifecycle, and ownership mutations are
		// admitted and validated by the Agents service itself.
		return mutation.file == "internal/modules/agents/service.go"
	case agentsLifecycleStoreReceiver:
		return mutation.file == "internal/modules/agents/service.go" ||
			mutation.file == "internal/modules/agents/desired_state_reconciliation.go"
	case agentsRolePromptStoreReceiver:
		// Startup prompt repair is a deliberately narrow owner-private
		// compatibility adapter around one atomic repair primitive.
		return mutation.file == "internal/infra/agentsbootstrapstore/adapter.go"
	case legacyAgentServiceStoreReceiver, legacyRoleStoreReceiver:
		// agentsbootstrapstore is the bounded role/service bootstrap adapter.
		// FleetDB adapters remain owner-side transport implementations.
		return mutation.file == "internal/infra/agentsbootstrapstore/adapter.go" ||
			strings.HasPrefix(mutation.file, "internal/modules/agents/fleetdb/") ||
			strings.HasPrefix(mutation.file, "internal/infra/fleetdb/")
	default:
		return false
	}
}

const (
	phase5AgentsCompatibilityStoreImport = "github.com/tysonthomas9/loomcli/internal/infra/agentsbootstrapstore"
)

var phase5AgentsCompatibilityCompositions = map[string]struct{}{
	"internal/cli/serve/workspacemgr/agentsbootstrapcomposition/managed.go": {},
}

// snapshotPhase5AgentsCompatibilityImportBlockers enforces agentscompatstore
// as an infrastructure-only persistence detail. Only exact composition edges
// may import it; production consumers receive public Agents commands.
func snapshotPhase5AgentsCompatibilityImportBlockers(root string) ([]string, error) {
	var blockers []string
	internalRoot := filepath.Join(root, "internal")
	err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != internalRoot && excludedWalkDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse Agents compatibility importer %s: %w", path, err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return fmt.Errorf("decode import in %s: %w", relative, err)
			}
			if importPath != phase5AgentsCompatibilityStoreImport {
				continue
			}
			if _, allowed := phase5AgentsCompatibilityCompositions[relative]; allowed {
				continue
			}
			position := fileSet.Position(imported.Pos())
			blockers = append(blockers, fmt.Sprintf("%s:%d", relative, position.Line))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan Agents compatibility imports: %w", err)
	}
	slices.Sort(blockers)
	return blockers, nil
}
