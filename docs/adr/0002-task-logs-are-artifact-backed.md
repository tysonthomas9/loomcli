# ADR 0002: Task Logs Are Artifact-Backed

## Status

Accepted — 2026-08-15

## Context

Task-run and driver-run output previously had a filesystem pathway under `.loom`.
That split log ownership across runtime files and the control plane, complicated
serving, and made durable run records insufficient to locate their log artifact.

In Loom vocabulary a journal is an agent instance's working-memory file, not a log.
It has a different lifecycle and remains workspace-local.

## Decision

Task-run and driver-run logs live only as immutable, per-attempt content artifacts
owned through `taskrunlogs`. Each successful write returns an artifact reference;
that persisted reference on the run record is the source of truth for serving and
availability.

The filesystem log pathway was deleted, not wrapped, and there is no file fallback.
Existing file-only logs are not backfilled and deliberately go dark. Log content is
tail-capped to 1 MiB on write. The agent journal endpoint remains file-based because
the journal is working memory rather than run output.

## Consequences

- Retries get distinct immutable log artifacts instead of overwriting earlier
  attempts.
- Old runs without artifact references use the existing no-log state.
- There is no migration or dual-read compatibility path for historical file logs.
- Fleet-db stores at most the final 1 MiB of each attempt's log content.
- Journal storage and serving remain intentionally separate from task/run logs.
