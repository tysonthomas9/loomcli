# Loom Docs

> **Status:** Current index · *written 2026-07-23*. Per-doc status lives in each
> doc's own banner and in the per-directory README linked below; there is no
> tree-wide status.

Front door for the `docs/` tree. These are internal docs for contributors and
coding agents.

## Start here

1. **[`loom-glossary.md`](loom-glossary.md)** — mandatory. This repo uses
   ordinary words as specific concepts: `loom`, `flue`, `fleet`, `aether`,
   `codex`, `daytona`, `atlas`, `claude`, `stack`, `lead`, `worker`, `session`,
   `backend`. Using one in its general-knowledge sense is a defect. `AGENTS.md`
   requires this read.
2. **[`testing-terminology.md`](testing-terminology.md)** — mandatory before
   running anything slow or irreversible. Defines the four axes
   (depth / realness / provisioning / polarity), the trap words (`local`,
   `live`, `real`, `verify`, `gate`), and the terminology-handshake protocol
   that `AGENTS.md` enforces.
3. **[`agents/domain.md`](agents/domain.md)** — how to consume these docs when
   exploring the codebase, and why there is no `CONTEXT.md` or `docs/adr/`.

Then read the directory index for the area you are working in.

## Directories

| Directory | What it is for | Index |
|---|---|---|
| `agents/` | Runbooks for coding agents: which docs to read, and how the fleet-db issue tracker works. | [`agents/README.md`](agents/README.md) |
| `arch/` | As-built architecture of shipped web-UI subsystems — components, handlers, data flow. Descriptive. | [`arch/README.md`](arch/README.md) |
| `design/` | This repo's ADR equivalent. Dated files are decision records; undated ones are living subsystem designs. Mixed status — read banners. | [`design/README.md`](design/README.md) |
| `epics/` | Written plans behind multi-PR epics. Live epic state is in fleet-db, not here. | [`epics/README.md`](epics/README.md) |
| `observability/` | Tracing: the normative contract, the local how-to, and one closed decision record. Has a stated precedence order. | [`observability/README.md`](observability/README.md) |
| `product/` | Product specs plus three descriptive reference docs that outrank the specs when they disagree. | [`product/README.md`](product/README.md) |
| `reference/` | Generated reference: every `LOOM_*` env var, the full CLI command tree, the package-layer import graph. **Never hand-edit** — edit the preambles. | [`reference/README.md`](reference/README.md) |
| `testing/` | Test surfaces, runbooks, manual E2E plans, and the fleet-db acceptance gates. | [`testing/README.md`](testing/README.md) |
| `design/cortex-v7/` | UI screenshots (2026-07-07) with no accompanying spec. | [`design/cortex-v7/README.md`](design/cortex-v7/README.md) |

## Top-level files

| Doc | Purpose | Status |
|---|---|---|
| [`loom-glossary.md`](loom-glossary.md) | The dictionary of overloaded terms, each pinned to `path:line`. Not an architecture narrative — that is what `arch/` and `design/` are for. | Current |
| [`testing-terminology.md`](testing-terminology.md) | The testing vocabulary and handshake protocol. Companion to `testing/README.md`. | Current |
| [`security.md`](security.md) | Contributor-facing security posture: credential storage, auth modes, IPC, subprocess env policy, SSRF. **Owns auth-mode policy**; per-endpoint auth requirements belong to `api.md`. | Current |
| [`api.md`](api.md) | Reference for the WebUI HTTP API served by `loom serve`. **Generated — never hand-edit.** | Generated |
| [`api.preamble.md`](api.preamble.md) · [`api.appendix.md`](api.appendix.md) | The hand-written prose spliced into the top and bottom of `api.md`. Edit these, not `api.md`. | Hand-written source |

## Generated files

`docs/api.md` is produced by `scripts/openapi-to-md` from `api/openapi.yaml`
plus the preamble and appendix above. To change it:

