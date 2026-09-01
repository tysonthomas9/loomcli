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

The suite covers lifecycle round trips, concurrent and ambiguous publication,
delayed visibility, corrupt-download rejection, and the production GCS XML
path. Fleet's companion real-backend tests own publication atomicity,
projection recovery, and provider grant enforcement.

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

Each typed scenario declaration lives immediately above its executable test,
which calls the scenario's `Covers(t)` method before exercising the owning
seam. `Covers(t)` derives the test name, validates the metadata, and records the
scenario only after the complete test passes. Deterministic path and revision
cases use the public Loom domain seam; lifecycle cases continue through the
public CLI and real services.

The actual run generates a `skills-edge-coverage/v1` Loom report containing
only the canonical case IDs proved by that execution, with the exact Loom
revision, owning test, seam, backends, and providers. CI uploads
`e2e-coverage-<backend>-<storage>.yaml` as evidence. YAML is generated output;
it is not checked in or edited by hand.

A Loom report may be structurally valid while still partial. Release readiness
is a separate paired check over the generated Loom and Fleet reports plus the
six typed strict-cutover exclusions for cases 72-77. That check rejects every
missing, duplicate, out-of-range, or otherwise excluded applicable case; there
is no unresolved disposition that can produce a release-ready result.

Pull requests run Redis/MinIO and PostgreSQL/MinIO. Releases additionally use
the existing GCS test bucket through Fleet's production S3-compatible XML
configuration. Each GCS run receives a unique prefix, records every returned
object version/generation, and deletes only those recorded run-owned objects.
