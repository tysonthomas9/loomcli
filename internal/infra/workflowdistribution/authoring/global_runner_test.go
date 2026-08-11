package authoring

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

// builtinWorkflowsDeclaringRunner is the pure catalog lookup that drives GAP A:
// which builtin workflows bundle a given task runner. local-task-runner is
// bundled by bug-fix-agent, epic-runner and prompt-agent (sorted); the
// deny-listed openshell-task-runner is never a derived runner, so it has no
// declaring builtins.
func TestBuiltinWorkflowsDeclaringRunner(t *testing.T) {
	if got := strings.Join(builtinWorkflowsDeclaringRunner("local-task-runner"), ","); got != "bug-fix-agent,epic-runner,prompt-agent" {
		t.Fatalf("local-task-runner owners = %q, want bug-fix-agent,epic-runner,prompt-agent", got)
	}
	if got := strings.Join(builtinWorkflowsDeclaringRunner("github-review-task-runner"), ","); got != "github-review-agent,local-review-agent,review-loop-agent" {
		t.Fatalf("github-review-task-runner owners = %q, want github-review-agent,local-review-agent,review-loop-agent", got)
	}
	if got := builtinWorkflowsDeclaringRunner("no-such-runner"); len(got) != 0 {
		t.Fatalf("unknown runner owners = %v, want none", got)
	}
	// openshell-task-runner is deny-listed from derived runner specs (§4.6), so
	// it is never globally resolvable even though its source ships in a bundle.
	if got := builtinWorkflowsDeclaringRunner(driver.OpenShellRunnerName); len(got) != 0 {
		t.Fatalf("openshell owners = %v, want none (deny-listed)", got)
	}
}

func builtinWorkflowsDeclaringRunner(runnerName string) []string {
	support := builtinSupport{}
	names := []string{}
	for _, name := range support.BuiltinNames() {
		spec, ok := support.Builtin(name)
		if !ok {
			continue
		}
		for _, runner := range spec.Runners {
			if runner.Name == runnerName {
				names = append(names, name)
				break
			}
		}
	}
	return names
}

func activeTrustedBuiltinRunner(
	ctx context.Context,
	catalog workflowcatalog.API,
	workspace,
	workflowName,
	runnerName string,
) (*driver.GlobalRunnerResolution, error) {
	result, err := appworkflowauthoring.ResolveActiveBuiltinRunner(
		ctx, catalog, NewBuiltinSupport(), workspace, workflowName, runnerName,
	)
	if errors.Is(err, workflowcatalog.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if errors.Is(err, workflowcatalog.ErrInvalid) {
		return nil, domain.ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	return &driver.GlobalRunnerResolution{
		Driver: result.Driver, Version: result.Version,
		Spec: driver.DriverRunnerSpec{Name: result.Spec.Name, Kind: result.Spec.Kind, Entrypoint: result.Spec.Entrypoint},
	}, nil
}

// activeTrustedBuiltinRunner is the store-backed trust gate: a runner is only
// globally resolvable when the OWNING builtin's active version is TRUSTED and
// still declares it. This exercises the gate without the flue build path.
func TestActiveTrustedBuiltinRunnerResolvesTrustedOwner(t *testing.T) {
	catalog := runnerOwnerCatalog(t, "WS", "bug-fix-agent", "local-task-runner", workflowcatalog.DriverTrustTrusted)

	res, err := activeTrustedBuiltinRunner(t.Context(), catalog, "WS", "bug-fix-agent", "local-task-runner")
	if err != nil {
		t.Fatalf("activeTrustedBuiltinRunner: %v", err)
	}
	if res.Version.VersionID != "bug-fix-agent-v1" || res.Driver.DriverID != "bug-fix-agent" {
		t.Fatalf("resolved owner = %+v, want bug-fix-agent-v1", res)
	}
	if res.Spec.Name != "local-task-runner" || res.Spec.Kind != driver.RunnerKindFlueWorkflow {
		t.Fatalf("resolved spec = %+v, want local-task-runner flue-workflow", res.Spec)
	}
	if !driver.DriverVersionEffectiveTrust(res.Driver, res.Version).Trusted() {
		t.Fatal("resolved runner owner must be trusted")
	}
}

func TestActiveTrustedBuiltinRunnerRejectsUntrustedOwner(t *testing.T) {
	// An untrusted driver that (illegitimately) declares the runner name must NOT
	// be globally resolvable — an untrusted driver can never export its runners.
	catalog := runnerOwnerCatalog(t, "WS", "bug-fix-agent", "local-task-runner", workflowcatalog.DriverTrustUntrusted)

	if _, err := activeTrustedBuiltinRunner(t.Context(), catalog, "WS", "bug-fix-agent", "local-task-runner"); err == nil {
		t.Fatal("untrusted owner resolved a global runner; must fail closed")
	}
}

func TestActiveTrustedBuiltinRunnerRejectsUndeclaredRunner(t *testing.T) {
	catalog := runnerOwnerCatalog(t, "WS", "bug-fix-agent", "local-task-runner", workflowcatalog.DriverTrustTrusted)

	_, err := activeTrustedBuiltinRunner(t.Context(), catalog, "WS", "bug-fix-agent", "some-other-runner")
	if !errors.Is(err, driver.ErrRunnerNotDeclared) {
		t.Fatalf("undeclared runner err = %v, want ErrRunnerNotDeclared", err)
	}
}

// runnerOwnerCatalog returns a typed Workflow Catalog view whose ACTIVE
// version declares one task runner with the given trust level.
func runnerOwnerCatalog(
	t *testing.T,
	ws, name, runnerName string,
	trust workflowcatalog.DriverTrustLevel,
) builtinCatalogStub {
	t.Helper()
	runnersJSON, err := json.Marshal([]driver.DriverRunnerSpec{{
		Name: runnerName, Kind: driver.RunnerKindFlueWorkflow, Entrypoint: runnerName,
	}})
	if err != nil {
		t.Fatalf("marshal runners: %v", err)
	}
	versionID := name + "-v1"
	return builtinCatalogStub{
		driver: &workflowcatalog.Driver{
			WorkspaceKey: ws, DriverID: name, Name: name,
			OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
			TrustLevel: trust, ActiveVersionID: versionID, Revision: 1,
		},
		version: &workflowcatalog.DriverVersion{
			WorkspaceKey: ws, VersionID: versionID, DriverID: name, Version: 1,
			SourceDigest: "sha256:" + name, BundleDigest: "sha256:" + name,
			Runtime: driver.RuntimeFlueNode,
			Manifest: map[string]string{
				"runners":                    string(runnersJSON),
				driver.ManifestTrustLevelKey: string(trust),
			},
		},
	}
}
