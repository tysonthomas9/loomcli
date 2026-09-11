# Native recovery comment contract

Offers, registry reads and native validators now require fleet.issue-workspace.v4 with ten fields, including comments. Each comment has exactly id, issue_id, author, body and created_at. IDs remain unchanged and unique across the workspace; issue references must exist in the complete manifest. Missing/null collections, duplicate IDs, invalid timestamps and blank fields fail validation. Body length is capped at 10000 UTF-8 bytes, not JavaScript string length. An explicit whitespace predicate matches Go, including NEL rejection and BOM preservation.

The Go bridge retains the original document bytes; the browser freezes complete prepared comment records. Old v3 payloads cannot be accepted by defaulting comments to empty. Prepared comments do not publish selected-detail state or retire confirmed-comment overlays yet. History, external views, all-view coverage and exact reset acknowledgment remain separate requirements.

The Fleet v4 producer requires independent comment provenance 1 as well as issue provenance 1 in the same PostgreSQL snapshot; old lanes fail explicitly. This is a paired strict contract cutover and must be rolled back together. No merge or deployment is included.

Validation includes 89 shared Go/TypeScript cases, affected backend/realtime/subscription race tests, strict v3 offer/registry rejection, and 327 focused frontend tests. Independent review verified whitespace, byte limit, identity, timestamp and preservation parity. The full frontend quality gate passed all 427 files and 9348 tests, and the production build passed in 2.56s; logs use `/private/tmp/sse-stack-review/recovery-comments-*`. These fixtures are not deployed paired-browser evidence.
