package archtest

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestProductionAwaitDispatchDoesNotFallbackToRawAtomicStore(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "internal", "infra", "automationruntime", "await_matcher.go")
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var rawFallbacks []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		assertion, ok := node.(*ast.TypeAssertExpr)
		if !ok {
			return true
		}
		selector, ok := assertion.Type.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "AtomicAwaitStore" {
			return true
		}
		qualifier, ok := selector.X.(*ast.Ident)
		if !ok || qualifier.Name != "store" {
			return true
		}
		rawFallbacks = append(rawFallbacks, files.Position(assertion.Pos()).String())
		return true
	})
	if len(rawFallbacks) != 0 {
		t.Fatalf("production Await dispatch must use its injected Execution resolver and fail closed when unavailable; raw store.AtomicAwaitStore fallback at %v", rawFallbacks)
	}
}

func TestCheckedInManifestsAndRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	report, err := CheckRepository(root, filepath.Join(root, "internal", "archtest", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(report.CompositeStoreFiles), 14; got != want {
		t.Fatalf("composite Store file count = %d, want %d; files = %v", got, want, report.CompositeStoreFiles)
	}
	if got, want := len(report.CompositeStoreOutside), 0; got != want {
		t.Fatalf("outside-composition Store file count = %d, want %d", got, want)
	}
	if got, want := len(report.LegacyHandlerImports), 0; got != want {
		t.Fatalf("legacy handler imports = %d, want %d", got, want)
	}
	if got, want := report.ModuleRoots, checkedInModuleRoots; !slices.Equal(got, want) {
		t.Fatalf("module roots = %v, want active capability extractions %v", got, want)
	}
	if got, want := len(report.PendingDecisions), 0; got != want {
		t.Fatalf("pending decisions = %d, want %d", got, want)
	}
	if got, want := report.AnalysisProfilesEnforced, 11; got != want {
		t.Fatalf("enforced analysis profiles = %d, want %d", got, want)
	}
	if got, want := report.MutationCommands, 107; got != want {
		t.Fatalf("mutation commands = %d, want %d", got, want)
	}
	if got, want := report.DirectPersistenceWrites, 90; got != want {
		t.Fatalf("direct persistence-write rows = %d, want %d", got, want)
	}
	if got, want := report.RuntimeComponents, 70; got != want {
		t.Fatalf("runtime components = %d, want %d", got, want)
	}
	if got, want := report.RuntimeGoroutineLaunches, 79; got != want {
		t.Fatalf("runtime goroutine launches = %d, want %d", got, want)
	}
	if got, want := report.PerformanceMetrics, 6; got != want {
		t.Fatalf("performance metrics = %d, want %d", got, want)
	}
	if got, want := report.PerformanceMetricsMeasured, 6; got != want {
		t.Fatalf("measured performance metrics = %d, want %d", got, want)
	}
	if got, want := report.PerformanceMetricsDeferred, 0; got != want {
		t.Fatalf("deferred performance metrics = %d, want %d", got, want)
	}
}

func TestBaselineRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	contents := `{"schema_version":1,"analyzer_version":"1.0.0","unknown":true}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict decode error, got %v", err)
	}
}

func TestBaselineRequiresEveryPhase1InventoryComplete(t *testing.T) {
	baseline, err := LoadBaseline(filepath.Join("testdata", "migration-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline.Inventories[0].Status = "deferred"
	if err := baseline.Validate(); err == nil || !strings.Contains(err.Error(), "must be complete") {
		t.Fatalf("Validate error = %v, want incomplete-inventory rejection", err)
	}

	baseline, err = LoadBaseline(filepath.Join("testdata", "migration-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline.Inventories[0].ID = "unexpected-inventory"
	if err := baseline.Validate(); err == nil || !strings.Contains(err.Error(), "missing Phase 1 inventory") {
		t.Fatalf("Validate error = %v, want exact-inventory rejection", err)
	}
}

func TestMutationLedgerRequiresEveryMigratedCommand(t *testing.T) {
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ledger.Commands = ledger.Commands[1:]
	if err := ledger.Validate(); err == nil || !strings.Contains(err.Error(), "missing required migrated command agentprovisioning.begin") {
		t.Fatalf("Validate error = %v, want missing-migrated-command rejection", err)
	}
}

func TestMutationLedgerMatchesProductionExecutionMutationInventory(t *testing.T) {
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, command := range ledger.Commands {
		if strings.HasPrefix(command.ID, "execution.") {
			got = append(got, command.ID)
		}
	}

	// These registered actions do not belong in a mutation ledger. Preflight's
	// port contract explicitly forbids claiming product state, and Classifier
	// only returns an ExitClassification value; neither has a durable command.
	nonMutationEvidence := map[authority.Action]string{
		execution.ActionPreflight:               "PreflightPort validates readiness without claiming product state",
		execution.ActionClassify:                "Classifier returns only an ExitClassification value",
		execution.ActionResolveTrustedRunner:    "TrustedRunnerResolver returns only a validated catalog projection",
		execution.ActionListDueOutboxDeliveries: "Outbox delivery listing is a read-only due-row query",
	}
	// These original generic API actions remain compatibility scaffolds and are
	// not wired by the production Phase 4 composition. Their exact composed
	// replacements are represented by the other registered actions below.
	uncomposedScaffolds := map[authority.Action]string{
		execution.ActionAwait:          "replaced by exact DriverRun await actions",
		execution.ActionClaimAndLaunch: "replaced by exact TaskRun claim and lifecycle actions",
		execution.ActionRecover:        "replaced by exact DriverRun and TaskRun recovery actions",
	}
	exclusions := make(map[authority.Action]string, len(nonMutationEvidence)+len(uncomposedScaffolds))
	for action, evidence := range nonMutationEvidence {
		exclusions[action] = evidence
	}
	for action, evidence := range uncomposedScaffolds {
		exclusions[action] = evidence
	}

	rules := append(execution.OperationRules(), execution.DriverRunOperationRules()...)
	want := make([]string, 0, len(rules)-len(exclusions))
	observedExclusions := make(map[authority.Action]struct{}, len(exclusions))
	for _, rule := range rules {
		if evidence, excluded := exclusions[rule.Action]; excluded {
			observedExclusions[rule.Action] = struct{}{}
			if evidence == "" {
				t.Fatalf("excluded Execution action %q has no evidence", rule.Action)
			}
			if slices.Contains(got, string(rule.Action)) {
				t.Fatalf("non-production-mutation Execution action %q appears in ledger; exclusion evidence: %s", rule.Action, evidence)
			}
			continue
		}
		want = append(want, string(rule.Action))
	}
	if len(observedExclusions) != len(exclusions) {
		t.Fatalf("Execution action exclusions observed = %v, want all documented exclusions %v", observedExclusions, exclusions)
	}
	slices.Sort(want)
	if gotCount, wantCount := len(got), 43; gotCount != wantCount {
		t.Fatalf("Execution mutation commands = %d, want %d; commands = %v", gotCount, wantCount, got)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Execution mutation commands = %v, want exact production action inventory %v", got, want)
	}
}

func TestMutationLedgerMatchesProductionInteractionMutationInventory(t *testing.T) {
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	commands := make(map[string]struct{}, len(ledger.Commands))
	for _, command := range ledger.Commands {
		commands[command.ID] = struct{}{}
	}
	rules := append(interaction.OperationRules(), interaction.ChatOperationRules()...)
	readOnly := map[authority.Action]struct{}{
		interaction.ActionReadActivity:     {},
		interaction.ActionReadConversation: {},
	}
	for _, rule := range rules {
		id := string(rule.Action)
		if _, ok := readOnly[rule.Action]; ok {
			if _, ok := commands[id]; ok {
				t.Fatalf("read-only Interaction action %s must not be a mutation-ledger row", id)
			}
			continue
		}
		if _, ok := commands[id]; !ok {
			t.Fatalf("production Interaction mutation %s is missing from the mutation ledger", id)
		}
	}
}

func TestMutationLedgerMatchesProductionAgentsMutationInventory(t *testing.T) {
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	commands := make(map[string]struct{}, len(ledger.Commands))
	for _, command := range ledger.Commands {
		commands[command.ID] = struct{}{}
	}
	for _, rule := range agents.OperationRules() {
		id := string(rule.Action)
		if _, ok := commands[id]; !ok {
			t.Fatalf("production Agents mutation %s is missing from the mutation ledger", id)
		}
	}
}

func TestExecutionLedgerDurableCommandsExistInFrozenFleetContract(t *testing.T) {
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join("..", "infra", "fleetdb", "testdata", "fleetdb-openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		t.Fatal(err)
	}

	for _, command := range ledger.Commands {
		if !strings.HasPrefix(command.ID, "execution.") {
			continue
		}
		for _, durableCommand := range command.DurableCommands {
			method, route, ok := strings.Cut(durableCommand, " ")
			if !ok || method == "" || route == "" {
				t.Fatalf("%s durable command %q is not an exact Fleet HTTP operation", command.ID, durableCommand)
			}
			operations, ok := contract.Paths[route]
			if !ok {
				t.Fatalf("%s durable command %q has no route in frozen Fleet contract", command.ID, durableCommand)
			}
			if _, ok := operations[strings.ToLower(method)]; !ok {
				t.Fatalf("%s durable command %q has no matching method in frozen Fleet contract", command.ID, durableCommand)
			}
		}
	}
}

func TestExecutionQueueLedgerDistinguishesLeaseRecoveryFromReceiptReplay(t *testing.T) {
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	commands := make(map[string]MutationCommand, len(ledger.Commands))
	for _, command := range ledger.Commands {
		commands[command.ID] = command
	}

	for _, id := range []string{
		"execution.claim-await-event-notifications",
		"execution.claim-driver-run-outcomes",
		"execution.claim-terminal-driver-run-work-recoveries",
	} {
		command := commands[id]
		if !strings.Contains(command.IdempotencyKey, "no exact claim replay receipt") ||
			!strings.Contains(command.RetryRestartBehavior, "before claim_until returns no row") ||
			slices.Contains(command.FaultInjectionTests, "duplicate_claim") {
			t.Fatalf("%s must document lease-expiry recovery, not exact claim replay: %+v", id, command)
		}
	}
	for _, id := range []string{
		"execution.retry-await-event-notification",
		"execution.retry-driver-run-outcome",
		"execution.retry-terminal-driver-run-work-recovery",
	} {
		command := commands[id]
		if !strings.Contains(command.IdempotencyKey, "no exact replay receipt") ||
			!strings.Contains(command.RetryRestartBehavior, "ownership conflict") ||
			slices.Contains(command.FaultInjectionTests, "duplicate_retry") {
			t.Fatalf("%s must document state convergence after a cleared claim, not retry receipt replay: %+v", id, command)
		}
	}
	for _, id := range []string{
		"execution.complete-await-event-notification",
		"execution.complete-driver-run-outcome",
		"execution.complete-terminal-driver-run-work-recovery",
	} {
		command := commands[id]
		if !strings.Contains(command.RetryRestartBehavior, "Exact completion replay is idempotent") {
			t.Fatalf("%s must retain exact same-claim completion replay: %+v", id, command)
		}
	}
}

func TestPhase4LedgerDistinguishesReceiptsFromStateConvergence(t *testing.T) {
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	commands := make(map[string]MutationCommand, len(ledger.Commands))
	for _, command := range ledger.Commands {
		commands[command.ID] = command
	}

	immutableReceiptMarkers := map[string]string{
		"artifacts.declare":                             "an exact finalized digest returns without rewriting",
		"artifacts.finalize":                            "returns an exact finalized match without rewriting",
		"artifacts.reference":                           "immutable receipt",
		"artifacts.upload":                              "a finalized exact match is returned without another upload",
		"execution.append-log":                          "Exact request replay",
		"execution.claim-driver-run":                    "Exact request replay",
		"execution.claim-driver-run-work-item":          "Exact request replay",
		"execution.claim-task-run":                      "same TaskRun owner and action receipt",
		"execution.exhaust-task-run-retries":            "Exact replay returns the committed exhaustion receipt",
		"execution.finalize":                            "original terminal result",
		"execution.finalize-driver-run":                 "Exact request replay",
		"execution.handoff-driver-run-review-work-item": "Exact replay returns the committed handoff receipt",
		"execution.request-task-run":                    "Exact request replay",
		"execution.requeue-task-run":                    "Exact replay returns the committed retry receipt",
		"execution.release-driver-run-work-item":        "Exact request replay",
		"execution.start-child-driver-run":              "Exact replay",
		"execution.submit-driver-run":                   "stable DriverRun identity and exact submitted definition",
	}
	for id, marker := range immutableReceiptMarkers {
		command, ok := commands[id]
		if !ok || !strings.Contains(command.RetryRestartBehavior, marker) {
			t.Fatalf("%s must document immutable receipt replay with %q: %+v", id, marker, command)
		}
	}

	stateConvergenceMarkers := map[string]string{
		"execution.cascade-child-driver-runs":        "current projections",
		"execution.converge-task-run":                "next scan resume",
		"execution.create-worker-profile":            "converges on the stable profile identity",
		"execution.delete-worker-profile":            "converges on absence",
		"execution.heartbeat":                        "converge",
		"execution.heartbeat-driver-run":             "converge on current liveness state",
		"execution.heartbeat-worker-node":            "move liveness forward",
		"execution.recover-child-driver-run-cascade": "current projections",
		"execution.recover-driver-runs":              "current recovered ID set",
		"execution.recover-stale-child-task-runs":    "fresh heartbeat or successor fence wins",
		"execution.register-worker-node":             "updates the same registration",
		"execution.repair-terminal-driver-step":      "converges",
		"execution.set-worker-node-drain":            "converges on the same Node projection",
		"execution.update-worker-profile":            "converges",
	}
	for id, marker := range stateConvergenceMarkers {
		command, ok := commands[id]
		if !ok || !strings.Contains(command.RetryRestartBehavior, marker) {
			t.Fatalf("%s must document live state convergence with %q: %+v", id, marker, command)
		}
	}

	await := commands["execution.await-driver-run"]
	resolve := commands["execution.resolve-driver-await"]
	if !strings.Contains(await.RetryRestartBehavior, "Registration replay") ||
		!strings.Contains(resolve.RetryRestartBehavior, "cannot resume the parent twice") {
		t.Fatalf("Await commands must document identity plus suspend/resume convergence: register=%+v resolve=%+v", await, resolve)
	}
}

func TestPhase5InteractionLedgerPinsDeliveryAuthorityAndAttemptFencing(t *testing.T) {
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	commands := make(map[string]MutationCommand, len(ledger.Commands))
	for _, command := range ledger.Commands {
		commands[command.ID] = command
	}

	enqueue := commands["interaction.enqueue-inbox"]
	if !slices.Equal(enqueue.AcceptedAuthority, []string{"operator", "system"}) ||
		!strings.Contains(enqueue.RetryRestartBehavior, "fresh system authority") ||
		!slices.Contains(enqueue.NegativeTests, "wrong_component") {
		t.Fatalf("Interaction enqueue must pin registered operator/system authority: %+v", enqueue)
	}

	complete := commands["interaction.complete-inbox"]
	for label, value := range map[string]string{
		"boundary":    complete.Boundary,
		"idempotency": complete.IdempotencyKey,
		"retry":       complete.RetryRestartBehavior,
	} {
		if !strings.Contains(value, "attempt") {
			t.Fatalf("Interaction complete %s must bind the exact claim attempt: %+v", label, complete)
		}
	}
	if !strings.Contains(complete.RetryRestartBehavior, "queued preserves the attempt") ||
		!slices.Contains(complete.NegativeTests, "stale_claim") ||
		!slices.Contains(complete.FaultInjectionTests, "reclaim_completion_race") {
		t.Fatalf("Interaction queued retry must reject predecessor completion: %+v", complete)
	}

	patch := commands["interaction.patch-session"]
	if !slices.Equal(patch.AcceptedAuthority, []string{"session"}) ||
		!slices.Equal(patch.DurableCommands, []string{"fleetdb.patch_interaction_session.v1"}) ||
		!slices.Contains(patch.NegativeTests, "forbidden_identity_field") {
		t.Fatalf("Interaction patch must remain owner-fenced and bounded: %+v", patch)
	}

	forceInterrupt := commands["interaction.force-interrupt"]
	for label, value := range map[string]string{
		"boundary":    forceInterrupt.Boundary,
		"idempotency": forceInterrupt.IdempotencyKey,
		"retry":       forceInterrupt.RetryRestartBehavior,
	} {
		if !strings.Contains(value, "lease") ||
			!strings.Contains(value, "fenc") {
			t.Fatalf("Interaction force-interrupt %s must bind the caller-expected lease generation: %+v", label, forceInterrupt)
		}
	}
	if !slices.Contains(forceInterrupt.NegativeTests, "delayed_prior_lease_generation") {
		t.Fatalf("Interaction force-interrupt must reject delayed prior-generation teardown: %+v", forceInterrupt)
	}
}

// TestCheckedInPhase6ArchitectureContracts keeps the completed Phase 6
// contract monotonic after later migration phases advance the checked graph.
// Historical acceptance required equality at Phase 6; current heads must keep
// at least those roots and commands while allowing the Phase 7 ratchet to add
// owners and commands.
func TestCheckedInPhase6ArchitectureContracts(t *testing.T) {
	graph, err := LoadCapabilityGraph(filepath.Join("testdata", "capability-graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if graph.CompletedPhase < 6 {
		t.Fatalf("completed phase = %d, want at least 6", graph.CompletedPhase)
	}
	statusByCapability := make(map[string]string, len(graph.Capabilities))
	for _, capability := range graph.Capabilities {
		statusByCapability[capability.Name] = capability.Status
	}
	for _, capability := range []string{
		"agents",
		"artifacts",
		"automation",
		"connectors",
		"execution",
		"interaction",
		"sourcecontrol",
		"workflowcatalog",
	} {
		if statusByCapability[capability] != "active" {
			t.Fatalf("Phase 6 capability %s status = %q, want active", capability, statusByCapability[capability])
		}
	}
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Commands) < 105 {
		t.Fatalf("mutation commands = %d, want at least the 105 Phase 6 commands", len(ledger.Commands))
	}
}

func TestCheckedInPhase7ArchitectureContracts(t *testing.T) {
	graph, err := LoadCapabilityGraph(filepath.Join("testdata", "capability-graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if graph.CompletedPhase != 7 {
		t.Fatalf("completed phase = %d, want 7", graph.CompletedPhase)
	}
	if len(graph.LegacyPaths) != 0 {
		t.Fatalf("legacy paths remain after Phase 7 completion: %+v", graph.LegacyPaths)
	}
	statusByCapability := make(map[string]string, len(graph.Capabilities))
	for _, capability := range graph.Capabilities {
		statusByCapability[capability.Name] = capability.Status
	}
	for _, capability := range []string{
		"agents",
		"artifacts",
		"automation",
		"connectors",
		"execution",
		"interaction",
		"sourcecontrol",
		"workitems",
		"workflowcatalog",
		"workspace",
	} {
		if statusByCapability[capability] != "active" {
			t.Fatalf("capability %s status = %q, want active", capability, statusByCapability[capability])
		}
	}

	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Commands) != 107 {
		t.Fatalf("mutation commands = %d, want 107", len(ledger.Commands))
	}
}

func TestPhase5InteractionOwnershipBlockerRatchet(t *testing.T) {
	graph, err := LoadCapabilityGraph(filepath.Join("testdata", "capability-graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	// These legacy composite-store calls are semantic writes, not persistence
	// adapters. Target architecture assigns all four aggregates exclusively to
	// Interaction in Phase 5. The retired AgentCommand aggregate is absent.
	mutation := regexp.MustCompile(`\.(AgentSessions|TerminalSessions|AgentLeases|AgentInboxMessages)\(\)\.(Create|Update|Heartbeat|Release|ClaimNext|Complete)\(`)
	counts := map[string]int{}
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/infra/") ||
			strings.HasPrefix(rel, "internal/modules/interaction/") {
			return nil
		}
		contents, err := os.ReadFile(path) // #nosec G122 -- WalkDir is rooted in the checked-out repository and does not follow symlinks.
		if err != nil {
			return err
		}
		for _, match := range mutation.FindAllSubmatch(contents, -1) {
			counts[rel+":"+string(match[1])+"."+string(match[2])]++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var observed []string
	totalBlockers := 0
	for key, count := range counts {
		observed = append(observed, key+"="+strconv.Itoa(count))
		totalBlockers += count
	}
	slices.Sort(observed)
	if len(observed) != 0 {
		t.Fatalf(
			"Phase 5 Interaction ownership has %d direct aggregate mutation blockers outside owner adapters: %v",
			totalBlockers,
			observed,
		)
	}
	if graph.CompletedPhase != 7 {
		t.Fatalf("completed_phase = %d after zero direct Interaction aggregate mutation blockers, want 7", graph.CompletedPhase)
	}
}

func TestRetiredLegacyWorkflowsPathCannotReturn(t *testing.T) {
	graph, err := LoadCapabilityGraph(filepath.Join("testdata", "capability-graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range graph.LegacyPaths {
		if candidate.Path == "internal/workflows" {
			t.Fatalf("retired internal/workflows path remains in capability graph: %+v", candidate)
		}
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retiredRoot := filepath.Join(root, "internal", "workflows")
	if walkErr := filepath.WalkDir(retiredRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			t.Errorf("retired internal/workflows path contains %s", path)
		}
		return nil
	}); walkErr != nil && !os.IsNotExist(walkErr) {
		t.Fatal(walkErr)
	}
	var observed []string
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if path == "github.com/tysonthomas9/loomcli/internal/workflows" {
				observed = append(observed, rel)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(observed)
	if len(observed) != 0 {
		t.Fatalf("retired internal/workflows callers = %v, want none", observed)
	}
}

func TestRetiredHorizontalRootsCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retired := []string{
		"internal/agentinbox",
		"internal/app/agentsbootstrap",
		"internal/app/serve/agentcomposition/owneradapters",
		"internal/authmode",
		"internal/backend/backendtest",
		"internal/backendnames",
		"internal/cli/backendapi",
		"internal/cli/clitest",
		"internal/cli/serve/serveadapter/daytonabroker",
		"internal/cli/serve/workspacemgr/admissionstore",
		"internal/cli/serve/workspacemgr/workspacematerialization",
		"internal/connector",
		"internal/driver/runtypes",
		"internal/driver/taskworktree",
		"internal/infra/agentsbootstrapstore",
		"internal/infra/artifactcatalog",
		"internal/infra/connectorscatalog",
		"internal/infra/sessionstoreadapter",
		"internal/harness/fakeharness/mock",
		"internal/leadcontrol",
		"internal/localnodeconfig",
		"internal/modules/artifacts/fleetdb",
		"internal/modules/connectors/fleetdb",
		"internal/modules/sourcecontrol/stackpublish",
		"internal/pathsec",
		"internal/runtimectx",
		"internal/runtimepreflight",
		"internal/sessions/eventstore",
		"internal/sessions/transcript/backends",
		"internal/sessions/transcript/claude",
		"internal/sessions/transcript/codex",
		"internal/sessions/transcript/opencode",
		"internal/stacklineage",
		"internal/stackpublish",
		"internal/stackstore",
		"internal/store/storetest",
		"internal/trigger",
		"internal/types",
		"internal/webui/service",
		"internal/webui/svcimpl",
		"internal/workspace",
		"internal/workflows",
	}
	for _, relative := range retired {
		relative := relative
		t.Run(strings.ReplaceAll(relative, "/", "_"), func(t *testing.T) {
			retiredRoot := filepath.Join(root, filepath.FromSlash(relative))
			if walkErr := filepath.WalkDir(retiredRoot, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !entry.IsDir() {
					t.Errorf("retired horizontal root contains %s", path)
				}
				return nil
			}); walkErr != nil && !os.IsNotExist(walkErr) {
				t.Fatal(walkErr)
			}

			importPath := "github.com/tysonthomas9/loomcli/" + relative
			var callers []string
			walkErr := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() || !strings.HasSuffix(path, ".go") {
					return nil
				}
				parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
				if parseErr != nil {
					return parseErr
				}
				for _, imported := range parsed.Imports {
					value, unquoteErr := strconv.Unquote(imported.Path.Value)
					if unquoteErr != nil {
						return unquoteErr
					}
					if value == importPath || strings.HasPrefix(value, importPath+"/") {
						rel, relErr := filepath.Rel(root, path)
						if relErr != nil {
							return relErr
						}
						callers = append(callers, filepath.ToSlash(rel))
						break
					}
				}
				return nil
			})
			if walkErr != nil {
				t.Fatal(walkErr)
			}
			slices.Sort(callers)
			if len(callers) != 0 {
				t.Fatalf("retired horizontal root callers = %v, want none", callers)
			}
		})
	}
}

func TestRetiredDriverRunContractBridgeCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "internal", "driver", "executor.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"runtypes.",
		"legacy direct-store compatibility path",
	} {
		if strings.Contains(string(content), forbidden) {
			t.Errorf("retired Driver run-contract compatibility %q returned", forbidden)
		}
	}
}

func TestRetiredAgentsBootstrapCompatibilityCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "internal", "modules", "agents", "bootstrap.go")); !os.IsNotExist(statErr) {
		t.Errorf("retired Agents bootstrap implementation returned: %v", statErr)
	}
	for _, relative := range []string{
		"internal/modules/agents",
		"internal/infra/memstore",
	} {
		files, globErr := filepath.Glob(filepath.Join(root, filepath.FromSlash(relative), "*.go"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			for _, declaration := range parsed.Decls {
				switch value := declaration.(type) {
				case *ast.FuncDecl:
					if value.Name.Name == "NewBootstrapService" || value.Name.Name == "SetPromptFileIfEmpty" {
						t.Errorf("retired Agents bootstrap function %s returned in %s", value.Name.Name, path)
					}
				case *ast.GenDecl:
					for _, specification := range value.Specs {
						typeSpec, ok := specification.(*ast.TypeSpec)
						if !ok {
							continue
						}
						switch typeSpec.Name.Name {
						case "BootstrapAPI", "BootstrapService", "BootstrapStore", "RolePromptRepairStore":
							t.Errorf("retired Agents bootstrap type %s returned in %s", typeSpec.Name.Name, path)
						}
					}
				}
			}
		}
	}
}

func TestRetiredAmbientRuntimeContextCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"cmdstore.RootContext()",
		"runtimectx.RootContext()",
		"runtimectx.SetRootContext(",
		"events.SetContextProvider(",
		"func SetRootContext(",
		"func RootContext()",
		"func SetContextProvider(",
		"ambientCtxProvider",
	}
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "archtest" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(content), fragment) {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("retired ambient runtime-context fallback %q returned in %s", fragment, filepath.ToSlash(relative))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetiredConnectorModelAndRepositoryPlaneCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"internal/domain/connector.go",
		"internal/store/connector_store.go",
		"internal/store/connector_unimplemented.go",
	} {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !os.IsNotExist(statErr) {
			t.Errorf("retired connector plane file %s returned (stat error: %v)", relative, statErr)
		}
	}
}

func TestRetiredConnectorCompatibilityConstructorsCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for relative, forbidden := range map[string]string{
		"internal/modules/connectors/service.go":           "NewWithGrants",
		"internal/app/serve/source_control_composition.go": "NewSourceControlCapability",
		"internal/app/serve/workflow_catalog.go":           "NewSourceControlCapability",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Name.Name == forbidden {
				t.Errorf("retired Connector compatibility constructor %s returned in %s", forbidden, relative)
			}
		}
	}
}

func TestRetiredAutomationCompositionCompatibilityCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"internal/webui/agentmodules/automation_agent_identity_compat.go",
		"internal/webui/agentmodules/automation_agent_identity_compat_test.go",
		"internal/webui/agentmodules/automation_connector_compat.go",
		"internal/webui/agentmodules/automation_connector_compat_test.go",
	} {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !os.IsNotExist(statErr) {
			t.Errorf("retired Automation compatibility file %s returned (stat error: %v)", relative, statErr)
		}
	}
	for relative, forbidden := range map[string][]string{
		"internal/webui/handlers/triggerbindings/module.go": {
			"ConnectorCompatibility", "UnattachedBindingIdentityChecker", "DeleteBindingAndRevokeGrants",
		},
		"internal/webui/handlers/agents/module.go": {"BindingGrantCompatibility"},
		"internal/webui/agentmodules/automation_routes.go": {
			"storeConnectorCompatibility", "storeAgentIdentityCompatibility", "bindingGrantCompatibility",
		},
	} {
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(content), fragment) {
				t.Errorf("retired Automation compatibility symbol %q returned in %s", fragment, relative)
			}
		}
	}
}

func TestRetiredSourceCompatibilityAPIsCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"internal/driver/stale_task_sweeper_legacy_test.go",
		"internal/driver/stale_task_sweeper_test.go",
		"internal/driver/await_op_legacy_test.go",
		"internal/driver/await_op_test.go",
		"internal/driver/composition_legacy_test.go",
		"internal/driver/composition_test.go",
		"internal/driver/run_events_test.go",
		"internal/driver/run_legacy_test.go",
		"internal/driver/run_test.go",
		"internal/driver/register_legacy_test.go",
		"internal/driver/task_close_suppression_test.go",
		"internal/driver/task_events_legacy_test.go",
		"internal/driver/task_events_test.go",
		"internal/driver/task_request_e2e_input_test.go",
		"internal/driver/task_request_input_test.go",
		"internal/driver/task_request_legacy_test.go",
		"internal/driver/task_request_test.go",
		"internal/driver/task_retry_test.go",
		"internal/infra/automationruntime/internal_source.go",
		"internal/infra/automationruntime/internal_source_test.go",
		"internal/infra/automationruntime/binding_run_input.go",
		"internal/infra/automationruntime/binding_run_input_test.go",
		"internal/infra/automationruntime/cron.go",
		"internal/infra/automationruntime/cron_test.go",
		"internal/infra/automationruntime/delivery_sweeper.go",
		"internal/infra/automationruntime/delivery_sweeper_test.go",
		"internal/infra/automationruntime/pattern.go",
		"internal/infra/automationruntime/pattern_test.go",
		"internal/infra/automationruntime/issue_journal_bridge_task_review_integration_test.go",
		"internal/infra/workflowdistribution/authoring/builtin_authoring.go",
		"internal/infra/workflowdistribution/authoring/native_authoring.go",
		"internal/infra/workflowdistribution/authoring/legacy_authoring_test.go",
		"internal/infra/workflowdistribution/authoring/legacy_compat_test.go",
		"internal/infra/fleetdb/webhook_dispatch_test.go",
		"internal/infra/fleetdb/platform_trigger_fanout_test.go",
		"internal/infra/memstore/platform_trigger_test.go",
		"internal/infra/memstore/platform_trigger_fanout_test.go",
		"internal/infra/memstore/platform_provenance_test.go",
		"internal/webui/handlers/webhooks/await_dispatch_test.go",
		"internal/webui/handlers/webhooks/webhooks_router_e2e_test.go",
	} {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !os.IsNotExist(statErr) {
			t.Errorf("retired compatibility test implementation %s returned (stat error: %v)", relative, statErr)
		}
	}
	files := map[string]map[string]struct{}{
		"internal/infra/interactionlead/codex_delivery.go": {
			"DeliverCurrentAssignmentToCodex": {}, "DeliverLeadMessageToCodex": {},
			"DeliverLeadMessageToCodexWithOptions": {}, "DeliverPendingLeadMessagesToCodex": {},
		},
		"internal/infra/automationruntime/await_reconcilers.go": {
			"NewAutomationAwaitEventNotifier": {},
		},
		"internal/webui/handlers/webhooks/module.go":        {"NewModule": {}},
		"internal/webui/handlers/triggerbindings/module.go": {"NewModule": {}},
		"internal/driver/stale_task_sweeper.go":             {"DefaultStaleTaskRunMaxAge": {}},
		"internal/webui/storeadapter/storeadapter.go":       {"DefaultWorkspaceKey": {}},
	}
	for relative, forbidden := range files {
		parsed, parseErr := parser.ParseFile(
			token.NewFileSet(), filepath.Join(root, filepath.FromSlash(relative)), nil, 0,
		)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range parsed.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if _, retired := forbidden[value.Name.Name]; retired {
					t.Errorf("retired compatibility function %s returned in %s", value.Name.Name, relative)
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					values, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range values.Names {
						if _, retired := forbidden[name.Name]; retired {
							t.Errorf("retired compatibility value %s returned in %s", name.Name, relative)
						}
					}
				}
			}
		}
	}
}

func TestRetiredCompatibilitySurfacesCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for relative, forbidden := range map[string][]string{
		"internal/app/serve/execution_task_run_recovery.go": {
			"LegacyDriverRuns", "AllowLegacyStoreAdapters", "executionTaskRunLegacyRecoveryAdapter",
		},
		"internal/app/serve/execution.go":                                     {"LegacyDriverRuns:"},
		"internal/driver/task_worker.go":                                      {"legacyTaskRunFromExecution"},
		"internal/driver/task_worktree_resolver.go":                           {"repoBasename"},
		"internal/domain/control_plane.go":                                    {"AgentSessionKindOrchestration"},
		"internal/webui/app/server.go":                                        {"wsExistsFn"},
		"internal/webui/sessioncoord/execution_projection.go":                 {"legacyAgentSessionTaskRunID"},
		"internal/modules/automation/admission.go":                            {"deliveryDispatchLegacyKeyAccepted", "taskReadyExhaustedRecoverySuffix"},
		"internal/infra/automationruntime/issue_journal_bridge_task_ready.go": {"snapshotSourceRepo"},
		"internal/store/platform_store.go": {
			"TriggerRouteDispatcher", "TriggerRouteDispatch", "TriggerRouteDelivery", "PrimaryRun *domain.DriverRun",
		},
		"internal/infra/memstore/platform_trigger.go": {
			"triggerRouteStore", "DispatchTriggerRouteV2", "legacy := exact", "runID(supplied",
		},
		"internal/infra/fleetdb/platform_trigger.go": {"triggerRouteStore", "DispatchTriggerRouteV2"},
	} {
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(content), fragment) {
				t.Errorf("retired Execution recovery Store fallback %q returned in %s", fragment, relative)
			}
		}
	}
}

func TestRetiredDriverAuthenticationFallbackCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"LOOM_DRIVER_API_TOKEN", "LOOM_DRIVER_LEGACY_AUTH_ENV",
		"HeaderDriverRunID", "HeaderDriverNodeID", "HeaderDriverLeaseID", "HeaderDriverLeaseToken", "HeaderDriverFencingToken",
		"legacyDriverAuthEnv", "driverAPIToken",
	}
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "archtest" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(content), fragment) {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("retired Driver authentication fallback %q returned in %s", fragment, filepath.ToSlash(relative))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetiredTerminalLaunchFallbackCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]string{
		"internal/webui/terminal/session_command.go": {
			"ArgvForSession", "session names encode the backend",
		},
		"internal/webui/handlers/terminal/ws.go": {
			"legacyLaunchSpecForSession", "isUUIDTerminalSession",
		},
		"internal/webui/frontend/src/components/TerminalView/tabs/terminalTabUtils.ts": {
			"getBackendFromSessionName", `startsWith("agent-")`,
		},
		"internal/webui/frontend/src/components/TerminalView/tabs/useTabInit.ts": {
			"getBackendFromSessionName", `startsWith("agent-")`,
		},
		"internal/webui/frontend/src/components/IssueDetailPanel/IssueDetailPanel.tsx": {
			"getBackendFromSessionName",
		},
		"internal/webui/frontend/src/components/TerminalView/instances/useSessionSeeding.ts": {
			"trySeedOnConnect",
		},
		"internal/webui/frontend/src/components/TerminalView/instances/useConnectionState.ts": {
			"onTabConnected",
		},
	}
	for relative, forbidden := range files {
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(content), fragment) {
				t.Errorf("retired terminal launch fallback %q returned in %s", fragment, relative)
			}
		}
	}
}

func TestRetiredHandlerBackendCompatibilityCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]map[string]struct{}{
		"internal/webui/app/workspace_routes.go": {
			"WithIssueBackendFn": {},
		},
		"internal/webui/handlers/git/graph.go": {
			"IssueBackendFn": {}, "HandleBlockedWithBackend": {}, "HandleGraphWithBackend": {},
			"serveBlockedViaBackend": {}, "serveGraphViaBackend": {},
		},
		"internal/webui/handlers/health/health.go": {
			"IssueBackendFn": {}, "HandleStatsWithBackend": {}, "serveStatsViaBackend": {},
		},
	}
	for relative, forbidden := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		checkName := func(name string) {
			if _, retired := forbidden[name]; retired {
				t.Errorf("retired handler backend compatibility symbol %s returned in %s", name, relative)
			}
		}
		for _, declaration := range parsed.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				checkName(value.Name.Name)
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					switch named := spec.(type) {
					case *ast.TypeSpec:
						checkName(named.Name.Name)
					case *ast.ValueSpec:
						for _, name := range named.Names {
							checkName(name.Name)
						}
					}
				}
			}
		}
	}
	authConfig, err := os.ReadFile(filepath.Join(root, "internal", "webui", "handlers", "misc", "auth_config.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(authConfig), `os.Getenv("LOOM_ISSUE_BACKEND")`) {
		t.Error("retired /api/config backend environment fallback returned")
	}
}

func TestRetiredBackendMutationPlaneCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	forbiddenNames := map[string]struct{}{
		"MutationData": {}, "CursorMutationBackend": {},
		"GetMutations": {}, "WaitForMutations": {},
		"ensureStoreBackedSSESubscriber": {}, "ActivationReasonLegacy": {},
	}
	for _, relative := range []string{
		"internal/backend",
		"internal/backend/api",
		"internal/backend/fleet",
		"internal/cli",
		"internal/webui/app",
		"internal/webui/subscription",
	} {
		files, globErr := filepath.Glob(filepath.Join(root, filepath.FromSlash(relative), "*.go"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				switch declaration := node.(type) {
				case *ast.TypeSpec:
					if _, forbidden := forbiddenNames[declaration.Name.Name]; forbidden {
						t.Errorf("retired backend mutation type %s returned in %s", declaration.Name.Name, path)
					}
				case *ast.FuncDecl:
					if _, forbidden := forbiddenNames[declaration.Name.Name]; forbidden {
						t.Errorf("retired backend mutation function %s returned in %s", declaration.Name.Name, path)
					}
				case *ast.Field:
					for _, name := range declaration.Names {
						if _, forbidden := forbiddenNames[name.Name]; forbidden {
							t.Errorf("retired backend mutation method %s returned in %s", name.Name, path)
						}
					}
				}
				return true
			})
		}
	}

	apiBackend, err := os.ReadFile(filepath.Join(root, "internal", "backend", "api", "backend.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(apiBackend), "does not implement SSE subscription") {
		t.Error("retired API backend mutation fallback returned")
	}
}

func TestUnusedIssueBackendCompatibilityOperationsCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	forbiddenNames := map[string]struct{}{
		"BatchOp": {}, "BatchResult": {}, "Batch": {}, "GetChildren": {},
		"DeferIssue": {}, "UndeferIssue": {}, "AddLabel": {}, "RemoveLabel": {},
		"CountOpts": {}, "Count": {}, "SearchIssues": {}, "StatsData": {}, "BlockedOpts": {},
	}
	for _, relative := range []string{
		"internal/backend",
		"internal/backend/api",
		"internal/backend/fleet",
		"internal/cli",
		"internal/cli/data",
	} {
		files, globErr := filepath.Glob(filepath.Join(root, filepath.FromSlash(relative), "*.go"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				switch declaration := node.(type) {
				case *ast.TypeSpec:
					if _, forbidden := forbiddenNames[declaration.Name.Name]; forbidden {
						t.Errorf("retired backend compatibility type %s returned in %s", declaration.Name.Name, path)
					}
				case *ast.FuncDecl:
					if _, forbidden := forbiddenNames[declaration.Name.Name]; forbidden {
						t.Errorf("retired backend compatibility function %s returned in %s", declaration.Name.Name, path)
					}
				case *ast.Field:
					for _, name := range declaration.Names {
						if _, forbidden := forbiddenNames[name.Name]; forbidden {
							t.Errorf("retired backend compatibility method %s returned in %s", name.Name, path)
						}
					}
				}
				return true
			})
		}
	}
}

func TestRetiredFleetWorkItemFallbacksCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retiredFile := filepath.Join(root, "internal", "backend", "fleet", "create_compat.go")
	if _, statErr := os.Stat(retiredFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("retired Fleet create compatibility file returned: %v", statErr)
	}

	forbiddenNames := map[string]struct{}{
		"isCreateExternalRefUnsupported":    {},
		"createWithoutExternalRef":          {},
		"createExternalRefPatchError":       {},
		"unmarshalCanonicalRepositoryIssue": {},
	}
	files, err := filepath.Glob(filepath.Join(root, "internal", "backend", "fleet", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, retiredText := range []string{
			`unknown field \"external_ref\"`,
			"derive compatibility idempotency key",
			"retries without external_ref",
			"bare issue for compatibility",
		} {
			if strings.Contains(string(source), retiredText) {
				t.Errorf("retired Fleet fallback text %q returned in %s", retiredText, path)
			}
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			declaration, ok := node.(*ast.FuncDecl)
			if ok {
				if _, forbidden := forbiddenNames[declaration.Name.Name]; forbidden {
					t.Errorf("retired Fleet fallback function %s returned in %s", declaration.Name.Name, path)
				}
			}
			return true
		})
	}
}

