// Cardinality lint for span names.
//
// The trace contract (docs/observability/tracing-contract.md §3) forbids
// high-cardinality span names. Span names appear as labels in dashboards
// and as series identifiers in Tempo/Honeycomb queries; if they embed
// hex IDs, UUIDs, workspace keys, issue IDs, timestamps, or other
// unbounded values, the cardinality of the indexed name set explodes
// and dashboards stop being usable.
//
// Enforcement strategy:
//
//  1. The package-level allowlistRegexes constant below IS the contract.
//     It is the source-of-truth for what a valid span name looks like.
//     Any new span shape MUST add a regex here in the same PR that
//     introduces it, and reviewers gate that change against the human-
//     readable contract document.
//
//  2. TestSpanNames_KnownNamesMatchAllowlist is a synthetic table-driven
//     test that runs known-good and known-bad names through the
//     allowlist. It guards the regexes themselves: catches a typo that
//     would silently let any name through, and catches a regex that
//     accidentally rejects a valid name.
//
//  3. TestSpanNames_InMemoryExporterSmoke is a smoke check that drives a
//     representative subset of real span-emission paths (the singleton
//     AgentEventBus → otelexport → loom.task / loom.agent.lifecycle, plus
//     direct tracer.Start calls for the CLI / backend shapes) through
//     an in-memory exporter and runs every emitted name through the
//     allowlist. Many production span shapes (HTTP server middleware,
//     fleet-db pgx, Redis, RPC layer) cannot be unit-tested without a
//     full stack; for those, the synthetic test above is the gate.
package tracing_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/observability/tracing"
)

// allowlistRegexes is the canonical, source-of-truth list of valid span-
// name shapes per the trace contract. Each regex covers one span kind.
//
// To add a new span shape, add a regex here AND update
// docs/observability/tracing-contract.md §3 in the same PR.
//
// Forbidden by construction: any name containing a hex ID
// (`[a-f0-9]{8,}`), a UUID, a workspace key (e.g. raw `HAPPY` interpolated
// into `/api/v1/HAPPY/issues`), an issue ID (e.g. `HAPPY-42`), a long
// random token, or a timestamp. Each regex below is anchored with `^…$`
// so partial matches of a high-cardinality string can't slip through.
var allowlistRegexes = []*regexp.Regexp{
	// CLI root and subcommand spans: `loom.cli`, `loom.cli.plan`,
	// `loom.cli.workspace_list`. The verb segment is lowercase letters
	// + underscore — no digits, no dashes (so `loom.cli.HAPPY-42` fails).
	regexp.MustCompile(`^loom\.cli(\.[a-z][a-z_]*)?$`),

	// Event-derived spans emitted by otelexport. These names are fixed
	// by the event-bus → trace mapping in
	// internal/events/otelexport/exporter.go.
	regexp.MustCompile(`^loom\.task$`),
	regexp.MustCompile(`^loom\.agent\.lifecycle$`),

	// Backend invocation sub-spans (Tier 1A decorator). Format:
	// `loom.backend.<backend>.invoke_<mode>`. Backend is one of
	// claude/codex/opencode (lowercase letters), mode is interactive,
	// non_interactive, or streaming.
	regexp.MustCompile(`^loom\.backend\.[a-z]+\.invoke_(interactive|non_interactive|streaming)$`),

	// HTTP server: `<METHOD> <route-template>`. Route may contain
	// `{param}` placeholders but MUST NOT contain raw IDs/keys. Allowed
	// chars in the path are lowercase letters, digits, slash, braces,
	// underscore, dot, dash. Uppercase letters are rejected so a leaked
	// workspace key like `HAPPY` fails.
	regexp.MustCompile(`^[A-Z]+ /[a-z0-9/{}_.\-]+$`),

	// HTTP client: method only. Route lives on `http.route` attribute.
	regexp.MustCompile(`^HTTP [A-Z]+$`),

	// Redis commands and pipelines: `redis.HGETALL`, `redis.PIPELINE`.
	regexp.MustCompile(`^redis\.[A-Z]+$`),

	// Postgres via otelpgx: `pgx.query`, `pgx.exec`, `pgx.connect`,
	// `pgx.prepare`. The library emits these four names — pin the set
	// so a future upgrade that adds a new op forces a contract update.
	regexp.MustCompile(`^pgx\.(query|exec|connect|prepare)$`),

	// Service layer methods: `service.IssueService.Claim`,
	// `workspacecoord.WorkspaceService.List`, `service.IssueBackend.Get`.
	// Two CamelCase identifiers joined by dots. Loom's extracted Workspace
	// coordinator uses its fixed package name; fleet-db retains `service`.
	regexp.MustCompile(`^service\.[A-Z][a-zA-Z]+\.[A-Z][a-zA-Z]+$`),
	regexp.MustCompile(`^workspacecoord\.[A-Z][a-zA-Z]+\.[A-Z][a-zA-Z]+$`),

	// Projector handler spans (fleet-db). Lowercase event-name segments
	// like `service.Projector.workspace.upsert`,
	// `service.Projector.issue.field_changed`.
	regexp.MustCompile(`^service\.Projector\.[a-z][a-z_.]*$`),

	// Background workers: compaction.cycle, archive.cycle,
	// retention.cycle.
	regexp.MustCompile(`^(compaction|archive|retention)\.cycle$`),

	// RPC dispatch (fleet-db RPC layer): `rpc.task_claim`,
	// `rpc.workspace_list`, etc.
	regexp.MustCompile(`^rpc\.[a-z][a-z_.]*$`),

	// Git subprocess invocations (Tier L4 decorator). Format:
	// `git.<subcommand>` where subcommand is a bounded git verb
	// (push, pull, fetch, merge, rebase, status, commit, log, diff,
	// rev-parse, worktree, …). The fallback `git.unknown` is emitted
	// when the first arg is missing or contains characters outside the
	// allowlisted [a-z_-] set, so the cardinality of git.* span names
	// stays bounded by the git verb vocabulary regardless of caller
	// input. See internal/cli/git_runner_tracing.go.
	regexp.MustCompile(`^git\.[a-z][a-z_-]*$`),

	// WebSocket lifetime spans (Tier L1). The route lives on the
	// otelhttp parent span; these names are the bounded set of
	// lifecycle phases. Per docs/observability/tracing-contract.md §3:
	// `ws.upgrade` covers the handshake, `ws.disconnect` covers the
	// teardown — neither span covers the long-lived relay (per-message
	// spans would flood the collector). See
	// internal/webui/handlers/terminal/{ws,agent}.go.
	regexp.MustCompile(`^ws\.(upgrade|disconnect)$`),

	// Server-Sent Events lifetime spans (Tier L1). Same shape as the
	// WS counterpart: `sse.handshake` covers the initial replay +
	// connected event, `sse.disconnect` covers teardown. The long-
	// lived event pump in between is unspanned. See
	// internal/webui/server/realtime/handler.go.
	regexp.MustCompile(`^sse\.(handshake|disconnect)$`),
}

