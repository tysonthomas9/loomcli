# Phase 10.1 Generated HTTP Contract Evidence

- **Status:** Implemented and focused proof complete
- **Date:** 2026-08-12
- **Parent decision:**
  [Phase 10 consolidated deep-module goal](20-phase-10-consolidated-deep-module-goal.md)
- **Stack:** 10.1 of 10.12
- **Implementation commit:** `0a1621ec1`

## Outcome

OpenAPI is now the only public transport schema for the migrated Agents, PR
Review, task workflow-history, file, worker, and terminal-tab routes. The Go
handlers emit and decode generated models, and the frontend consumes the
TypeScript types generated from the same schema. Transport-neutral owner input
structures remain private and have no JSON tags.

The obsolete `internal/webui/server/dto` package and its duplicate validation
and projection tests are deleted without a forwarding package or compatibility
alias. The production-package topology shrinks from 158 to 157 packages, from
141 to 140 packages outside `internal/modules`, and from 42 to 40 one-file
packages. The one-or-two-file ceiling remains 60.

## Migrated contract surfaces

- unified Agent list, lifecycle, creation, patch, run history, transcript, and
  built-in interactive prompts;
- PR detail, diff, review submission/result, reviewer creation, messages, and
  conversation snapshots;
- task workflow-run history and the shared legacy DriverRun read projection;
- scoped file writes and repository qualification;
- worker registration and state mutation; and
- terminal-tab create and patch requests.

The DriverRun schema now includes the already-shipped `subject_key` and
`await_instance_key` fields. This makes the canonical schema complete instead
of narrowing the response while changing its producer.

## Failure behavior and ratchets

Generated request models are decoded with unknown-field rejection at migrated
write boundaries. Malformed, trailing, oversized, foreign-kind, and unknown
field inputs return explicit 4xx responses and do not partially mutate owner
state. Existing request-path tests cover those cases for interactive and
prompt Agents, workers, terminal tabs, native workflows, and session writes.

`phase10_http_contract_test.go` prevents the deleted DTO package and the
migrated handwritten transport structs from returning. The package-shape
inventory records the deletion. Go and TypeScript staleness checks regenerate
from `api/openapi.yaml` and fail when checked-in output differs.

## Proof

The following passed:

```text
./scripts/check-go-api-staleness.sh
cd internal/webui/frontend && npm run check:generated
cd internal/webui/frontend && npm test -- --run
go test ./internal/webui/handlers/agents
go test ./internal/webui/handlers/misc ./internal/webui/handlers/terminal
go test ./internal/webui/handlers/prreview ./internal/webui/handlers/workflows
go test ./internal/webui/app
go test -race ./internal/archtest -run 'TestPhase10|TestCheckedInProductionPackageShape|TestGeneratedRequiresCanonicalGoMarker' -count=1
go run ./scripts/archcheck check
```

The architecture checker covered all 11 build profiles and reported a peak
tree RSS below the 2 GiB limit in the full gate. Exact handler import-fanout
budgets remain unchanged: Agents is 18 and PR Review is 15.

The full `make gate` reached and passed generation, format, vet, build, lint,
LOC, package size, import fanout, architecture, characterization, and the
affected race suites. Its remaining failure is an existing cross-worktree
fixture mismatch: this Loom branch contains the newer FleetDB OpenAPI snapshot
while the adjacent `fleet-db` checkout is on an older `unified-agents` API.
Phase 10.1 does not rewrite that snapshot backward or include an unrelated
FleetDB contract mutation.

## Next stack

Stack 10.2 deepens Artifacts evidence policy, private parsing adapters, Run
Capture, and Transcript Evidence, and then deletes the superseded Sessions
paths after authorization, redaction, bounds, truncation, finalization,
failure-state, restart, and UI evidence proof.
