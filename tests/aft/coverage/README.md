# AFT scenario map

`scenario-map.yaml` is the durable, human-reviewed map from Loom product
surfaces to deterministic AFT scenarios and known gaps. It is intentionally
semantic: rendering a component, seeing a test id, or issuing one HTTP method
does not by itself prove a behavior.

The default deterministic corpus is the PR gate. It runs each graph leaf in a
fresh browser, even when its source steps are shared with sibling leaves.
Graph-scoped setup and teardown may be reused by the package, but browser and
scenario state may not leak across executions.

## Status meanings

- `covered`: a deterministic scenario proves the user-observable outcome.
- `partial`: a scenario reaches the surface but omits an important branch.
- `missing`: the product path is mounted and deterministically testable, but no
  AFT scenario proves it.
- `implementation-gap`: the intended product path is not mounted or wired, so a
  faithful browser scenario cannot yet pass.
- `lower-level`: the invariant belongs in Go, frontend unit, protocol, or
  security tests rather than browser AFT.
- `opt-in-live`: the proof requires a paid/external provider or mutates an
  external system and stays outside the default corpus.

The map is not generated from the storyboard census. The census is useful for
reachability discovery, but its route, component, endpoint, and test-id totals
must not be presented as behavioral coverage.

## Validation

`make check-product-invariants` validates the map alongside the product-truth
registry. It checks the strict YAML shape, self-consistent execution arithmetic,
story and gap references, repository paths, unique IDs, family status
consistency, and known failure selectors. For graph scenarios, a known-failure
selector may be pinned to an imported file with `selectorSource`; the validator
confirms that file is actually reachable from the graph's imports.

The validator does not decide whether a scenario semantically proves a claim or
whether a gap has the right priority. Those remain human-reviewed judgments.
It also does not compile the current AFT corpus, require every default suite to
appear in the map, or open the report named by `observedBaseline.runId`. Before
updating execution totals or known failures, compile every graph and count the
linear suites, compare all default entrypoints with `families[].scenarios`, and
verify the named report's product/surface and pass/fail totals. A baseline from
before a graph migration is not evidence for the migrated graph, even when the
legacy and graph executions have the same names and total count.

The default harness runs `scripts/validate-default-graph-corpus.mjs` before AFT.
That compiler-backed guard pins the 108 graph and 44 linear executions, unique
case identities, save-before-use variables, and the descendant counts of every
meaningful shared trunk. Update its expectations intentionally when a scenario
or graph topology changes; do not make the guard infer its expected result from
the graph it is checking.

Use `missing` only when the full browser path is mounted and could pass once an
AFT scenario is added. Use `implementation-gap` when a component, route, field,
or external side effect is absent or unwired. If a capability is both unwired
and eventually requires a credentialed provider proof, record two gaps: the
implementation gap in the default product plan and the later `opt-in-live`
proof.

## When a suite should be a graph

Use a graph when two or more executions replay the same meaningful product
state transition. A graph may branch, share another trunk, and branch again;
each leaf still starts a fresh browser and replays every transition on its own
root-to-leaf path. Use `fixtureScope: graph` only for bootstrap data that no
leaf mutates for a sibling. Stateful prerequisites belong on the path, usually
with `fixtureScope: path` and a case-isolated identity.

For product resources with short or normalized identifiers, use the
compiler-owned `${AFT_CASE_KEY}`. It is a 12-character, uppercase, run-scoped
operational key whose leading `Cnn` or `Gnn` identifies the planned path.
`${AFT_CASE_ID}` remains the durable semantic/reporting identity; do not copy
either key's hashing or normalization logic into a product suite.

Keep a suite linear when its only common prefix is a blank-browser assertion,
one route open, or another trivial scaffold. Turning those suites into graphs
adds schema without reducing reviewer work. The Flow view may collapse moments
that come from the same shared transition and screenshot source, but independent
executions remain independently runnable and reportable.
