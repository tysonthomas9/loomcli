# FleetDB action contract provenance and CI

The canonical action and entity mapping remains FleetDB's `internal/models/event.go`.
Loom's generator parses that source and emits the consumer parity-test rows. This
gate covers action mapping; it does not certify the complete SSE wire protocol,
projection correctness, or runtime deployment compatibility.

`internal/backend/fleet/fleet_action_contract_producer.json` records the exact
producer repository, full commit SHA, canonical source path, and SHA256 of the
formatted generated contract. The generated Go file contains no upstream revision
or timestamp. An unrelated producer commit therefore leaves its contract bytes
unchanged. The hash includes generated type declarations and sorted action rows;
changing generator output requires an explicit regeneration and review.

Generation reads the pinned Git object's source, even if the selected FleetDB
checkout has another HEAD or dirty files. Missing objects fail; the generator
does not silently fall back to HEAD or working-tree sources. This is provenance
for a **committed contract**, not proof that a dirty producer runtime was tested.
A paired runtime test runner must reject dirty producer builds or explicitly
record their uncommitted diff and resulting build identity. This gate alone must
never be cited as certification of that runtime.

From the Loom repository root:

```sh
# Reproduce/check the existing pinned contract using a repository containing it.
go run ./internal/backend/fleet/internal/gen -fleet-db /path/to/fleet-db -check

# Independently compare that checkout's committed HEAD with consumer action rows.
go run ./internal/backend/fleet/internal/gen -fleet-db /path/to/fleet-db -check-upstream

# Explicitly move the pin; review the manifest and generated rows together.
go run ./internal/backend/fleet/internal/gen -fleet-db /path/to/fleet-db \
  -update-producer -revision FULL_40_CHARACTER_COMMIT_SHA
go test ./internal/backend/fleet -run TestFleetActionContractParity
```

`go generate ./internal/backend/fleet/...` regenerates from the existing pin and
verifies its hash. It cannot implicitly adopt new actions from a sibling checkout.

CI first validates the manifest and checks out its pinned producer commit, then
verifies the generated contract and hash. A separate checkout and check compare
FleetDB `main` at the revision GitHub checked out. Logs record the checked commit
and semantic hash. The existing job name is retained to preserve branch-protection
check identity. Missing repository-read credentials fail before checkout, and
checkout permission errors also fail; neither condition becomes a green skip.

Both comparisons currently belong to one required job. A coordinated candidate
that adds an action ahead of FleetDB `main` therefore fails the upstream equality
check until that producer change lands. Before the first such rollout, separate
baseline drift tracking from candidate-pair certification; do not weaken the
pinned producer check or silently accept stale action rows.

External blockers recorded for this milestone: the parent review confirmed that
the private-repository read token is unavailable and GitHub organization billing
blocks execution. This change does not provision credentials or alter billing.
Local deterministic generator/access tests prove gate behavior, not successful
hosted CI execution. Hosted verification remains required once those blockers
are resolved.
