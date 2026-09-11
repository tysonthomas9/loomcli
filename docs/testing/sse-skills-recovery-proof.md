# Skills catalog recovery

Skills catalog and capability hooks now enroll through workspace-qualified
invalidated-query keys. Duplicate consumers share a query; disabled and
actions-only consumers do not initiate reads. Skill, role and pack invalidation
and reconnect epochs refresh the registered queries.

The shared store uses a scoped request owner for each workspace catalog and
capability read. Strict recovery starts a fresh read and rejects failure,
cancellation and supersession. Ordinary loads join recovery. Invalidation
cancels stale reads before marking the catalog idle. Directory loaders wait
for the actual pending catalog and require successful loaded state; one
canceled directory waiter does not cancel another consumer's shared read.

The request owner moved from hooks/common to utils so stores can use it without
inverting the frontend dependency graph. Existing consumers import the same
implementation directly.

## Source integrity

The frontend forwards signals and validates catalog/capability response shape.
Loom's Fleet adapter requires an explicit skills array and valid workspace/ref
identity. The catalog handler rejects nil or invalid entries before projecting
groups. Canonical empty arrays remain valid. Capability responses describe
authorization; middleware rejects failed role resolution.

The paired FleetDB change makes Redis ListSkills fail on indexed missing,
wrong-type, malformed or mismatched records, instead of silently omitting them.
Reads do not repair the index. The index/hash reads remain non-atomic, so a
concurrent deletion can cause a visible failure requiring retry. Records absent
from the index itself cannot be detected by this change.

## Validation and limits

The full frontend suite passed: 415 files, 8,983 tests. Focused store, hook and
API tests cover shared requests, ignored abort, invalidation, workspace ABA,
empty catalogs and malformed payloads. Typecheck, scoped ESLint and formatting
passed. Independent review found no remaining blocker in this bounded hook,
store, API and source-read change.

This does not certify complete workspace recovery: expanded file trees and
dirty documents remain unenrolled. Committed-feed integration, retained cursor
reset, actual storage-server restart and paired browser recovery proofs remain
required. No transport or projection receipt guarantee is implied by these
query tests.

Affected Loom Fleet-adapter and skills-handler packages passed their full race
tests (1.460s and 1.321s); scoped Go lint passed. The paired Fleet storage race
suite passed (32.937s), alongside miniredis faults, an actual disposable
redis-server read-integrity proof and all 32 harness evaluations. The Redis
proof disables persistence and does not establish restart durability.
