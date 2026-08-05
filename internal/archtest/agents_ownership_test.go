package archtest

import (
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestPhase5AgentsMutationMethodClassificationIsComplete(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := packages.Load(
		&packages.Config{
			Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesSizes,
			Dir:  root,
			Env:  os.Environ(),
		},
		"./internal/store",
		"./internal/modules/agents",
	)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]*packages.Package, len(loaded))
	for _, pkg := range loaded {
		if len(pkg.Errors) != 0 {
			t.Fatalf("load %s: %s", pkg.PkgPath, pkg.Errors[0].Msg)
		}
		byPath[pkg.PkgPath] = pkg
	}

	if len(phase5AgentsReadMethods) != len(phase5AgentsMutationMethods) {
		t.Fatalf(
			"Agents method classification receiver count read=%d mutation=%d",
			len(phase5AgentsReadMethods),
			len(phase5AgentsMutationMethods),
		)
	}
	for receiver, mutations := range phase5AgentsMutationMethods {
		reads, ok := phase5AgentsReadMethods[receiver]
		if !ok {
			t.Fatalf("receiver %s has no explicit read-method classification", receiver)
		}
		classified := maps.Clone(mutations)
		for method := range reads {
			if _, mutating := classified[method]; mutating {
				t.Fatalf("receiver %s method %s is classified as both read and mutation", receiver, method)
			}
			classified[method] = struct{}{}
		}

		separator := strings.LastIndexByte(receiver, '.')
		if separator < 1 || separator == len(receiver)-1 {
			t.Fatalf("invalid receiver identity %q", receiver)
		}
		pkg := byPath[receiver[:separator]]
		if pkg == nil || pkg.Types == nil {
			t.Fatalf("receiver package %s was not loaded", receiver[:separator])
		}
		object := pkg.Types.Scope().Lookup(receiver[separator+1:])
		if object == nil {
			t.Fatalf("receiver type %s was not found", receiver)
		}
		named, ok := object.Type().(*types.Named)
		if !ok {
			t.Fatalf("receiver %s has type %T, want named interface", receiver, object.Type())
		}
		iface, ok := named.Underlying().(*types.Interface)
		if !ok {
			t.Fatalf("receiver %s underlying type is %T, want interface", receiver, named.Underlying())
		}
		iface.Complete()
		actual := make(map[string]struct{}, iface.NumMethods())
		for index := 0; index < iface.NumMethods(); index++ {
			actual[iface.Method(index).Name()] = struct{}{}
		}
		if !maps.Equal(actual, classified) {
			t.Fatalf(
				"receiver %s method classification actual=%v classified=%v",
				receiver,
				slices.Sorted(maps.Keys(actual)),
				slices.Sorted(maps.Keys(classified)),
			)
		}
	}
}

// TestPhase5AgentsOwnershipBlockerRatchet type-checks every production package
// below internal that can reference a legacy Agent/AgentService/Role mutation
// or an Agents-owned persistence mutation. The focused
// scan is independent of the generic direct-write inventory: the latter keeps
// its repository-wide default-deny contract while this ratchet permits only
// the Agents owner core, its persistence adapters, and transparent tracing
// decorators.
func TestPhase5AgentsOwnershipBlockerRatchet(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	mutations, err := snapshotPhase5AgentsMutations(root, matrix)
	if err != nil {
		t.Fatal(err)
	}

	var blockers []string
	for _, mutation := range mutations {
		if isPhase5AgentsMutationAllowed(mutation) {
			continue
		}
		blockers = append(blockers, mutation.withProfiles())
	}
	slices.Sort(blockers)
	if len(blockers) != 0 {
		t.Fatalf(
			"Phase 5 Agents ownership has persistence mutation blockers outside owner commands: %v",
			blockers,
		)
	}
}

