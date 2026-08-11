# Changelog

All notable changes to `@loom/sdk`. Semver per the policy in README
("Versioning and breaking-change policy"): wire camelCase and error-envelope
changes require a major.

## 1.0.0 — unreleased

First publishable cut. Not yet on npm (publish deferred pending @loom scope
verification; decided fallback name: `@browseroperator/loom-sdk`).

- Frozen v1 driver-op surface, shipped as `api-surface.v1.json` and enforced
  by `contract.test.mjs` (client) and server-side contract tests: 20 op paths,
  camelCase wire fields, error envelope `{code, message, retryable, details?}`
  with a 25-code union, `token_expired` pinned never-retryable.
- `@loom/sdk/driver`: `LoomDriverClient` with `epics` (incl. `watch` SSE),
  `agents`, `tasks`, `taskRuns`, `connectors` (github/slack/datadog +
  `dispatch`), `events.await`/`events.list`, `workflows.start`/`workflows.await`,
  `WorkflowSuspended` suspension signal, terminal result helpers.
- `@loom/sdk/runner`: `TaskRunClient`, `ArtifactHandle`, `RunnerEnv`,
  `LoomAPIError`.
- Token-only auth: required run-scoped `LOOM_RUN_TOKEN` bearer (TTL = max run
  duration, default 24h; revocation via fenced-run verification). The SDK does
  not expose a shared-bearer or identity-header fallback.
- Published TypeScript types for the full surface; strict node16 typecheck
  gate (`tsc -p tsconfig.typecheck.json`) wired into `npm test`.
- Vendoring guarantee: `driver.js` single-file with zero local imports;
  `runner.js` imports only `internal.js` (pinned by `package.test.mjs`).
