# Skills E2E

This directory contains scenario-driven executable specifications backed by a
real Loom/Fleet test environment. Scenarios invoke the public `loom` CLI; they
do not import product internals or inspect Redis/PostgreSQL as user-journey
evidence.

Scenario code keeps every public operation visible. The harness stages fixtures,
runs processes, decodes JSON, and compares results, but it does not combine
multiple Loom commands into one invented action. A scenario therefore reads in
the same order as the user journey:

```go
loom.SkillImport(source)
selected := loom.SkillShow("example")
materialized := loom.SkillMaterialize()
materialized.RequireExactTree(source, "example")
```

The suite currently covers the exact update round trip plus five independent
behaviors: stable identical reimport, content-only update, stale-file pruning,
whole-skill deletion, and agreement between public list and show results.

The surrounding compatibility workflow provisions the real persistence and
object-store processes. It then supplies `SKILLS_E2E_LOOM_BIN` and runs:

```sh
go test -tags=e2e -count=1 -v ./test/skills-e2e
```

## Fixture conventions

Fixtures are readable source recipes. The harness stages them as real Skill
trees before import:

- ordinary files are copied byte-for-byte;
- `*.executable` loses that suffix and is staged with mode `0755`;
- `*.empty` loses that suffix and is staged as a zero-byte file;
- `*.hex` loses that suffix and is decoded from hexadecimal bytes.

This keeps executable, empty, and binary expectations visible in review while
the actual product receives the intended paths, modes, and bytes.

`expected.json` is an independently reviewed literal oracle for the public
Skill representation and opaque tree revision. Exact materialized bytes and
modes are compared with the staged updated fixture.

Covered scenario metadata is authored once in `registry/scenarios.go`. Each
top-level E2E test calls its typed scenario's `Covers(t)` method before invoking
public Loom commands. The fast registry test validates IDs, ownership, matrices,
and edge-case uniqueness without starting services.

CI generates `e2e-coverage-<storage>.yaml` from those Go declarations and
uploads it as evidence. The YAML is not checked in and is never edited by hand.
