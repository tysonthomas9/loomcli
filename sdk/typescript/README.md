# `@loom/sdk` — TypeScript control-plane SDK

Typed client for the loom control plane (`loom serve`). It lets a Flue runner
**read its task and report results via typed calls** instead of receiving a JSON
blob and emitting NDJSON. See `docs/product/loom-typescript-sdk-spec.md` for the
product rationale, phasing, and Definition of Done.

The runner is given only a scoped **bootstrap** (`LOOM_SERVER_URL` + a token +
task/session/fencing ids); everything else is pulled/pushed through the
`TaskRunClient`. The runner reaches **only `loom serve`** — never fleetdb
directly.

## Usage

```ts
import { TaskRunClient } from "@loom/sdk";

const run = TaskRunClient.fromEnv(); // reads LOOM_SERVER_URL / LOOM_TASK_ID / …

const task = await run.getTask();    // title, description, design, acceptance criteria
// … implement the change in the working directory …
await run.comment("done — created DAYTONA_DEMO.md");
await run.complete({ reason: "implemented per design" });
```

## Status (by PRD phase)

| Method | Phase | State |
|---|---|---|
| `getTask()` | B (read path) | ✅ wired — `GET /issues/{id}` |
| `comment()`, `updateStatus()`, `complete()`, `block()`, `fail()` | B/C control | ✅ wired — `POST /comments`, `PATCH /issues/{id}`, `POST /close` |
| `postArtifact()`, `recordUsage()`, `appendLog()`, `heartbeat()` | C/D | ⛔ `NotImplementedError` — pending the loom-serve write surface + scoped-token/fencing auth |

`fromBootstrap` already sends `Authorization: Bearer`, an `X-Loom-Fencing-Token`
header, and (for local fleetdb dev mode) `X-Actor`; the server-side scoped-token
minting + fencing enforcement is Phase C.

## Generated types

`src/generated/openapi.ts` is generated from `api/openapi.yaml` (the single
source loom also uses for Go types and the web UI), via `openapi-typescript`.

```bash
npm install
npm run generate      # regenerate from api/openapi.yaml
npm run typecheck     # tsc --noEmit
npm run check:generated   # fail if committed types are stale (CI gate)
```

The committed `src/generated/openapi.ts` must stay in sync with the spec;
`check:generated` enforces it (wired into `make` alongside the frontend check).
