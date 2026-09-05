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

Each scenario declaration lives immediately above its executable test, which
calls `Covers(t)` before exercising the owning seam. A scenario keeps a readable
name and behavior, but its coverage metadata references only canonical IDs.
The single typed 1-95 catalog in `registry/catalog_v2.go` owns behavior wording,
owner, seam, required execution coordinates, and the strict-cutover decisions.

Passing runs generate `skills-edge-evidence/v2` JSON shards. Each evidence row
contains only its canonical ID, Go package, top-level test, and the backend or
provider actually used by that process. Ordinary package tests log the same
ID-only marker; `skills-edge-coverage extract` promotes it only after the exact
package-and-test pair passes under `go test -json`.

A repository shard may be structurally valid while still partial. The optional
`edge_readiness` compatibility input generates real Loom and Fleet shards and
runs `skills-edge-coverage readiness`. That deliberately fails until every
explicit catalog coordinate is proved. It merges repeated IDs across shards,
treats unspecified required dimensions as irrelevant, and adds only catalog
N/A decisions 72-77; there
is no unresolved disposition that can produce a release-ready result.

## Generated workspace-file contract

Fleet owns `contracts/workspace-file-v1.json`, the sole authored source for
shared limits and revision encoding. Loom consumes
`internal/domain/workspace_file_contract_gen.go`; it does not maintain a
handwritten copy of the algorithm. `TestGeneratedWorkspaceFileContractOwnsLimitsAndRevisionEncoding`
pins literal limits and identities while the paired compatibility workflow
regenerates from the exact Fleet SHA and rejects drift in either repository.

The two Git repositories cannot be committed or pushed atomically. Review and
release must therefore use the exact paired SHAs checked by that workflow; no
compatibility shim is used when the artifacts disagree.

Pull requests run Redis/MinIO and PostgreSQL/MinIO. Releases additionally use
the existing GCS test bucket through Fleet's production S3-compatible XML
configuration. Each GCS run receives a unique prefix, records every returned
object version/generation, and deletes only those recorded run-owned objects.