// matchesAllowlist returns nil if name matches at least one regex in
// allowlistRegexes; otherwise an error naming the violator. Used by both
// the synthetic test and the in-memory exporter smoke test.
func matchesAllowlist(name string) error {
	for _, re := range allowlistRegexes {
		if re.MatchString(name) {
			return nil
		}
	}
	return &cardinalityViolation{name: name}
}

type cardinalityViolation struct{ name string }

func (e *cardinalityViolation) Error() string {
	return "span name " + strconv(e.name) + " violates the cardinality contract " +
		"(docs/observability/tracing-contract.md §3); no allowlist regex matches. " +
		"If this is a legitimate new span shape, add a regex to allowlistRegexes " +
		"AND update the contract doc."
}

// strconv quotes a string for inclusion in an error message without
// pulling in fmt.Sprintf (kept tiny so the violation message stays
// grep-friendly). Equivalent to %q for ASCII names.
func strconv(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	b.WriteString(s)
	b.WriteByte('"')
	return b.String()
}

// TestSpanNames_KnownNamesMatchAllowlist is the primary regex-correctness
// gate. The positive cases ensure every shape declared in the contract
// passes; the negative cases ensure each high-cardinality footgun fails.
// If you add a new allowlist regex, add a positive case here. If a
// reviewer raises a new "could this slip through?" worry, add a negative
// case here.
func TestSpanNames_KnownNamesMatchAllowlist(t *testing.T) {
	positives := []string{
		// CLI roots and subcommands.
		"loom.cli",
		"loom.cli.plan",
		"loom.cli.task",
		"loom.cli.workspace_list",

		// Event-derived (preserved names).
		"loom.task",
		"loom.agent.lifecycle",

		// Backend invocation (Tier 1A).
		"loom.backend.claude.invoke_interactive",
		"loom.backend.codex.invoke_non_interactive",
		"loom.backend.opencode.invoke_streaming",

		// HTTP server with route template.
		"GET /api/v1/{workspace}/issues",
		"POST /api/v1/{workspace}/issues/{id}",
		"DELETE /api/v1/{workspace}",
		"GET /healthz",

		// HTTP client.
		"HTTP GET",
		"HTTP POST",

		// Redis.
		"redis.HGETALL",
		"redis.PIPELINE",
		"redis.SET",

		// Postgres via otelpgx (the four library-emitted names).
		"pgx.query",
		"pgx.exec",
		"pgx.connect",
		"pgx.prepare",

		// Service layer (Tier L2).
		"service.IssueService.Claim",
		"workspacecoord.WorkspaceService.List",
		"service.IssueBackend.Get",
		"service.IssueBackend.SearchIssues",
		"service.IssueBackend.WaitForMutations",

		// Projector.
		"service.Projector.workspace.upsert",
		"service.Projector.issue.field_changed",

		// Background workers.
		"compaction.cycle",
		"archive.cycle",
		"retention.cycle",

		// RPC dispatch.
		"rpc.task_claim",
		"rpc.workspace.list",

		// Git subprocess (Tier L4).
		"git.push",
		"git.pull",
		"git.fetch",
		"git.rebase",
		"git.status",
		"git.commit",
		"git.rev-parse",
		"git.unknown",

		// WebSocket lifetime (Tier L1) — handshake and disconnect only.
		"ws.upgrade",
		"ws.disconnect",

		// Server-Sent Events lifetime (Tier L1) — handshake and disconnect only.
		"sse.handshake",
		"sse.disconnect",
	}
	for _, name := range positives {
		t.Run("good/"+name, func(t *testing.T) {
			if err := matchesAllowlist(name); err != nil {
				t.Errorf("expected name to pass the allowlist but it did not: %v", err)
			}
		})
	}

	negatives := []struct {
		name   string
		reason string
	}{
		// Workspace key interpolated into HTTP route.
		{"GET /api/v1/HAPPY/issues", "workspace key in route"},
		// Issue ID interpolated into HTTP route.
		{"GET /api/v1/HAPPY/issues/HAPPY-42", "issue ID in route"},
		// CLI command with an issue ID glued on.
		{"loom.cli.task.HAPPY-42", "issue ID in CLI verb"},
		// Workspace key glued onto the CLI verb.
		{"loom.cli.workspace.HAPPY", "workspace key in CLI verb"},
		// Hex span / trace ID in name.
		{"loom.task.4f9c2a8b1e0d6571", "hex ID in name"},
		// UUID in name.
		{"loom.task.550e8400-e29b-41d4-a716-446655440000", "UUID in name"},
		// Long random token.
		{"loom.cli.session.abcdef1234567890abcdef1234567890", "random token in name"},
		// Timestamp in name.
		{"loom.cli.run.2026-05-07T12:34:56Z", "timestamp in name"},
		// Free-form description (none of the kinds match).
		{"do the thing", "free-form description"},
		// HTTP server name without a route template (just method + raw key).
		{"GET HAPPY", "uppercase token after method"},
		// Redis command with key interpolated.
		{"redis.HGETALL workspace:ACME", "key glob suffix"},
		// pgx with raw SQL fragment.
		{"pgx.query SELECT * FROM issues", "raw SQL in name"},
		// service-shape but with hex ID jammed in.
		{"service.IssueService.Claim.4f9c2a8b", "hex tail on service span"},
		// The retired composite-store decorator must not be reintroduced.
		{"service.Store.Workspaces.Get", "retired composite-store span"},
		// Empty string.
		{"", "empty span name"},
		// Git span with branch name leaked in.
		{"git.push.feature-branch", "branch glued onto git verb"},
		// Git span with a remote URL leaked in.
		{"git.push https://github.com/foo/bar.git", "URL after git verb"},
		// Git span with refspec.
		{"git.push origin HEAD:refs/heads/foo", "refspec after git verb"},
		// WS span with route glued onto the verb.
		{"ws.upgrade./api/workspaces/HAPPY/terminal/ws", "route after ws verb"},
		// WS span with session id leaked in.
		{"ws.disconnect.session-abc-123", "session id after ws verb"},
		// SSE span with workspace key leaked in.
		{"sse.handshake.HAPPY", "workspace after sse verb"},
		// Unbounded SSE phase.
		{"sse.message", "phase outside the bounded enum"},
	}
	for _, tc := range negatives {
		t.Run("bad/"+tc.reason, func(t *testing.T) {
			if err := matchesAllowlist(tc.name); err == nil {
				t.Errorf("name %q should have failed (%s) but passed the allowlist",
					tc.name, tc.reason)
			}
		})
	}
}

