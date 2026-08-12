# Lead agent scope response contract research

Status: implementation recommendation for the follow-up to PR #365  
Date: 2026-08-11  
Scope: the `repos`, `repo_groups`, and `cross_repo` data path used by workspace
agent creation and the agent-detail File Explorer

## Executive conclusion

The product decision is an atomic strict-contract migration with **no legacy
compatibility decoder**. The server must stop serializing `domain.Agent` as the
create response, and the frontend must consume the same generated OpenAPI
contract. Malformed or stale responses fail visibly at the API boundary rather
than being silently repaired into application state.

It is not yet the strongest long-term shape. The same HTTP model currently has
four independently maintained definitions (OpenAPI, generated Go, handwritten
Go DTO/ops types, and a handwritten TypeScript interface), and adjacent agent
routes still serialize `domain.Agent` directly. The fix is to make the
OpenAPI-generated `WorkspaceAgentInfo` the representation used at the create
HTTP boundary and in the frontend. Follow that with a separate, explicitly
scoped cleanup of list/update route contracts.

In short:

1. Keep the server-side projection from #365.
2. Replace the handwritten output types with the model generated from
   `api/openapi.yaml`.
3. Add `role_name` to the OpenAPI projection because it is already emitted and
   consumed.
4. Make response-contract tests compare the actual 201 payload with the
   OpenAPI-required fields.
5. Do **not** remove `omitempty` from `domain.Agent`; that makes an internal
   aggregate responsible for an HTTP projection and leaves future projections
   vulnerable to the same mistake.

## Question being answered

The observed crash was:

```text
TypeError: e.repo_groups is not iterable
```

It occurred after a Lead agent was created and optimistically inserted into the
workspace store. The research question is whether #365 is merely filling in
missing fields or whether it fixes the owned contract, and what code structure
would make a recurrence difficult.

## Primary-source inventory

### 1. The persisted/domain representation

