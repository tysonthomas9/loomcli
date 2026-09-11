# Expanded-tree and mounted-document recovery

Enabled file trees and mounted documents now participate in query recovery.
This extends reread coverage; it does not reset the SSE cursor or certify a
global filesystem/projection snapshot.

## Trees

Recovery reads the root, expanded directories and their ancestors in parent
order. The new tree is committed only after every required read succeeds and
its expansion revision is still current. Membership changes cause another
coordinator pass before acknowledgment. Pre-recovery ordinary/root/reveal
requests are fenced so late results cannot overwrite recovered data.

Fresh parent listings may prove an expanded descendant is absent or no longer
a directory; its cached subtree and expansion are then pruned. Arbitrary
endpoint errors are not interpreted as absence. Existing valid expansion and
selection survive recovery. Collapsed cached directories are discarded by the
new map and can be read when expanded again. Ordinary reveal retains its
best-effort navigation contract outside strict recovery.

## Documents

The document registry deduplicates enrollment by coordinator and full workspace,
explorer reference and path. Only mounted consumers participate; retained
closed drafts do not create reads. Strict recovery starts a fresh read and
rejects failure, cancellation or supersession, including synchronous
invalidation during commit. Ordinary refresh joins active recovery.

Dirty draft content and its base/revision are preserved. A changed server
version records an external conflict instead of overwriting the draft; edits
made during a read remain intact. Recovery rejects while a save is in flight
and requires retry, without canceling the write or acknowledging recovery.
Missing/unreadable documents fail the read; this patch does not manufacture a
deleted-document snapshot from an arbitrary 404.

## Source integrity

Directory API responses require canonical path and entry shape. Metadata
failures reject the directory listing; policy-hidden entries, symlinks and
entries confirmed concurrently vanished remain intentional omissions. File
reads require explicit content for text, flags and a current version. Empty
text is serialized explicitly. Binary and truncated responses represent
bounded previews; read-only revision previews retain their unversioned
contract. Truncated recovery certifies that preview and file version, not all
file contents. Existing UI disables editing truncated previews.

Skill-file reads require explicit content, requested ref/path, canonical body
revision and valid metadata. A conflicting ETag fails. Header-only revision
acceptance was deliberately removed: the canonical Fleet producer supplies a
body revision, matching the strict shared-contract cutover.

## Validation

- Full frontend suite: 416 files, 9,018 tests passed.
- Focused tree, document and API regressions passed; frontend typecheck, scoped
  ESLint and formatting passed.
- Full affected Go race tests passed for Fleet adapter, file service, service
  contracts and miscellaneous handlers. Final adapter rerun passed in 1.554s
  after strict revision/metadata checks; scoped Go lint reported zero issues.
- Independent reviews found no remaining blocker in these bounded contracts.

Frontend tests are deterministic. Go tests use owned filesystem/HTTP fixtures.
No paired application/browser proof or database restart is claimed here.

## Remaining architecture

A file handle is used to hash all bytes read, including beyond the preview
limit, but concurrent in-place writes are not an immutable snapshot. Directory
reads likewise lack a common source revision fence. File-browser derived
queries such as diff/index/search/history require their own coverage audit.
Committed projection runtime integration, stable replay boundary, retained
cursor-reset acknowledgment, real storage restart and paired browser proofs
remain required before claiming complete SSE recovery.
