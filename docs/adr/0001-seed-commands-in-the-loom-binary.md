# Seed commands ship inside the loom binary, gated by an env var

Browser and cross-process test harnesses need to create agent artifacts (worktrees, logs, session records) that are indistinguishable from what the runtime produces; before this decision they fabricated paths by hand in shell and TypeScript, which drifted from the real layout. We decided the seeding seam is a family of hidden `loom daemon seed-*` CLI subcommands (extending the existing `seed-transcript`), running the product's own creation flows (full fidelity — worktree registration, session finalize included) and refusing to run unless `LOOM_TESTSUPPORT=1` is set.

## Considered Options

- **Test-only HTTP endpoints** — reachable cross-container without shell access, but they put test surface into the served API, which then needs guarding from production traffic. Rejected.
- **Go test-support package only** — cannot cross the process/container boundary to the aft and Playwright harnesses, which is the original problem. Rejected.
- **Build-tagged test binary** — strongest isolation, but every consumer (aft stack build, podman images, desktop dev loop) would have to build and distribute a variant binary. Rejected.

## Consequences

- Test-only code ships in the production binary; the env gate (plus hidden help) is the guard, applied retroactively to `seed-transcript`.
- Layout knowledge stays with its owners: seed commands compose `localworkspace`, `webui/log`, and `sessions.Store` rather than duplicating paths (see CONTEXT.md: layout owner, seeding seam).
- No new umbrella package: the CLI command group is the composition point over the three layout owners.
