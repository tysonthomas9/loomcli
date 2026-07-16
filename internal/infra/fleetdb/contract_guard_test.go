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
//     the package's service-client and direct Client do/doWithHeaders/doRaw/
//     doBytes call sites and
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
// do/doWithHeaders/doRaw/doBytes call sites in this package's non-test sources.
// When you add/remove/move a client call, update clientRoutes below FIRST, then
// bump this constant.
const expectedClientCallSites = 150

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

	// role.go
	{"POST", "/api/v1/{}/roles"},
	{"GET", "/api/v1/{}/roles/{}"},
	{"GET", "/api/v1/{}/roles"},
	{"PATCH", "/api/v1/{}/roles/{}"},
	{"DELETE", "/api/v1/{}/roles/{}"},

	// agent.go
	{"POST", "/api/v1/{}/agents"},
	{"GET", "/api/v1/{}/agents/{}"},
	{"GET", "/api/v1/{}/agents"},
	{"PATCH", "/api/v1/{}/agents/{}"},
	{"DELETE", "/api/v1/{}/agents/{}"},

	// daemon.go
	{"GET", "/api/v1/{}/daemon"},
	{"PUT", "/api/v1/{}/daemon"},

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
	{"DELETE", "/api/v1/{}/agent-services/{}"},

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

	// control_plane.go — nodes, sessions, workers, terminals, artifacts,
	// leases, commands, inbox.
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
	{"PATCH", "/api/v1/{}/artifacts/{}"},
	{"POST", "/api/v1/{}/agent-sessions/{}/leases"},
	{"GET", "/api/v1/{}/agent-leases/{}"},
	{"GET", "/api/v1/{}/agent-leases"},
	{"POST", "/api/v1/{}/agent-leases/{}/heartbeat"},
	{"POST", "/api/v1/{}/agent-leases/{}/release"},
	{"POST", "/api/v1/{}/agent-ownership-leases/{}/acquire"},
	{"GET", "/api/v1/{}/agent-ownership-leases/{}"},
	{"GET", "/api/v1/{}/agent-ownership-leases"},
	{"POST", "/api/v1/{}/agent-ownership-leases/{}/heartbeat"},
	{"POST", "/api/v1/{}/agent-ownership-leases/{}/release"},
	{"POST", "/api/v1/{}/agent-commands"},
	{"GET", "/api/v1/{}/agent-commands/{}"},
	{"GET", "/api/v1/{}/agent-commands"},
	{"POST", "/api/v1/{}/agent-commands/{}/ack"},
	{"POST", "/api/v1/{}/agent-commands/{}/complete"},
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

	{"POST", "/api/v1/{}/trigger-bindings"},
	{"GET", "/api/v1/{}/trigger-bindings/{}"},
	{"GET", "/api/v1/{}/trigger-bindings"},
	{"PATCH", "/api/v1/{}/trigger-bindings/{}"},
	// The DELETE whose absence from spec+server this guard exists to catch.
	{"DELETE", "/api/v1/{}/trigger-bindings/{}"},
	{"GET", "/api/v1/{}/trigger-bindings/{}/webhook-secret"},
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
	{"GET", "/api/v1/{}/trigger-events/{}"},
	{"GET", "/api/v1/{}/trigger-events"},
	{"GET", "/api/v1/{}/trigger-deliveries/{}"},
	{"GET", "/api/v1/{}/trigger-deliveries"},

	// platform_await.go
	{"POST", "/api/v1/{}/awaits/register-and-check"},
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
// honest: it counts both service-client calls (s.client.do) and direct Client
// receiver calls (c.do) in this package's non-test sources. client.go is
// excluded from the direct-receiver expression because Client.do delegates to
// Client.doWithHeaders there; that internal transport hop is not an API route.
// A count drift therefore forces the route table to be revisited.
func TestFleetDBClientCallSiteCount(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package sources: %v", err)
	}
	serviceCallRe := regexp.MustCompile(`\.client\.(do|doWithHeaders|doRaw|doBytes)\(ctx`)
	directClientCallRe := regexp.MustCompile(`\bc\.(do|doWithHeaders|doRaw|doBytes)\(ctx`)
	total := 0
	perFile := make([]string, 0, len(files))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		n := len(serviceCallRe.FindAll(src, -1))
		if f != "client.go" {
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
