# Checked-in docs drift from the code, and the course must teach that

Three verified cases where a checked-in document in loomcli disagrees with the
code at `ce4df56fb`. Each was confirmed by opening the code, not inferred.

1. **`docs/testing-terminology.md` does not exist.** `AGENTS.md` cites it twice as
   the canonical map of the four-axis testing vocabulary and the trap words. A
   repo-wide search for `realness` matches `AGENTS.md` alone.
2. **The default AI backend is `codex`, not `claude`.** `ResolveBackendName()`
   falls back to `backendnames.Codex` at `internal/cli/backend.go:84-94`;
   `README.md:261` states the default is `claude`.
3. **`loom serve` alone serves no web UI.** `registerFrontendRoutes` returns
   immediately when `FrontendDir` is empty, which is the default
   (`internal/webui/app/frontend.go:11-16`). Non-`/api` paths fall through to Go's
   plain-text 404; the frontend is expected to be served externally
   (`internal/webui/app/routes.go:26-30`).

## Implications

- **This is a teachable skill, not an errata list.** A new hire's default posture
  toward a checked-in doc should be "strong evidence of intent, weak evidence of
  current behaviour". Lesson 01 introduces that idea and Lesson 02 gives it a live
  example; later lessons should keep reinforcing it rather than presenting docs as
  ground truth.
- **The verification habit generalises**: when a rule matters in this repo, the
  team's pattern is to mechanise it in `scripts/check-*.sh` or `.golangci.yml`.
  So "is this enforced?" is answered by grepping `scripts/`, not `docs/`. That
  ordering — code, then guards, then docs — is worth teaching explicitly.
- **Every lesson must be commit-stamped.** Because drift is normal here, an
  unstamped claim is unfalsifiable later. All lessons and reference cards carry
  the pinned loomcli/fleet-db SHAs in a footer.
- Each of these three is worth filing as an issue in its own right. That is a
  good first real task for the new hire — it is small, verifiable, and teaches the
  issue workflow on something they already understand.
