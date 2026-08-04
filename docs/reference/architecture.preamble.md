## About this document

This is the map of Loom's **internal package layers**: how `internal/…` is
partitioned and which imports may cross which boundary. Everything below this
preamble is generated — the rules come from the depguard section of
`.golangci.yml`, the package purposes from package doc comments, and the
cross-layer edges from the real compiled import graph. Regenerate with
`make docs-gen`; edit this preamble, not the generated body.

These boundaries are enforced, not advisory: `depguard` runs under `make check`
(the Go lint step) and fails the build when a package imports across a forbidden
edge. If the generated sections below ever disagreed with the code, CI would
already be red.

## Why the layers exist

The rules keep the dependency arrow pointing one way, `sdk → infra → web → cli`,
so the reusable core never reaches back up into whatever process embeds it. The
SDK layer — domain types, the issue-**backend** interface, the auth-aware HTTP
client — is a leaf that must not import infra, web, or CLI packages, so it can be
linked into a remote worker or a workflow bundle without pulling in the daemon,
the web server, or cobra. Infra may build on the SDK but not on web or CLI; the
web layer may use both but must not import CLI code.

"Backend" here means the **issue backend** — the pluggable issue-tracking data
layer (`internal/backend` interface, with `internal/backend/{api,fleet,agentipc,
mapping}` implementations). It is not the **AI CLI backend**
(`internal/cli/backends`, the `claude`/`codex`/… agent processes), which lives in
the CLI layer. See `docs/loom-glossary.md` for the other overloaded terms.

## Reading the generated sections

**Layer boundaries** and the per-rule detail are the enforced intent. **Actual
cross-layer imports** is the real graph; its forbidden-import check only
re-derives what depguard already gates, so it should always report agreement.
**Gaps and drift** is where to look for trouble — deny entries that no longer
match a package, globs that match nothing, packages that lint excludes, and
layer definitions referenced from outside the repository. Read that section as a
to-do list, not as settled architecture. Note too that each rule governs exactly
the packages its file globs match, which is not always every package its name
might suggest; the per-rule package list is the ground truth.
