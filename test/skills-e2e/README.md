# Skills E2E

This directory contains scenario-driven executable specifications backed by a
real Loom/Fleet test environment. Scenarios invoke the public `loom` CLI; they
do not import product internals or inspect Redis/PostgreSQL as user-journey
evidence.

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

`edge-cases.yaml` maps stable review IDs to behavioral tests. It is a coverage
registry, not an executable scenario language.