`domain.Agent` is the long-lived assignment aggregate. It owns far more than
the workspace UI needs: identity, role, backend, lifecycle state, execution
configuration, hooks, timestamps, and derived live state. Its comment defines
empty `Repos` as all workspace repositories, while its three scope-related JSON
fields use `omitempty` ([source](../../internal/domain/agent.go#L493-L523)).

That combination is legitimate for an internal/persistence representation:
absence and the zero value carry useful compact-encoding semantics. It is not
legitimate evidence that an HTTP response property documented as required may
be absent.

FleetDB has another deliberate wire adapter, `agentWire`, which mirrors the
FleetDB model and converts to `domain.Agent`; its scope fields also use
`omitempty` ([source](../../internal/infra/fleetdb/agent.go#L15-L70)). Thus a
created agent with empty affinity naturally reaches the web service with nil or
empty slices. Nothing in the store contract promises an HTTP-ready JSON shape
([source](../../internal/store/agent_store.go#L9-L56)).

### 2. The public HTTP contract

The create operation declares a 201 response of `WorkspaceAgentInfo`
([source](../../api/openapi.yaml#L2318-L2375)). That schema requires `name`,
`repos`, `repo_groups`, and `cross_repo`; the two arrays have `type: array`
([source](../../api/openapi.yaml#L5569-L5586)). Generated Go and TypeScript
therefore make all four fields non-optional
([Go](../../internal/backend/api/gen/types.gen.go#L2980-L2986),
[TypeScript](../../internal/webui/frontend/src/types/generated/openapi.ts#L2923-L2930)).

The `default: []` annotations do not repair a missing property at runtime.
OpenAPI 3.1 Schema Objects inherit JSON Schema 2020-12 semantics, and JSON
Schema defines `default` as an annotation rather than a value-insertion step
([OpenAPI 3.1 Schema Object](https://spec.openapis.org/oas/v3.1.0.html#schema-object),
[JSON Schema annotations](https://json-schema.org/understanding-json-schema/reference/annotations)).
The producer must emit the required arrays.

The schema is itself incomplete: the server workspace projections contain
`role_name`, and the frontend uses it to distinguish configured planners and
populate agent placeholders, but OpenAPI and its generated types omit it
([ops projection](../../internal/ops/workspace.go#L45-L52),
[frontend use](../../internal/webui/frontend/src/components/WorkspaceTree/AgentSection.tsx#L48-L72),
[OpenAPI schema](../../api/openapi.yaml#L5569-L5586)). The handwritten
TypeScript interface adds `role_name` and `backend` on top of the generated
contract, which is direct evidence of contract drift
([source](../../internal/webui/frontend/src/api/workspace/workspace.ts#L36-L43)).

The containing workspace operation is also misdocumented: OpenAPI says both
workspace GETs return a direct `WorkspaceResponse`
([active](../../api/openapi.yaml#L205-L217),
[by ID](../../api/openapi.yaml#L275-L289)), while the handlers actually return
`{success,data}` ([source](../../internal/webui/handlers/workspace/workspace.go#L11-L28)).
The frontend therefore uses `openapi-fetch` but casts the inferred response
through `unknown` before unwrapping it, and create-agent uses the generic raw
`post<T>` helper instead of endpoint inference
([workspace GET](../../internal/webui/frontend/src/api/workspace/workspace.ts#L178-L194),
[agent POST](../../internal/webui/frontend/src/api/workspace/workspace.ts#L350-L360)).
Generated types exist, but these casts currently bypass their ability to catch
the contract mismatch at compile time.

### 3. Producers of workspace agent scope

There are three relevant server producers:

- `POST /api/workspaces/{ws}/agents` receives a full `*domain.Agent`. Before
  #365 it wrote that object directly. #365 maps it to the narrow
  `dto.WorkspaceAgentInfo` and explicitly creates non-nil arrays
  ([source](../../internal/webui/handlers/agents/handlers.go#L54-L98)).
- Workspace topology is built by `storeadapter.loadAgents`, which manually
  projects each domain agent into `ops.WorkspaceAgentInfo`
  ([source](../../internal/webui/storeadapter/storeadapter.go#L172-L187)). The
  workspace service later normalizes nil arrays before returning data
  ([source](../../internal/webui/service/workspace_impl.go#L626-L652)).
- `GET /api/workspaces/{ws}/agents` and
  `PATCH /api/workspaces/{ws}/agents/{name}` still write domain agents
  directly ([source](../../internal/webui/handlers/agents/handlers.go#L23-L31),
  [source](../../internal/webui/handlers/agents/handlers.go#L100-L123)). The
  OpenAPI file documents the GET as daemon `AgentControlEntry` objects and does
  not document the PATCH route at all
  ([source](../../api/openapi.yaml#L2295-L2317),
  [registered routes](../../internal/webui/handlers/agents/module.go#L22-L32)).

The last bullet is wider API debt. It did not trigger this crash, but it proves
that fixing only the `omitempty` tags would not establish an owned agent HTTP
contract.

There is also an unused/underused duplicate DTO family. `dto.WorkspaceResponse`
contains a third Go definition of `WorkspaceAgentInfo`
([source](../../internal/webui/server/dto/workspace_response.go#L3-L21),
[source](../../internal/webui/server/dto/workspace_response.go#L52-L60)), while
production workspace handlers wrap and serialize `*ops.WorkspaceData` instead
([source](../../internal/webui/handlers/workspace/workspace.go#L11-L28)). This
is why merely moving the mapper into the DTO package would not create one
authoritative representation.

### 4. Consumers and the failure path

The frontend declares scope arrays as required, matching OpenAPI, and File
Explorer consequently uses them without optional checks
([type](../../internal/webui/frontend/src/api/workspace/workspace.ts#L36-L43),
[consumer](../../internal/webui/frontend/src/components/FileExplorer/treeRoots.ts#L75-L90)).
That is a reasonable internal invariant.

The failing sequence was:

1. The epic-runner flow calls `createWorkspaceAgent`.
2. It immediately calls `upsertAgent` with the 201 response
   ([source](../../internal/webui/frontend/src/hooks/workspace/startEpicRunnerForIssue.ts#L101-L120)).
3. The workspace store keeps that optimistic object across stale workspace
   polls ([source](../../internal/webui/frontend/src/stores/workspaceStore.ts#L62-L85),
   [source](../../internal/webui/frontend/src/stores/workspaceStore.ts#L202-L225)).
4. File Explorer iterates `repo_groups`; a response produced from raw
   `domain.Agent` omitted the property, violating the TypeScript invariant.

#365 initially normalized both workspace GET data and create responses at the
API boundary. The strict-contract decision removes that decoder: the producer
and consumer move together, and File Explorer continues to rely on required
generated fields instead of repairing wire data.

### 5. Git history confirms this is a repeated contract-boundary failure

Commit `ae5d3965f` on `origin/aft-agent-coverage` is titled
`webui: fix agents-pane crash after agent create`. Its commit message records
the same chain: raw `domain.Agent`, omitted arrays, optimistic upsert, and File
Explorer iteration. It added a DTO mapper, store normalization, and defensive
consumer reads. That commit is not an ancestor of the current branch, so the
fix never became part of the current `v5` lineage (`git merge-base
--is-ancestor ae5d3965f HEAD` exits 1). Live first-party GitHub metadata on
2026-08-11 shows that commit only in still-open, conflict-marked
[PR #290](https://github.com/tysonthomas9/loomcli/pull/290), whose base is
`aft-product-correctness`, not `v5`.

This history matters because the recurrence was caused by parallel model
definitions and an unowned conversion seam, not by a one-off typo.

## Scope semantics discovered during the trace

The three fields should not be described as one boolean-plus-list access rule:

- `repos` and `repo_groups` are repo affinity. Empty affinity means unfiltered
  or all repositories
  ([domain comment](../../internal/domain/agent.go#L493-L509),
  [daemon resolver](../../internal/cli/config/repos.go#L8-L17)).
- `cross_repo` means the agent may pick up work spanning repositories; the
  config comment describes it as a capability, not another affinity selector
  ([source](../../internal/cli/config/project.go#L96-L116)).
- File Explorer expands explicit repos plus groups and treats empty affinity as
  all repos; it does not consult `cross_repo`
  ([source](../../internal/webui/frontend/src/components/FileExplorer/treeRoots.ts#L75-L90)).

There is an adjacent semantic inconsistency: local worktree creation treats
`CrossRepo=true` as all workspace repos, even when explicit affinity exists
([source](../../internal/localworkspace/localworkspace.go#L491-L530)), whereas
checkout enumeration and File Explorer use affinity only
([server checkout source](../../internal/webui/svcimpl/file_git_status.go#L287-L311)).
That should be resolved in a separate domain-semantics change. It should not be
silently decided by the serialization fix.

## Assessment of PR #365

### What is structurally correct

- It fixes the producer, not just the crashing `for ... of` loop.
- It uses a narrow workspace projection instead of exposing every
  `domain.Agent` field.
- `append([]string{}, values...)` guarantees JSON `[]` for nil and empty input.
- It fixes the frontend ingress and server producer together, so application
  state receives only the canonical generated shape.
- Its tests assert exact wire values (`[]`, `[]`, and `false`), not only Go
  zero values ([source](../../internal/webui/handlers/agents/handlers_test.go#L75-L103)).

### What remains weak

- The server mapper returns handwritten `dto.WorkspaceAgentInfo`, while the
  published source of truth already generates `gen.WorkspaceAgentInfo`.
- The frontend initially retained a handwritten normalizer, duplicating
  required defaults already owned by OpenAPI.
- `role_name` is preserved by the Go DTO and required by product behavior but
  absent from OpenAPI-generated clients.
- The list/update routes can still leak `domain.Agent`, so the unsafe pattern
  remains available next to the repaired route.
- The same workspace projection is defined in `ops`, `server/dto`, OpenAPI,
  generated Go, generated TypeScript, and handwritten TypeScript.

## Design alternatives

| Design | Depth | Locality | Contract strictness | Migration risk | Testability | Verdict |
|---|---|---|---|---|---|---|
| A. Remove `omitempty` from `domain.Agent` scope fields | Low | Poor: changes domain/persistence JSON to satisfy one HTTP projection | Risky for CLI/FleetDB consumers; still permits `null` slices | Medium and broad | Easy wire test, weak architectural guard | Reject |
| B. Keep #365 exactly as-is | Medium | Good around the failing route and frontend ingress | Weak: handwritten types and silent repair remain | Low | Good regression coverage | Reject: retains type drift |
| C. Normalize in workspace store and every UI consumer | Low | Poor: repair is downstream from the violation | Weak: malformed data becomes valid-looking state | Low initially; maintenance grows with consumers | Many repetitive tests | Reject |
| D. Generated HTTP projection, atomic producer/consumer update | High | Best: contract owner is OpenAPI; conversion stays at HTTP seam | Strong: one schema, no silent repair | Moderate, localized | Compile-time generated types plus exact wire tests | **Recommend** |
| E. Redesign all agent CRUD/list/control APIs into one resource | Highest | Potentially excellent | Needs an explicit version/migration plan because store-backed and daemon-only modes currently differ | High | Excellent after migration | Follow-up, not part of crash fix |
| F. Strictly migrate every workspace HTTP response to generated models now | Highest | Excellent after completion | Requires changing the `{success,data}` envelope contract and all clients together | High and cross-cutting | Excellent | Valuable migration, but too broad for #365 |

## Recommended implementation

### PR 1: strengthen #365 without broadening behavior

1. **Correct the published projection.** In `api/openapi.yaml`, add optional
   `role_name` to `WorkspaceAgentInfo`. Do not add `backend` unless
   `ops.WorkspaceAgentInfo` and `storeadapter.loadAgents` are also changed to
   preserve it after the next workspace poll. Regenerate:

   - `internal/backend/api/gen/types.gen.go` via `make gen-go-api`
   - `internal/webui/frontend/src/types/generated/openapi.ts` via
     `npm run generate:types`

2. **Use the generated Go response type.** Replace the handwritten return type
   of `workspaceAgentInfo` in
   `internal/webui/handlers/agents/handlers.go` with
   `gen.WorkspaceAgentInfo` (or move the mapper to an agent-handler-local
   `response.go`). This repository already uses `gen` response types in web
   handlers such as PR review, so the dependency direction has precedent
   ([source](../../internal/webui/handlers/prreview/reviewer.go#L200-L212)).
   Populate arrays with fresh non-nil slices. Populate `RoleName` according to
   the generated optional-field shape.

3. **Use the generated frontend type.** In
   `internal/webui/frontend/src/api/workspace/workspace.ts`, replace the
   handwritten interface with:

   ```ts
   export type WorkspaceAgentInfo =
     components["schemas"]["WorkspaceAgentInfo"];
   ```

   Keep the existing generic `post<T>` only if the 120-second timeout cannot be
   expressed through `openapi-fetch`; otherwise switch create-agent to the
   generated `api.POST` operation. Do not convert workspace GETs to strict
   endpoint inference in this PR, because their OpenAPI envelope is currently
   wrong and fixing it requires a coordinated server/client migration.

4. **Remove compatibility normalization.** Return workspace GET data and
   create-agent responses as the generated type. Keep `treeRoots.ts` strict.
   Missing required fields are a producer/schema violation and must not be
   defaulted in the API module, store, or consumer.

5. **Add contract-focused tests.** Keep the exact create-handler wire test and
   add:

   - non-empty `repos`/`repo_groups` preservation;
   - `role_name` preservation;
   - a small `api/openapi_workspace_agent_contract_test.go` that parses
     `openapi.yaml` with the repository's existing `gopkg.in/yaml.v3` and
     asserts that the create 201 references `WorkspaceAgentInfo` and that its
     required fields are exactly present;
   - frontend tests for the current generated payload;
   - compile-time use of `WorkspaceAgentInfo` at every public workspace-agent
     API boundary.

6. **Run generated-type gates.** The repository already checks Go and
   TypeScript generated files for staleness
   ([Go gate](../../scripts/check-go-api-staleness.sh),
   [frontend gate](../../internal/webui/frontend/scripts/check-openapi-staleness.mjs)).
   Run those in addition to the focused handler/frontend tests and the browser
   hard-navigation reproduction.

### PR 2: eliminate the adjacent raw-agent escape hatches

Treat this as API cleanup, not as a hidden expansion of #365:

1. Decide whether `/api/workspaces/{ws}/agents` is an assignment-resource list
   or the daemon-control list. Today the runtime switches implementation by
   server mode while OpenAPI names only the daemon shape
   ([store-backed registration](../../internal/webui/app/server_modules.go#L112-L132),
   [daemon-only registration](../../internal/webui/app/server_modules.go#L133-L141)).
2. Document GET/PATCH/DELETE accurately in OpenAPI.
3. Add a generated `AgentAssignmentResponse` if those operations need the full
   assignment. Map from `domain.Agent` in handlers; never serialize the domain
   object directly.
4. Update `internal/cli/data/agents.go`, which currently decodes the route as
   `AgentControlEntry`, as part of that migration
   ([source](../../internal/cli/data/agents.go#L14-L23),
   [source](../../internal/cli/data/agents.go#L50-L87)).
5. Add route-registration-to-OpenAPI tests so new agent routes cannot remain
   undocumented.

### PR 3: strict workspace response migration

After agent CRUD is accurate, decide whether the public workspace contract is
the existing `{success,data,warnings?}` envelope or a direct resource. The
runtime and error handling already use the envelope, so the lowest-risk choice
is to document it as a reusable generated schema, update both workspace GET
operations, regenerate clients, remove the `as unknown as` casts, and then
delete the stale `dto.WorkspaceResponse` and handler-level
`NormalizeWorkspaceData` duplicates once references prove they are unused.
Doing this separately keeps #365 reviewable and avoids coupling a Lead crash
fix to every workspace mutation response.

### Separate follow-up: define repo-affinity semantics once

Introduce a small domain operation for expanding agent affinity from
`repos`/`repo_groups` against workspace repos, and have daemon routing,
worktree creation, checkout enumeration, and File Explorer fixtures share its
test vectors. Decide explicitly whether `cross_repo` widens affinity or only
permits multi-repo tasks. Current product/config text supports the latter, but
one local-worktree implementation behaves as the former. This is a correctness
decision and should not ride along with JSON-shape changes.

## Acceptance criteria for the better fix

- The create 201 response is constructed as the generated OpenAPI model.
- `repos` and `repo_groups` are always JSON arrays and `cross_repo` is always a
  JSON boolean.
- `role_name` exists in OpenAPI and generated clients.
- Frontend public API functions expose the generated workspace-agent type
  without handwritten defaulting or compatibility normalization.
- A payload missing required scope fields is not normalized into application
  state.
- A current payload preserves explicit repos, groups, `cross_repo`, and role.
- Generated Go/TypeScript staleness checks pass.
- The real browser hard-navigation reproduction remains green.
- PR 1 does not change the semantics of `cross_repo` or the list/update API.

## Final recommendation

Do not replace #365 with a domain-tag tweak. Amend it (or stack one focused PR
above it) so the server and frontend use OpenAPI-generated
`WorkspaceAgentInfo` and publish `role_name`. Per the subsequent product
decision, update producer and consumer atomically and remove the compatibility
decoder. Then take the raw list/update responses and repo-affinity semantic
inconsistency as two separate follow-ups.

That gives the crash fix the right long-term ownership: domain objects remain
domain objects, the API owns its projection, and generated clients share that
projection without a second compatibility contract.