func TestPhase5AgentsCompatibilityStoreHasSingleCompositionImporter(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	blockers, err := snapshotPhase5AgentsCompatibilityImportBlockers(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 {
		t.Fatalf(
			"Agents compatibility store imported outside %s: %v",
			"the exact Agents compatibility composition edges",
			blockers,
		)
	}
}

func TestPhase5AgentsCompatibilityStoreImportRatchetRejectsProductionBypass(t *testing.T) {
	root := t.TempDir()
	writePhase5AgentsOwnershipFixture(t, root, "internal/cli/serve/workspacemgr/agents_bootstrap.go", `package workspacemgr
import _ "github.com/tysonthomas9/loomcli/internal/infra/agentsbootstrapstore"
`)
	writePhase5AgentsOwnershipFixture(t, root, "internal/webui/bypass.go", `package webui
import _ "github.com/tysonthomas9/loomcli/internal/infra/agentsbootstrapstore"
`)
	writePhase5AgentsOwnershipFixture(t, root, "internal/webui/bypass_test.go", `package webui
import _ "github.com/tysonthomas9/loomcli/internal/infra/agentsbootstrapstore"
`)
	blockers, err := snapshotPhase5AgentsCompatibilityImportBlockers(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 1 || !strings.HasPrefix(blockers[0], "internal/webui/bypass.go:") {
		t.Fatalf("compatstore import blockers = %v, want production bypass only", blockers)
	}
}

func TestCollectPhase5AgentsMutationPackageUsesDeclaringInterfaceType(t *testing.T) {
	root := t.TempDir()
	writePhase5AgentsOwnershipFixture(t, root, "go.mod", "module github.com/tysonthomas9/loomcli\n\ngo 1.24\n")
	writePhase5AgentsOwnershipFixture(t, root, "internal/store/store.go", `package store
type RoleStore interface {
	Create()
	Get()
}
type AgentServiceStore interface {
	Update()
	List()
}
`)
	writePhase5AgentsOwnershipFixture(t, root, "internal/modules/agents/ports.go", `package agents
type RoleStore interface {
	DeleteRole()
	GetRole()
}
type RolePromptRepairStore interface {
	SetPromptFileIfEmpty()
}
type AgentIdentityStore interface {
	ArchiveAgent()
}
type DesiredStateStore interface {
	SetDesiredStateOwned()
}
type LifecycleStore interface {
	ApplyLifecycle()
}
type OwnershipStore interface {
	ReleaseOwnership()
	GetOwnership()
}
`)
	writePhase5AgentsOwnershipFixture(t, root, "internal/sample/sample.go", `package sample
import (
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
)
func mutate(
	legacyRole store.RoleStore,
	legacyAgent store.AgentServiceStore,
	role agents.RoleStore,
	prompt agents.RolePromptRepairStore,
	identity agents.AgentIdentityStore,
	desired agents.DesiredStateStore,
	lifecycle agents.LifecycleStore,
	ownership agents.OwnershipStore,
) {
	_ = legacyRole.Create
	legacyRole.Get()
	legacyAgent.Update()
	legacyAgent.List()
	role.DeleteRole()
	role.GetRole()
	prompt.SetPromptFileIfEmpty()
	identity.ArchiveAgent()
	desired.SetDesiredStateOwned()
	lifecycle.ApplyLifecycle()
	ownership.ReleaseOwnership()
	ownership.GetOwnership()
}
`)
	loaded, err := packages.Load(
		&packages.Config{
			Mode: directWritePackageLoadMode,
			Dir:  root,
			Env:  os.Environ(),
		},
		"./internal/sample",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Errors) != 0 {
		t.Fatalf("load fixture = %#v", loaded)
	}
	mutations := collectPhase5AgentsMutationPackage(root, loaded[0])
	if len(mutations) != 8 {
		t.Fatalf("mutations = %#v, want one mutation per receiver family", mutations)
	}
	got := make(map[string]string, len(mutations))
	for _, mutation := range mutations {
		got[mutation.receiver] = mutation.method
	}
	want := map[string]string{
		legacyRoleStoreReceiver:          "Create",
		legacyAgentServiceStoreReceiver:  "Update",
		agentsRoleStoreReceiver:          "DeleteRole",
		agentsRolePromptStoreReceiver:    "SetPromptFileIfEmpty",
		agentsAgentIdentityStoreReceiver: "ArchiveAgent",
		agentsDesiredStateStoreReceiver:  "SetDesiredStateOwned",
		agentsLifecycleStoreReceiver:     "ApplyLifecycle",
		agentsOwnershipStoreReceiver:     "ReleaseOwnership",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("mutations = %#v, want %#v", got, want)
	}
}

func TestPhase5AgentsMutationAllowlistIsReceiverAndPathSpecific(t *testing.T) {
	families := []struct {
		name     string
		receiver string
		method   string
		file     string
	}{
		{
			name:     "owned roles",
			receiver: agentsRoleStoreReceiver,
			method:   "UpdateRole",
			file:     "internal/modules/agents/role_management.go",
		},
		{
			name:     "owned role prompt repair",
			receiver: agentsRolePromptStoreReceiver,
			method:   "SetPromptFileIfEmpty",
			file:     "internal/infra/agentsbootstrapstore/adapter.go",
		},
		{
			name:     "owned identity",
			receiver: agentsAgentIdentityStoreReceiver,
			method:   "UpdateAgent",
			file:     "internal/modules/agents/service.go",
		},
		{
			name:     "owned desired state",
			receiver: agentsDesiredStateStoreReceiver,
			method:   "SetDesiredStateOwned",
			file:     "internal/modules/agents/service.go",
		},
		{
			name:     "owned lifecycle",
			receiver: agentsLifecycleStoreReceiver,
			method:   "ApplyLifecycle",
			file:     "internal/modules/agents/service.go",
		},
		{
			name:     "owned ownership",
			receiver: agentsOwnershipStoreReceiver,
			method:   "RenewOwnership",
			file:     "internal/modules/agents/service.go",
		},
		{
			name:     "bootstrap agent service adapter",
			receiver: legacyAgentServiceStoreReceiver,
			method:   "Create",
			file:     "internal/infra/agentsbootstrapstore/adapter.go",
		},
		{
			name:     "legacy role tracing adapter",
			receiver: legacyRoleStoreReceiver,
			method:   "Update",
			file:     "internal/cli/cmdstore/store_tracing_core_entities.go",
		},
	}
	for _, family := range families {
		t.Run(family.name, func(t *testing.T) {
			allowed := phase5AgentsMutation{phase5AgentsMutationIdentity: phase5AgentsMutationIdentity{
				file: family.file, receiver: family.receiver, method: family.method,
			}}
			if !isPhase5AgentsMutationAllowed(allowed) {
				t.Fatalf("allowed owner/adaptor placement rejected: %#v", allowed)
			}
			outsideOwner := phase5AgentsMutation{phase5AgentsMutationIdentity: phase5AgentsMutationIdentity{
				file:     "internal/webui/handlers/agents/bypass.go",
				receiver: family.receiver,
				method:   family.method,
			}}
			if isPhase5AgentsMutationAllowed(outsideOwner) {
				t.Fatalf("outside-owner placement allowed: %#v", outsideOwner)
			}
		})
	}
}

func writePhase5AgentsOwnershipFixture(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
