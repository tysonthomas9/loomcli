package fleetdb

// Contract guard: every method+path this handwritten client issues must exist
// in fleet-db's OpenAPI spec. This is the test that would have caught the
// trigger-binding DELETE gap (client issued DELETE, server registered no
// DELETE route and the spec had no delete operation → 405 dressed as 409).
//
// Mechanism (deliberately minimal and honest):
//
//  1. clientRoutes is a HAND-CURATED table of every route the client calls.
//     A table was chosen over generation because the client builds paths by
//     string concatenation through local variables, helper funcs
//     (awaitsPath, driverStepListQuery) and conditionals (resolveRoute →
//     /resolve vs /resolve-system); associating a method with its resolved
//     template statically would need real data-flow analysis, which is
//     disproportionate for ~130 routes that change rarely.
//  2. The table cannot silently rot: TestFleetDBClientCallSiteCount counts
//     the FleetDB adapter package's service-client and direct Client
//     do/doWithHeaders/doWithHeadersStatus/doRaw/doBytes call sites plus the
//     owner-scoped Requester Do/DoWithHeaders call sites, and
//     fails when the count drifts from expectedClientCallSites, forcing the
//     editor of the client to revisit the table.
//  3. The spec snapshot (testdata/fleetdb-openapi.yaml) cannot silently rot:
//     when the fleet-db repo is checked out side-by-side (the same layout
//     the local-mode compose's `context: ../../../fleet-db` relies on),
//     TestFleetDBSpecSnapshotFresh byte-compares the snapshot against the
//     live spec and fails with a copy instruction on mismatch. Without the
//     sibling checkout the guard still runs against the snapshot.
//
// Paths are normalized on both sides — every {param} segment becomes {} — so
// parameter naming (client "ws" vs spec "workspace") is irrelevant and only
// method + path structure is asserted.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// specSnapshotPath is the vendored fleet-db OpenAPI spec this guard runs
// against; siblingSpecPath is the live spec when the repos are side-by-side.
const (
	specSnapshotPath = "testdata/fleetdb-openapi.yaml"
	siblingSpecPath  = "../../../../fleet-db/api/openapi.yaml"
	companionRepoEnv = "FLEET_DB_REPO"
)

// expectedClientCallSites pins the number of service-client and direct Client
// do/doWithHeaders/doWithHeadersStatus/doRaw/doBytes call sites in this
// package tree's non-test sources.
// When you add/remove/move a client call, update clientRoutes below FIRST, then
// bump this constant.
const expectedClientCallSites = 236

// clientRoute is one method+path template the client issues. Path params are
// written as {} (already normalized).
type clientRoute struct {
	method string
	path   string
}