func TestRetiredWorkItemCreateProjectionFallbackCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"internal/modules/workitems",
		"internal/app/workitemmove",
		"internal/webui/handlers/onboarding",
	} {
		files, globErr := filepath.Glob(filepath.Join(root, filepath.FromSlash(relative), "*.go"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			source, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, retiredText := range []string{
				"CreatedIssue",
				"post-create read is unavailable",
				"Summary remains a valid fallback",
			} {
				if strings.Contains(string(source), retiredText) {
					t.Errorf("retired Work Items create fallback %q returned in %s", retiredText, path)
				}
			}
			parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, source, 0)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				declaration, ok := node.(*ast.FuncDecl)
				if ok && declaration.Name.Name == "createdIssueID" {
					t.Errorf("retired Work Items create-result branch returned in %s", path)
				}
				return true
			})
		}
	}
}

func TestHandwrittenProductionAPIsDoNotRemainDeprecated(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	internalRoot := filepath.Join(root, "internal")
	var deprecated []string
	err = filepath.WalkDir(internalRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			relative, relErr := filepath.Rel(internalRoot, path)
			if relErr != nil {
				return relErr
			}
			if relative == "archtest" || relative == filepath.Join("backend", "api", "gen") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		for _, comment := range parsed.Comments {
			if strings.Contains(comment.Text(), "Deprecated:") {
				relative, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				deprecated = append(deprecated, filepath.ToSlash(relative))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(deprecated)
	if len(deprecated) != 0 {
		t.Fatalf("handwritten production Deprecated declarations returned: %v", deprecated)
	}
}

func TestWorkflowHTTPModuleRequiresTypedConfig(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "internal", "webui", "handlers", "workflows", "module.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "NewModule" {
			continue
		}
		if function.Type.Params == nil || len(function.Type.Params.List) != 1 {
			t.Fatal("workflows.NewModule must accept exactly one typed Config")
		}
		parameter, ok := function.Type.Params.List[0].Type.(*ast.Ident)
		if !ok || parameter.Name != "Config" {
			t.Fatalf("workflows.NewModule parameter = %T, want Config", function.Type.Params.List[0].Type)
		}
		return
	}
	t.Fatal("workflows.NewModule is missing")
}

func TestRetiredArtifactModelAndRepositoryPlaneCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"internal/store/artifact_upload.go",
		"internal/infra/artifactcatalog/catalog.go",
	} {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !os.IsNotExist(statErr) {
			t.Errorf("retired artifact plane path %s returned (stat error: %v)", relative, statErr)
		}
	}

	forbiddenNames := map[string]struct{}{
		"Artifact": {}, "ArtifactStore": {}, "ArtifactContentReader": {},
		"ArtifactCreate": {}, "ArtifactFilter": {}, "ArtifactUpdate": {},
		"ArtifactFinalize": {}, "ArtifactContentUpload": {},
		"UploadContentArtifact": {}, "Artifacts": {}, "ArtifactQueries": {},
	}
	for _, relative := range []string{"internal/domain", "internal/store"} {
		files, globErr := filepath.Glob(filepath.Join(root, filepath.FromSlash(relative), "*.go"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				switch declaration := node.(type) {
				case *ast.TypeSpec:
					if _, forbidden := forbiddenNames[declaration.Name.Name]; forbidden {
						t.Errorf("retired artifact type %s returned in %s", declaration.Name.Name, path)
					}
				case *ast.FuncDecl:
					if _, forbidden := forbiddenNames[declaration.Name.Name]; forbidden {
						t.Errorf("retired artifact function or method %s returned in %s", declaration.Name.Name, path)
					}
				case *ast.Field:
					for _, name := range declaration.Names {
						if _, forbidden := forbiddenNames[name.Name]; forbidden {
							t.Errorf("retired artifact field or interface method %s returned in %s", name.Name, path)
						}
					}
				}
				return true
			})
		}
	}
}

