# Observability docs

> **Status:** Current · *audited 2026-07-23*

Three files, with a precedence order that readers get wrong. Read this first.

| Doc | Kind | Authority |
|---|---|---|
| [tracing-contract.md](./tracing-contract.md) | **Normative** | Source of truth for span names, attribute keys, propagation, PII policy. Instrumentation PRs are reviewed against it. Everything else defers to it. |
| [tracing.md](./tracing.md) | Operational how-to | Turning tracing on locally, the coverage matrix, known gaps. Where it disagrees with the contract on a name or key, **the contract wins**. |
| [events-tracing-spike.md](./events-tracing-spike.md) | Closed decision record | Why event spans inherit their originating trace (Option A). Shipped 2026-05-07. Historical — do not implement from it. |

The contract has a mirror in the **fleet-db repo** at
`fleet-db/docs/observability/tracing-contract.md`, which points back here. Keep
the two in lockstep, including the `semconv` version pin
(`go.opentelemetry.io/otel/semconv/v1.26.0`,
`internal/observability/tracing/tracing.go:28`).

## Known contradiction

`tracing-contract.md` §3 gives the Postgres span name as `pgx.<op>`;
`tracing.md`'s coverage table says `pg.query`. Both describe fleet-db, which is
not in this tree, so neither can be checked here. **Unresolved** — settle it
against fleet-db and fix whichever is wrong.

## Where the code is (this repo)

| Concern | Path |
|---|---|
| TracerProvider init, resource attrs, sampler, exporter | `internal/observability/tracing/tracing.go` |
| Per-invocation CLI tracing + service-name routing | `internal/cli/root.go:223-277` |
| Span-name cardinality allowlist test | `internal/observability/tracing/cardinality_test.go` |
| HTTP route-template span naming (mirrors the Prometheus labels) | `internal/webui/otel_tracing.go:11-40` |
| Event → span export | `internal/events/otelexport/` |
| Trace context on events | `internal/events/trace_context.go` |
| Git subprocess spans | `internal/cli/git_runner_tracing.go` |
| SSE / WebSocket lifetime spans | `internal/webui/server/realtime/handler.go`, `internal/webui/handlers/terminal/agent.go` |

## Cross-repo path convention

Docs here cite fleet-db paths in the same bare `internal/...` form as loomcli
paths, which sends grep-driven readers into the wrong tree. Mark them
**`(fleet-db repo)`** inline. `internal/api/`, `internal/projection/`,
`internal/resilience/` and `internal/storage/` do not exist in loomcli.

## Related

- [../README.md](../README.md) — index of the whole `docs/` tree
- [../loom-glossary.md](../loom-glossary.md) — what `fleet-db`, `daemon`,
  `agent` and `session` mean here
- [../security.md](../security.md) — the PII surface these spans must not carry
- [../agents/domain.md](../agents/domain.md) — which domain docs to read first