// clientRoutes enumerates every route the fleet-db client issues, grouped by
// source file. Keep in lockstep with the client (see the call-site tripwire).
var clientRoutes = []clientRoute{
	// capabilities.go
	{"GET", "/api/v1/capabilities"},

	// workspace.go
	{"POST", "/api/v1/admin/workspaces"},
	{"GET", "/api/v1/admin/workspaces/{}"},
	{"GET", "/api/v1/admin/workspaces"},
	{"PATCH", "/api/v1/admin/workspaces/{}"},
	{"DELETE", "/api/v1/admin/workspaces/{}"}, // issued with ?force=true

	// repo.go
	{"POST", "/api/v1/{}/repos"},
	{"GET", "/api/v1/{}/repos/{}"},
	{"GET", "/api/v1/{}/repos"},
	{"PATCH", "/api/v1/{}/repos/{}"},
	{"DELETE", "/api/v1/{}/repos/{}"},

	// repository_admission.go — durable, incarnation-fenced Workspace/Repo
	// admission and exact-owner recovery.
	{"POST", "/api/v1/admin/workspace-repository-admissions"},
	{"POST", "/api/v1/{}/repository-admissions"},
	{"GET", "/api/v1/{}/repository-admissions/recoverable"},
	{"GET", "/api/v1/{}/repository-admissions/operations/{}"},
	{"GET", "/api/v1/{}/repository-admissions/{}"},
	{"POST", "/api/v1/{}/repository-admissions/{}/renew"},
	{"POST", "/api/v1/{}/repository-admissions/{}/claim-recovery"},
	{"POST", "/api/v1/{}/repository-admissions/{}/commit"},
	{"POST", "/api/v1/{}/repository-admissions/{}/fail"},
	{"POST", "/api/v1/{}/repository-admissions/{}/abort"},

	// role.go
	{"POST", "/api/v1/{}/roles"},
	{"GET", "/api/v1/{}/roles/{}"},
	{"GET", "/api/v1/{}/roles"},
	{"PATCH", "/api/v1/{}/roles/{}"},
	{"DELETE", "/api/v1/{}/roles/{}"},
	{"PATCH", "/api/v1/{}/roles/{}/definition"},
	{"POST", "/api/v1/{}/roles/{}/delete"},

	// worker_profile.go
	{"POST", "/api/v1/{}/worker-profiles"},
	{"GET", "/api/v1/{}/worker-profiles/{}"},
	{"GET", "/api/v1/{}/worker-profiles"},
	{"PATCH", "/api/v1/{}/worker-profiles/{}"},
	{"DELETE", "/api/v1/{}/worker-profiles/{}"},

	// agent_service.go
	{"POST", "/api/v1/{}/agent-services"},
	{"GET", "/api/v1/{}/agent-services/{}"},
	{"GET", "/api/v1/{}/agent-services"},
	{"PATCH", "/api/v1/{}/agent-services/{}"},
	{"POST", "/api/v1/{}/agent-services/{}/archive"},
	{"POST", "/api/v1/{}/agent-services/{}/desired-state"},
	{"POST", "/api/v1/{}/agent-services/{}/desired-state/owned"},
	{"POST", "/api/v1/{}/agent-services/{}/lifecycle"},

	// agent_provisioning.go
	{"POST", "/api/v1/{}/agent-provisioning"},
	{"GET", "/api/v1/{}/agent-provisioning/{}"},
	{"GET", "/api/v1/{}/agent-provisioning/pending"},
	{"POST", "/api/v1/{}/agent-provisioning/{}/progress"},

	// connector.go
	{"POST", "/api/v1/{}/connectors"},
	{"GET", "/api/v1/{}/connectors/{}"},
	{"GET", "/api/v1/{}/connectors"},
	{"GET", "/api/v1/{}/connectors/{}/secrets"},
	{"POST", "/api/v1/{}/connectors/{}/rotate"},
	{"POST", "/api/v1/{}/connector-grants"},
	{"POST", "/api/v1/{}/connector-grants/{}/revoke"},
	{"GET", "/api/v1/{}/connector-grants"},
	{"POST", "/api/v1/{}/connector-audit"},
	{"GET", "/api/v1/{}/connector-audit"},
	// connector_grant_commands.go reuses connector-grant create/list above
	// through the narrow Connectors-owned transport; both call sites count.

	// control_plane.go — nodes, sessions, workers, terminals, leases, commands,
	// inbox. Artifact reads are owned by artifact_queries.go; session and
	// execution mutations use their owner transports below.
	{"POST", "/api/v1/{}/nodes"},
	{"GET", "/api/v1/{}/nodes/{}"},
	{"GET", "/api/v1/{}/nodes"},
	{"POST", "/api/v1/{}/nodes/{}/heartbeat"},
	{"PATCH", "/api/v1/{}/nodes/{}"},
	{"POST", "/api/v1/{}/agent-sessions"},
	{"GET", "/api/v1/{}/agent-sessions/{}"},
	{"GET", "/api/v1/{}/agent-sessions"},
	{"POST", "/api/v1/{}/agent-sessions/{}/heartbeat"},
	{"PATCH", "/api/v1/{}/agent-sessions/{}"},
	{"POST", "/api/v1/{}/workers/{}/heartbeat"},
	{"DELETE", "/api/v1/{}/workers/{}"},
	{"POST", "/api/v1/{}/terminal-sessions"},
	{"GET", "/api/v1/{}/terminal-sessions/{}"},
	{"GET", "/api/v1/{}/terminal-sessions"},
	{"PATCH", "/api/v1/{}/terminal-sessions/{}"},
	{"POST", "/api/v1/{}/artifacts"},
	{"GET", "/api/v1/{}/artifacts/{}"},
	{"GET", "/api/v1/{}/artifacts"},
	{"PUT", "/api/v1/{}/artifacts/{}/content"},
	{"GET", "/api/v1/{}/artifacts/{}/content"},
	{"POST", "/api/v1/{}/artifacts/{}/finalize"},
	// session_artifacts.go reuses create/get/content/finalize above through a
	// narrow session-owned transport; those four call sites are counted by the
	// ratchet without duplicating the unique route table.
	// artifact_commands.go — owner-fenced lifecycle and scoped queries.
	{"POST", "/api/v1/{}/artifact-commands/create"},
	{"POST", "/api/v1/{}/artifacts/{}/commands/upload"},
	{"POST", "/api/v1/{}/artifacts/{}/commands/finalize"},
	{"POST", "/api/v1/{}/artifacts/{}/commands/reference"},
	{"GET", "/api/v1/{}/task-runs/{}/artifacts/{}"},
	{"GET", "/api/v1/{}/task-runs/{}/artifacts"},
	{"POST", "/api/v1/{}/agent-sessions/{}/leases"},
	{"GET", "/api/v1/{}/agent-leases/{}"},
	{"GET", "/api/v1/{}/agent-leases"},
	{"POST", "/api/v1/{}/agent-leases/{}/heartbeat"},
	{"POST", "/api/v1/{}/agent-leases/{}/release"},
	// interaction.go — raw session credential validation.
	{"POST", "/api/v1/{}/agent-session-authority/validate"},
	// interaction_commands.go — atomic Interaction lifecycle, terminal,
	// inbox, recovery, and combined activity operations.
	{"POST", "/api/v1/{}/interaction/sessions"},
	{"GET", "/api/v1/{}/interaction/sessions/{}"},
	{"PATCH", "/api/v1/{}/interaction/sessions/{}"},
	{"GET", "/api/v1/{}/interaction/sessions/recoverable"},
	{"POST", "/api/v1/{}/interaction/sessions/{}/recover-start"},
	{"POST", "/api/v1/{}/interaction/sessions/{}/heartbeat"},
	{"POST", "/api/v1/{}/interaction/sessions/{}/finish"},
	{"POST", "/api/v1/{}/interaction/sessions/{}/force-interrupt"},
	{"POST", "/api/v1/{}/interaction/sessions/{}/interrupt-if-lease-missing"},
	{"POST", "/api/v1/{}/interaction/terminals"},
	{"GET", "/api/v1/{}/interaction/terminals/{}"},
	{"PATCH", "/api/v1/{}/interaction/terminals/{}"},
	{"POST", "/api/v1/{}/interaction/inbox"},
	{"POST", "/api/v1/{}/interaction/inbox/claim-next"},
	{"POST", "/api/v1/{}/interaction/inbox/{}/complete"},
	{"GET", "/api/v1/{}/interaction/activity"},
	{"GET", "/api/v1/{}/interaction/activity/sessions"},
	{"GET", "/api/v1/{}/interaction/activity/execution"},
	{"POST", "/api/v1/{}/agent-ownership-leases/{}/acquire"},
	{"GET", "/api/v1/{}/agent-ownership-leases/{}"},
	{"GET", "/api/v1/{}/agent-ownership-leases"},
	{"POST", "/api/v1/{}/agent-ownership-leases/{}/heartbeat"},
	{"POST", "/api/v1/{}/agent-ownership-leases/{}/release"},
	{"POST", "/api/v1/{}/agent-inbox-messages"},
	{"GET", "/api/v1/{}/agent-inbox-messages/{}"},
	{"GET", "/api/v1/{}/agent-inbox-messages"},
	{"POST", "/api/v1/{}/agent-inbox-messages/claim-next"},
	{"POST", "/api/v1/{}/agent-inbox-messages/{}/complete"},

	// journal_events.go
	{"GET", "/api/v1/{}/events/mutations"},

	// platform.go — drivers, versions, trigger bindings, driver runs, steps,
	// task runs, trigger events/deliveries reads.
	{"POST", "/api/v1/{}/drivers"},
	{"GET", "/api/v1/{}/drivers/{}"},
	{"GET", "/api/v1/{}/drivers"},
	{"PATCH", "/api/v1/{}/drivers/{}"},
	{"POST", "/api/v1/{}/drivers/{}/versions"},
	{"GET", "/api/v1/{}/drivers/{}/versions"},
	{"GET", "/api/v1/{}/driver-versions/{}"},
	{"GET", "/api/v1/{}/driver-versions"},

	// workflow_catalog.go — atomic owner-scoped version lifecycle commands.
	{"POST", "/api/v1/{}/drivers/{}/versions/{}/approve"},
	{"POST", "/api/v1/{}/drivers/{}/versions/{}/unapprove"},
	{"POST", "/api/v1/{}/drivers/{}/versions/{}/activate"},
	{"POST", "/api/v1/{}/drivers/{}/versions/author"},
	{"POST", "/api/v1/{}/drivers/{}/versions/author-managed"},

	// driver_run_outcome.go — durable terminal-outcome reconciliation.
	{"POST", "/api/v1/{}/driver-run-outcomes/claim"},
	{"POST", "/api/v1/{}/driver-run-outcomes/complete"},
	{"POST", "/api/v1/{}/driver-run-outcomes/retry"},
	{"POST", "/api/v1/{}/driver-run-outcomes/terminal-work/claim"},
	{"POST", "/api/v1/{}/driver-run-outcomes/terminal-work/complete"},
	{"POST", "/api/v1/{}/driver-run-outcomes/terminal-work/retry"},

	// await_event_notification.go — durable generic-event await reconciliation.
	{"POST", "/api/v1/{}/await-event-notifications/claim"},
	{"POST", "/api/v1/{}/await-event-notifications/complete"},
	{"POST", "/api/v1/{}/await-event-notifications/retry"},

	// automation.go — deterministic matching, atomic admission, retry claim,
	// authoritative dispatch, and non-dispatch delivery transition.
	// Binding/Event/Delivery CRUD reads reuse compatibility routes below.
	{"GET", "/api/v1/{}/automation/binding-matches/{}"},
	{"POST", "/api/v1/{}/automation/admissions/external/{}"},
	{"POST", "/api/v1/{}/automation/admissions/system/{}"},
	{"POST", "/api/v1/{}/driver-runs/{}/automation/admissions/{}"},
	{"POST", "/api/v1/{}/automation/deliveries/claim-due"},
	{"POST", "/api/v1/{}/automation/cron/claim-due"},
	{"POST", "/api/v1/{}/automation/cron/{}/complete"},
	{"POST", "/api/v1/{}/automation/bindings/{}/dispatch"},
	{"POST", "/api/v1/{}/automation/deliveries/{}/dispatch"},
	{"POST", "/api/v1/{}/automation/deliveries/{}/transition"},

	{"POST", "/api/v1/{}/trigger-bindings"},
	{"GET", "/api/v1/{}/trigger-bindings/{}"},
	{"GET", "/api/v1/{}/trigger-bindings"},
	// Shared by the generic binding adapter and Connectors-owned secret lifecycle.
	{"PATCH", "/api/v1/{}/trigger-bindings/{}"},
	// The DELETE whose absence from spec+server this guard exists to catch.
	{"DELETE", "/api/v1/{}/trigger-bindings/{}"},
	{"POST", "/api/v1/{}/driver-runs"},
	{"POST", "/api/v1/{}/epics/{}/runs"},
	{"GET", "/api/v1/{}/driver-runs/{}"},
	{"GET", "/api/v1/{}/driver-runs/{}/events"},
	{"GET", "/api/v1/{}/driver-runs"},
	{"POST", "/api/v1/{}/driver-runs/{}/claim"},
	{"POST", "/api/v1/{}/driver-runs/{}/heartbeat"},
	{"POST", "/api/v1/{}/driver-runs/{}/finish"},
	{"POST", "/api/v1/{}/driver-runs/recover-stale"},
	{"POST", "/api/v1/{}/driver-runs/{}/recover-stale-tasks"},
	// execution.go — atomic Phase-4 intent routes not represented by legacy
	// CRUD-shaped client methods above.
	{"POST", "/api/v1/{}/driver-runs/{}/task-runs/request"},
	{"POST", "/api/v1/{}/driver-runs/{}/work-items/{}/claim"},
	{"POST", "/api/v1/{}/driver-runs/{}/work-items/{}/release"},
	{"POST", "/api/v1/{}/driver-runs/{}/work-items/{}/review-handoff"},
	{"POST", "/api/v1/{}/task-runs/claim-next-and-start"},
	{"POST", "/api/v1/{}/task-runs/{}/claim-and-start"},
	{"POST", "/api/v1/{}/task-runs/{}/requeue-and-reset-step"},
	{"POST", "/api/v1/{}/task-runs/{}/exhaust-retries"},
	{"POST", "/api/v1/{}/task-runs/{}/work-item/design"},
	{"GET", "/api/v1/{}/task-runs/terminal-convergence-candidates"},
	{"POST", "/api/v1/{}/task-runs/{}/complete-terminal-convergence"},
	{"POST", "/api/v1/{}/driver-steps/{}/repair-terminal"},
	{"POST", "/api/v1/{}/driver-runs/{}/children/start"},
	{"POST", "/api/v1/{}/driver-runs/{}/commands/cascade-children"},
	{"POST", "/api/v1/{}/driver-runs/{}/commands/recover-child-cascade"},
	{"POST", "/api/v1/{}/driver-runs/{}/commands/recover-terminal-work"},
	{"POST", "/api/v1/{}/driver-steps"},
	{"POST", "/api/v1/{}/driver-runs/{}/steps"},
	{"GET", "/api/v1/{}/driver-steps/{}"},
	{"GET", "/api/v1/{}/driver-steps"},
	{"GET", "/api/v1/{}/driver-runs/{}/steps"},
	{"PATCH", "/api/v1/{}/driver-steps/{}"},
	{"POST", "/api/v1/{}/task-runs"},
	{"POST", "/api/v1/{}/task-runs/claim"},
	{"GET", "/api/v1/{}/task-runs/{}"},
	{"GET", "/api/v1/{}/task-runs"},
	{"POST", "/api/v1/{}/task-runs/{}/finish"},
	{"POST", "/api/v1/{}/task-runs/{}/heartbeat"},
	{"POST", "/api/v1/{}/task-runs/{}/requeue"},
	{"POST", "/api/v1/{}/task-runs/{}/complete"},
	{"POST", "/api/v1/{}/task-runs/{}/logs"},
	{"GET", "/api/v1/{}/task-runs/{}/logs"},
	{"POST", "/api/v1/{}/trigger-events"},
	{"GET", "/api/v1/{}/trigger-events/{}"},
	{"GET", "/api/v1/{}/trigger-events"},
	{"GET", "/api/v1/{}/trigger-deliveries/{}"},
	{"GET", "/api/v1/{}/trigger-deliveries"},

	// platform_await.go
	{"POST", "/api/v1/{}/awaits/register-and-check"},
	{"POST", "/api/v1/{}/awaits/resolve-and-resume"},
	{"POST", "/api/v1/{}/awaits/resolve-run-outcome"},
	{"POST", "/api/v1/{}/awaits/{}/resolve"},
	{"POST", "/api/v1/{}/awaits/{}/resolve-system"},
	{"GET", "/api/v1/{}/awaits"},
	{"GET", "/api/v1/{}/awaits/due"},
	{"GET", "/api/v1/{}/awaits/{}/satisfied"},
	{"POST", "/api/v1/{}/driver-runs/{}/suspend"},
	{"POST", "/api/v1/{}/driver-runs/{}/resume"},

	// platform_outbox.go
	{"POST", "/api/v1/{}/task-run-events"},
	{"GET", "/api/v1/{}/task-run-events"},
	{"POST", "/api/v1/{}/outbox"},
	{"GET", "/api/v1/{}/outbox/due"},
	{"POST", "/api/v1/{}/outbox/{}/result"},
	{"GET", "/api/v1/{}/outbox/{}"},

	// platform_trigger_retry.go
	{"GET", "/api/v1/{}/trigger-deliveries/due"},
	{"POST", "/api/v1/{}/trigger-deliveries/{}/result"},

	// trigger_route.go
	{"POST", "/api/v1/{}/trigger-routes/{}"},
}