func TestPhase7LegacyTypeBucketsCannotPublishAliases(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"internal/domain", "internal/entity"} {
		relative := relative
		t.Run(strings.ReplaceAll(relative, "/", "_"), func(t *testing.T) {
			files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(relative), "*.go"))
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range files {
				parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				if err != nil {
					t.Fatal(err)
				}
				for _, declaration := range parsed.Decls {
					generic, ok := declaration.(*ast.GenDecl)
					if !ok || generic.Tok != token.TYPE {
						continue
					}
					for _, specification := range generic.Specs {
						typeSpec, ok := specification.(*ast.TypeSpec)
						if ok && typeSpec.Assign.IsValid() {
							t.Errorf("legacy compatibility alias %s remains in %s", typeSpec.Name.Name, path)
						}
					}
				}
			}
		})
	}
}

func TestPhase6SupervisorRuntimePathsCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retired := []string{
		"internal/backend/agentipc",
		"internal/cli/daemon",
		"internal/cli/serve/daemonwire",
		"internal/rpc",
		"internal/webui/daemon",
	}
	for _, relative := range retired {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(relative)), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() {
				t.Errorf("retired Phase 6 runtime path contains %s", path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestCapabilityGraphRejectsSynchronousCycle(t *testing.T) {
	graph := validGraph()
	graph.Edges = []GraphEdge{
		{From: "automation", To: "execution", Kinds: []string{"command"}},
		{From: "execution", To: "automation", Kinds: []string{"query"}},
	}
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestCapabilityGraphAllowsDurableReverseEdge(t *testing.T) {
	graph := validGraph()
	graph.Edges = []GraphEdge{
		{From: "automation", To: "execution", Kinds: []string{"command"}},
		{From: "execution", To: "automation", Kinds: []string{"durable_event"}, Durable: validDurablePolicy()},
	}
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityGraphApprovalRequiresCompletedBoundaryContract(t *testing.T) {
	graph := validGraph()
	graph.Status = "approved"
	if err := graph.Validate(); err != nil {
		t.Fatalf("completed graph should be approvable: %v", err)
	}
	graph.Restrictions.NamedAppsOwnPortsOnly = false
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "restrictions") {
		t.Fatalf("expected boundary-contract blocker, got %v", err)
	}
}

func TestCapabilityGraphRejectsUnboundedDurableEdge(t *testing.T) {
	graph := validGraph()
	graph.Edges = []GraphEdge{{From: "execution", To: "automation", Kinds: []string{"durable_event"}}}
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "requires idempotency") {
		t.Fatalf("expected durable policy error, got %v", err)
	}
}

func TestCapabilityGraphRequiresEveryMigrationDecision(t *testing.T) {
	graph := validGraph()
	graph.DecisionDependencies = graph.DecisionDependencies[:6]
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "decision dependencies") {
		t.Fatalf("expected decision-dependency error, got %v", err)
	}
}

func TestAnalysisMatrixRejectsDeferredProfileAfterPhase1(t *testing.T) {
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix.Release[0].Enforced = false
	if err := matrix.Validate(); err == nil || !strings.Contains(err.Error(), "must be enforced") {
		t.Fatalf("expected deferred-profile error, got %v", err)
	}
}

func TestAnalysisMatrixRejectsWrongProfileTuple(t *testing.T) {
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix.Release[0].GOOS = "windows"
	if err := matrix.Validate(); err == nil || !strings.Contains(err.Error(), "has tuple") {
		t.Fatalf("expected profile-tuple error, got %v", err)
	}
}

func TestAnalysisMatrixRequiresTaggedSourceSentinel(t *testing.T) {
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix.Tagged[0].RequiredFiles = nil
	if err := matrix.Validate(); err == nil || !strings.Contains(err.Error(), "source-selection sentinel") {
		t.Fatalf("expected required-source sentinel error, got %v", err)
	}
}

func TestAnalysisMatrixRejectsUnsafeTaggedSourceSentinel(t *testing.T) {
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix.Tagged[0].RequiredFiles = []string{"../outside.go"}
	if err := matrix.Validate(); err == nil || !strings.Contains(err.Error(), "clean relative Go source path") {
		t.Fatalf("expected unsafe required-source error, got %v", err)
	}
}

func TestRatchetRejectsNewCompositeStoreFile(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/newcap/new.go", `package newcap

import "github.com/tysonthomas9/loomcli/internal/store"

type State struct { Store store.Store }
`)
	baseline := scanBaseline(nil)
	_, violations, err := scanRepository(root, baseline, validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "new composite Store use") {
		t.Fatalf("expected new-use violation, got %v", violations)
	}
}

func TestRatchetRequiresBaselineRefreshOnRemoval(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/empty/doc.go", "package empty\n")
	baseline := scanBaseline([]string{"internal/legacy/removed.go"})
	baseline.Ratchets.CompositeStore.MaxProductionFiles = 1
	_, violations, err := scanRepository(root, baseline, validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "stale composite Store baseline entry") {
		t.Fatalf("removing a legacy use without refreshing the baseline must fail, got %v", violations)
	}
	baseline = scanBaseline(nil)
	_, violations, err = scanRepository(root, baseline, validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("removal with refreshed baseline must pass, got %v", violations)
	}
}

func TestRatchetRejectsNewForbiddenHandlerImport(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/webui/handlers/new/module.go", `package new

import _ "github.com/tysonthomas9/loomcli/internal/store"
`)
	baseline := scanBaseline(nil)
	baseline.Ratchets.LegacyHandlerImports = LegacyImportRatchet{
		Root:           "internal/webui/handlers",
		DeniedPrefixes: []string{"github.com/tysonthomas9/loomcli/internal/store"},
	}
	_, violations, err := scanRepository(root, baseline, validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "new forbidden handler import") {
		t.Fatalf("expected handler-import violation, got %v", violations)
	}
}

func TestProposedGraphBlocksPackageMoves(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/workspace/doc.go", "package workspace\n")
	graph := validGraph()
	graph.Status = "proposed"
	_, violations, err := scanRepository(root, scanBaseline(nil), graph)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "graph is still proposed") {
		t.Fatalf("expected proposed-graph violation, got %v", violations)
	}
}

func TestApprovedGraphRejectsUnknownModuleImport(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/workspace/doc.go", `package workspace

import _ "github.com/tysonthomas9/loomcli/internal/modules/unknown"
`)
	graph := validGraph()
	graph.Status = "approved"
	_, violations, err := scanRepository(root, scanBaseline(nil), graph)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "capability imports must target a declared capability public root") {
		t.Fatalf("expected unknown-root violation, got %v", violations)
	}
}

func TestApprovedGraphRejectsLegacyInternalImport(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/workspace/doc.go", `package workspace

import _ "github.com/tysonthomas9/loomcli/internal/store"
`)
	graph := validGraph()
	graph.Status = "approved"
	_, violations, err := scanRepository(root, scanBaseline(nil), graph)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "capability core may not import internal implementation package") {
		t.Fatalf("expected forbidden-internal-import violation, got %v", violations)
	}
}

func TestModuleRootRejectsGoFiles(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/modules.go", "package modules\n")
	_, violations, err := scanRepository(root, scanBaseline(nil), validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "directly at the module root") {
		t.Fatalf("expected module-root-file violation, got %v", violations)
	}
}

func TestViolationsErrorSupportsErrorsAs(t *testing.T) {
	err := error(&ViolationsError{Violations: []string{"one"}})
	var target *ViolationsError
	if !errors.As(err, &target) {
		t.Fatal("expected ViolationsError")
	}
}

func validGraph() CapabilityGraph {
	names := []string{"agents", "artifacts", "automation", "connectors", "execution", "interaction", "sourcecontrol", "workflowcatalog", "workitems", "workspace"}
	capabilities := make([]Capability, 0, len(names))
	for _, name := range names {
		capabilities = append(capabilities, Capability{Name: name, Root: name, Status: "planned"})
	}
	return CapabilityGraph{
		SchemaVersion:        SchemaVersion,
		Status:               "approved",
		CompletedPhase:       1,
		DecisionDependencies: []string{"MM-1", "MM-2", "MM-3", "MM-4", "MM-5", "MM-6", "MM-7"},
		ModuleRoot:           "internal/modules",
		AppRoot:              "internal/app",
		PlatformRoot:         "internal/platform",
		Restrictions: BoundaryRestrictions{
			ServeCompositionOnly:     true,
			NamedAppsPublicRootsOnly: true,
			NamedAppsOwnPortsOnly:    true,
			ModulesRejectLegacyTypes: true,
		},
		ExternalImports: ExternalImportPolicy{
			CoreAllowedPrefixes:        []string{},
			AdapterAllowedPrefixes:     []string{},
			PlatformAllowedPrefixes:    []string{},
			CoreDeniedStandardPrefixes: []string{"database/sql", "net/http", "net/rpc", "os", "plugin", "syscall"},
		},
		Capabilities:       capabilities,
		AggregateOwnership: validAggregateOwnership(),
		LegacyPaths: []LegacyPath{{
			Path: "internal/domain", Owner: "test", RemovalIssue: "MM-TEST", ExpiresAfterPhase: 2,
		}},
	}
}

func validAggregateOwnership() []AggregateOwnership {
	identities := approvedAggregateOwnership()
	records := make([]string, 0, len(identities))
	for record := range identities {
		records = append(records, record)
	}
	slices.Sort(records)
	values := make([]AggregateOwnership, 0, len(records))
	for _, record := range records {
		identity := identities[record]
		values = append(values, AggregateOwnership{
			Record: record, Owner: identity.owner, Mechanism: identity.mechanism,
			Discriminator: identity.discriminator, CrossCapabilityRule: "test contract",
		})
	}
	return values
}

func TestCapabilityGraphPinsApprovedOwnershipContract(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CapabilityGraph)
		want   string
	}{
		{name: "status downgrade", mutate: func(graph *CapabilityGraph) { graph.Status = "proposed" }, want: "status must be approved"},
		{name: "capability removed", mutate: func(graph *CapabilityGraph) { graph.Capabilities = graph.Capabilities[1:] }, want: "exact ten approved capabilities"},
		{name: "capability root changed", mutate: func(graph *CapabilityGraph) { graph.Capabilities[0].Root = "renamed" }, want: "root:"},
		{name: "ownership row removed", mutate: func(graph *CapabilityGraph) { graph.AggregateOwnership = graph.AggregateOwnership[1:] }, want: "exact approved aggregate-owner matrix"},
		{name: "ownership changed", mutate: func(graph *CapabilityGraph) { graph.AggregateOwnership[0].Owner = "workspace" }, want: "drifted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph := validGraph()
			tt.mutate(&graph)
			if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCapabilityGraphRejectsExpiredLegacyPath(t *testing.T) {
	graph := validGraph()
	graph.CompletedPhase = graph.LegacyPaths[0].ExpiresAfterPhase
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "expired after Phase") {
		t.Fatalf("Validate error = %v, want completed-phase expiry rejection", err)
	}
}

func TestCapabilityGraphRejectsIncompleteLegacyPathExtension(t *testing.T) {
	graph := validGraph()
	graph.LegacyPaths[0].Extension = &LegacyPathExtension{ReviewedBy: "reviewer"}
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "extension requires reviewer, date, rationale, replacement APIs, and remaining call sites") {
		t.Fatalf("Validate error = %v, want incomplete-extension rejection", err)
	}
}

func TestCapabilityGraphAllowsZeroLegacyPathsAtMigrationCompletion(t *testing.T) {
	graph := validGraph()
	graph.CompletedPhase = 7
	graph.LegacyPaths = []LegacyPath{}
	if err := graph.Validate(); err != nil {
		t.Fatalf("Validate completed graph with no legacy paths: %v", err)
	}
}

func validDurablePolicy() *DurableEventPolicy {
	return &DurableEventPolicy{
		IdempotencyKey: "workspace:event", ActorScope: "execution", MaxHops: 8, ReentryPolicy: "reject",
	}
}

func scanBaseline(allowed []string) Baseline {
	decisions := make([]DecisionBaseline, 0, 7)
	for _, id := range []string{"MM-1", "MM-2", "MM-3", "MM-4", "MM-5", "MM-6", "MM-7"} {
		decisions = append(decisions, DecisionBaseline{
			ID: id, Status: "approved", Owner: "test", Rationale: "test", ReviewedBy: "test", ReviewedAt: "2026-07-15",
		})
	}
	return Baseline{
		Ratchets: RatchetBaseline{CompositeStore: CompositeStoreRatchet{
			MaxProductionFiles:        len(allowed),
			MaxOutsideComposition:     len(allowed),
			CompositionPrefixes:       []string{"internal/cli/serve/", "internal/infra/", "internal/store/"},
			AllowedProductionFileUses: allowed,
		}},
		Decisions: decisions,
	}
}

func writeGoFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func containsViolation(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