// TestSpanNames_InMemoryExporterSmoke is the live-pipeline check. It
// installs a tracetest in-memory exporter as the global TracerProvider,
// drives a representative slice of the real span-emitting paths, and
// asserts every emitted name passes the allowlist. This catches drift
// where a production code path silently changes its span name to an
// out-of-contract value (e.g. somebody adds an issue ID to a span name).
//
// The smoke set covers:
//   - The CLI root span (`loom.cli`) and its post-rename form
//     (`loom.cli.<verb>`), produced by tracer.Start the same way
//     internal/cli/root.go does.
//   - The backend-invoke span (`loom.backend.<backend>.invoke_*`),
//     produced by tracer.Start with the same naming as
//     internal/cli/agent_invoker_tracing.go.
//   - The event-derived spans (`loom.task`, `loom.agent.lifecycle`),
//     driven through the singleton cli.AgentEventBus(t.Context()), which is the
//     real production wiring (otelexport subscribed to the bus).
//
// Span shapes that need a real stack (HTTP server middleware, pgx,
// Redis, RPC, projector) are not exercised here — the synthetic test
// above is the gate for those.
func TestSpanNames_InMemoryExporterSmoke(t *testing.T) {
	// Install in-memory exporter as global TracerProvider so both direct
	// tracer.Start calls AND the AgentEventBus singleton (which reads
	// otel.GetTracerProvider() at subscribe time) record into the same
	// place.
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// Re-route AgentEventBus storage to a tempdir to avoid touching
	// ~/.loom (the JSONL writer side-effect).
	t.Setenv("LOOM_EVENTS_DIR", t.TempDir())

	// 1. Drive the CLI root span shape directly. internal/cli/root.go
	//    starts `loom.cli` then renames to `loom.cli.<os.Args[1]>`.
	//    Recreate that here.
	cliTracer := tracing.Tracer("github.com/tysonthomas9/loomcli/internal/cli")
	rootCtx, rootSpan := cliTracer.Start(context.Background(), "loom.cli")
	rootSpan.SetName("loom.cli.plan")

	// 2. Drive the backend-invoke span shape directly. The production
	//    code in internal/cli/agent_invoker_tracing.go composes the
	//    name as `loom.backend.<backend>.invoke_<mode>`.
	backendTracer := tracing.Tracer("github.com/tysonthomas9/loomcli/internal/cli/backend")
	_, backendSpan := backendTracer.Start(rootCtx,
		"loom.backend.claude.invoke_non_interactive")
	backendSpan.End()

	// 3. Drive the event-derived path through the real cli.AgentEventBus
	//    so otelexport's `loom.task` span gets emitted by the real
	//    subscriber. This is the same wiring exercised by
	//    TestAgentEventBus_EmitsLoomTaskSpanUnderActiveContext.
	bus := cli.AgentEventBus(rootCtx)
	if bus == nil {
		t.Fatalf("cli.AgentEventBus(t.Context()) returned nil")
	}

	claimEv, err := events.NewEvent(events.TaskClaimed, "happy-worker", "task", "EPIC-1",
		events.TaskClaimedData{TaskID: "HAPPY-2", Title: "cardinality smoke"})
	if err != nil {
		t.Fatalf("NewEvent claim: %v", err)
	}
	if err := bus.Emit(claimEv); err != nil {
		t.Fatalf("bus.Emit claim: %v", err)
	}
	completeEv, err := events.NewEvent(events.TaskCompleted, "happy-worker", "task", "EPIC-1",
		events.TaskCompletedData{TaskID: "HAPPY-2", Duration: events.Duration{Duration: 0}})
	if err != nil {
		t.Fatalf("NewEvent complete: %v", err)
	}
	if err := bus.Emit(completeEv); err != nil {
		t.Fatalf("bus.Emit complete: %v", err)
	}

	rootSpan.End()
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	got := exp.GetSpans()
	if len(got) == 0 {
		t.Fatalf("in-memory exporter saw no spans; smoke harness misconfigured")
	}

	// Confirm we actually got the span shapes we expected, otherwise the
	// "every name passes" assertion is vacuous (you can pass with zero
	// spans of a kind).
	wantPresent := map[string]bool{
		"loom.cli.plan": false,
		"loom.backend.claude.invoke_non_interactive": false,
		"loom.task": false,
	}
	for _, s := range got {
		if _, ok := wantPresent[s.Name]; ok {
			wantPresent[s.Name] = true
		}
		if err := matchesAllowlist(s.Name); err != nil {
			t.Errorf("emitted span violates cardinality contract: %v", err)
		}
	}
	for name, seen := range wantPresent {
		if !seen {
			gotNames := make([]string, 0, len(got))
			for _, s := range got {
				gotNames = append(gotNames, s.Name)
			}
			t.Errorf("expected span %q in the smoke set but it was not emitted; got: %v",
				name, gotNames)
		}
	}
}