var pathParamRe = regexp.MustCompile(`\{[^}]*\}`)

// normalizePathTemplate rewrites every {param} segment to {} so client and
// spec templates compare structurally regardless of parameter names.
func normalizePathTemplate(p string) string {
	return pathParamRe.ReplaceAllString(p, "{}")
}

// loadSpecOperations parses the vendored spec and returns the set of
// "METHOD normalized-path" operations it declares.
func loadSpecOperations(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(specSnapshotPath)
	if err != nil {
		t.Fatalf("read spec snapshot: %v (vendor it with: cp ../fleet-db/api/openapi.yaml %s)", err, specSnapshotPath)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec snapshot: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatalf("spec snapshot has no paths — wrong or truncated file at %s", specSnapshotPath)
	}
	httpMethods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true}
	ops := make(map[string]bool)
	for path, item := range doc.Paths {
		for key := range item {
			if httpMethods[strings.ToLower(key)] {
				ops[strings.ToUpper(key)+" "+normalizePathTemplate(path)] = true
			}
		}
	}
	return ops
}

// TestFleetDBClientRoutesExistInSpec asserts every method+path the client
// issues is declared in fleet-db's OpenAPI spec.
func TestFleetDBClientRoutesExistInSpec(t *testing.T) {
	ops := loadSpecOperations(t)

	var missing []string
	seen := make(map[string]bool, len(clientRoutes))
	for _, r := range clientRoutes {
		key := r.method + " " + normalizePathTemplate(r.path)
		if seen[key] {
			t.Errorf("duplicate clientRoutes entry: %s", key)
			continue
		}
		seen[key] = true
		if !ops[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("client issues %d operation(s) missing from fleet-db's OpenAPI spec:\n  %s\n"+
			"Either the server/spec lacks the operation (fix fleet-db: route in internal/api + api/openapi.yaml) "+
			"or the client calls a route that no longer exists.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestFleetDBSpecSnapshotFresh fails loudly when the vendored snapshot has
// drifted from the live spec in a side-by-side fleet-db checkout. Without the
// sibling checkout it logs and passes: the routes test above still runs
// against the snapshot.
func TestFleetDBSpecSnapshotFresh(t *testing.T) {
	liveSpecPath := siblingSpecPath
	if companionRepo := strings.TrimSpace(os.Getenv(companionRepoEnv)); companionRepo != "" {
		liveSpecPath = filepath.Join(companionRepo, "api", "openapi.yaml")
	}
	live, err := os.ReadFile(liveSpecPath)
	if err != nil {
		t.Logf("no companion fleet-db checkout at %s (%v); snapshot freshness not verifiable here", liveSpecPath, err)
		return
	}
	snapshot, err := os.ReadFile(specSnapshotPath)
	if err != nil {
		t.Fatalf("read spec snapshot: %v", err)
	}
	if string(live) != string(snapshot) {
		t.Fatalf("vendored fleet-db spec snapshot is STALE relative to %s.\n"+
			"Update it (from the loomcli repo root):\n"+
			"  cp %s internal/infra/fleetdb/testdata/fleetdb-openapi.yaml\n"+
			"then re-run this package's tests so the route guard checks the current spec.",
			liveSpecPath, liveSpecPath)
	}
}

// TestFleetDBClientCallSiteCount is the tripwire that keeps clientRoutes
// honest: it counts root-package service-client calls (s.client.do), direct
// Client receiver calls (c.do), and owner-scoped capability Requester calls
// (s.client.Do) in this package tree's non-test sources. client.go is excluded
// because Client.do delegates to Client.doWithHeaders and capabilityRequester
// delegates back into Client; those internal transport hops are not API routes.
// A count drift therefore forces the route table to be revisited.
func TestFleetDBClientCallSiteCount(t *testing.T) {
	files := make([]string, 0, expectedClientCallSites)
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk package sources: %v", err)
	}
	// Accept gofmt's single-line and multiline call forms. Requiring `(ctx`
	// here silently omitted every call whose context argument started on the
	// next line, including the repository-admission transport.
	serviceCallRe := regexp.MustCompile(`(?s)\.client\.(do|doWithHeaders|doWithHeadersStatus|doRaw|doBytes)\(\s*ctx`)
	directClientCallRe := regexp.MustCompile(`(?s)\bc\.(do|doWithHeaders|doWithHeadersStatus|doRaw|doBytes)\(\s*ctx`)
	capabilityCallRe := regexp.MustCompile(`(?s)\.client\.(Do|DoWithHeaders)\(\s*ctx`)
	total := 0
	perFile := make([]string, 0, len(files))
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		n := len(capabilityCallRe.FindAll(src, -1))
		if filepath.Clean(f) != "client.go" {
			n += len(serviceCallRe.FindAll(src, -1))
			n += len(directClientCallRe.FindAll(src, -1))
		}
		if n > 0 {
			total += n
			perFile = append(perFile, fmt.Sprintf("%s: %d", f, n))
		}
	}
	if total != expectedClientCallSites {
		sort.Strings(perFile)
		t.Fatalf("fleet-db client call sites = %d, want %d.\nPer file:\n  %s\n"+
			"A client call was added, removed, or moved: update clientRoutes in contract_guard_test.go "+
			"to match the client's routes, then set expectedClientCallSites = %d.",
			total, expectedClientCallSites, strings.Join(perFile, "\n  "), total)
	}
}