```sh
# edit api/openapi.yaml (endpoint facts) or docs/api.preamble.md / docs/api.appendix.md (prose)
make gen-api-docs
```

`make check-api-docs-staleness` fails the gate when the committed file does not
match its inputs. The generator also scans the Go route registrations under
`internal/webui` and emits a coverage appendix, so a route added in Go without a
spec entry shows up as a visible gap instead of silent drift. The spec alone is
**not** a complete description of the served surface — the appendix in `api.md`
quantifies the difference.

`internal/backend/api/gen/types.gen.go` is likewise generated from
`api/openapi.yaml` via `make gen-go-api`.

`docs/reference/{env-vars,cli,architecture}.md` are produced by
`scripts/loomdoc` from git-tracked Go source — the `os.Getenv` call sites, the
assembled cobra tree, and the depguard rules plus the compiled import graph.
Each splices in a hand-written `*.preamble.md`; edit those, not the generated
pages. `make docs-gen` regenerates all three **and** `docs/api.md`;
`make check-loomdoc-staleness` gates them.

The exclusion filter is **gitignored**, not "uncommitted": a brand-new `doc.go`
is counted before you commit it, so a dirty tree does not silently drop packages
(`scripts/loomdoc/common.go:59-67`). Adding a path to `.gitignore` *does* remove
it from these pages. See [`reference/README.md`](reference/README.md).

## Status vocabulary

Banners use the greppable form
`> **Status:** <value> · *audited YYYY-MM-DD*`.

| Value | Means |
|---|---|
| Current | Verified against the tree on the audit date. |
| Implemented / Shipped | The change described landed; the doc records it. |
| Partially implemented | Some of it shipped; the doc says which parts inline. |
| Aspirational / Future plan / Proposed / Draft | Not built. Do not read as behaviour. |
| Superseded | A named successor exists. Historical only. |
| Stale snapshot | Point-in-time analysis that has not been re-run. |
| Pointer | The content lives elsewhere; the file is a redirect. |
| *(no banner)* | Never audited. Treat every claim as unverified. |

Find every unaudited doc — the pattern is anchored, so a doc that writes the
banner some other way counts as unaudited and gets fixed:

```sh
for f in $(find docs -name '*.md'); do
  head -20 "$f" | grep -qE '^> \*\*Status:\*\*' || echo "$f"
done
```

As of 2026-08-03 the hits are the generated pages and their hand-written
partials — `api.md`, `api.preamble.md`, `api.appendix.md`, and the six files
under `reference/` — whose status lives in this index and in
[`reference/README.md`](reference/README.md) instead, plus
`design/2026-07-22-lead-resume-defects.md`, which is owned by an in-flight
change. Anything else the command prints is a doc that needs a banner.

## Conventions

- Every claim about behaviour carries a `path/file.go:123` citation. A claim
  without one has not been checked — verify before relying on it.
- Where two docs cover the same subject, one of them says it is canonical and
  the other points at it. If you find a pair that does not, that is a defect.
- Descriptive docs outrank specs on questions of current behaviour. `product/`
  states this explicitly for its three reference docs.
- Cross-repo paths (fleet-db, flue) are marked inline, because a bare
  `internal/...` path sends grep-driven readers into the wrong tree.

## Outside `docs/`

- `AGENTS.md` — shared agent instructions: the glossary read, the terminology
  handshake, generated-file rules, gate environment, driver-runtime auth and
  sandbox deploy notes.
- `CLAUDE.md` — repo rules and pointers into `docs/agents/`.
- `.agent-skills/loom-pr-test/SKILL.md` — real Loom PR runtime testing runbook.
- `test/*/README.md`, `e2e/README.md` — per-harness runbooks, indexed by
  [`testing/README.md`](testing/README.md).
- `sdk/README.md` — the authoritative TypeScript SDK docs, which
  [`product/loom-typescript-sdk-spec.md`](product/loom-typescript-sdk-spec.md)
  points at.
