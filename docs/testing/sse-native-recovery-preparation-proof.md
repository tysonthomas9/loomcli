# Native issue recovery preparation and HTTP read

This slice provides a browser API for retrieving and preparing the certified issue manifest through a recovery handle. It does not publish the result to a UI cache or authorize a checkpoint change.

## Native preparation

`api/common/issueRecovery.ts` accepts the original JSON document, validated offer and echoed handle. It checks the fixed manifest/workspace/cursor, complete nonnull collections and total, native issue fields, repository alias agreement, unique issue identities, complete derived-record equality and blocker semantics. Parent-blocked sentinels remain distinct from direct blockers. Status/type values stay native strings; the existing UI compatibility converters are not used.

Preparation retains the original document, a copied immutable offer, all native records and explicit `issues`, `ready`, `blocked`, `deferred` coverage. The returned tree is deeply frozen. Coverage identifies producer collections, not all mounted browser views; no graph edges, detail collections or acknowledgment are fabricated.

Both Go and browser validators reject duplicate JSON members (including escaped-key duplicates), unpaired Unicode surrogates, malformed documents, excessive nesting and oversized input. Valid Unicode pairs and literal backslash sequences remain valid. The browser additionally rejects numeric tokens that would change decimal value when converted to JavaScript numbers, unsafe integers and nonfinite/underflow values. Go can retain arbitrary unknown numeric values as raw JSON; browser preparation deliberately fails unsupported numeric representations rather than rounding them. The original document remains the authoritative textual representation.

The limits are 16 MiB UTF-8 and nesting depth 512. These bound accepted input and traversal, not peak heap allocation or SQL work. A string-only preparation API cannot detect bytes already replaced by a lossy decoder; the HTTP reader therefore validates UTF-8 before preparation.

## HTTP ownership

`api/common/readIssueRecovery.ts` uses a bodyless authenticated POST to `/api/workspaces/{ws}/events/recovery/issues`, the dedicated handle header, disabled caching and redirect rejection. It requires HTTP 200, JSON content type and the exact echoed handle before reading the document. Streaming byte accounting enforces the size limit. Fatal UTF-8 decoding preserves a BOM so strict JSON parsing can reject it rather than silently stripping it.

Caller cancellation, the 15-second request deadline and offer expiry invalidate the operation. Late successful fetch/body results cannot return prepared data. Cancellation cleanup does not wait indefinitely for an uncooperative stream. Errors do not invoke ordinary queries, retry with another source, publish partial data or change an SSE cursor.

The caller must still own the current browser connection, committed scope and recovery attempt. Reading through a valid handle does not establish that the caller's current UI still owns that handle, nor does it prove durable Fleet incarnation across requests.

## Shared evidence

`internal/backend/fleet/testdata/issue_recovery_corpus.json` is consumed directly by both the Go client and browser tests. Its 32 common cases cover native metadata/extensions, nonnull collections, aliases, workspace/derived-record consistency, sentinels, invalid cursors, duplicate keys, Unicode and calendar dates. These are native wire conformance fixtures, not recorded production storage output. Browser-only numeric limits have separate tests.

Before the Go guard, the shared duplicate-key cases reproduced acceptance of ambiguous manifests. The new guard rejects them before ordinary JSON decoding can discard duplicates or replace lone surrogates. The independent Unicode review identified that Go and JavaScript otherwise interpret those strings differently.

Validation completed: 38 focused preparation tests, 32 shared corpus cases, 21 HTTP reader tests, and the full frontend suite (9,154 tests in 420 files). Full Fleet backend race tests passed in 1.661 seconds; Go build, vet and scoped lint passed. Independent preparation, Unicode and HTTP reviews completed; their findings were fixed. Frontend TypeScript, architecture checks and production build passed; frontend lint had no errors and 26 existing unrelated warnings. Existing bundle-size warnings remain. This evidence uses package and simulated HTTP streams; no paired Fleet service/browser recovery is claimed.

## Remaining acceptance work

The API is not invoked by EventProvider yet. The [browser coverage contract](../design/sse-browser-recovery-coverage.md) still requires attempt ownership, retry suspension, generation-controlled publication across all writers, graph/detail/filtered-cache coverage, dormant-state invalidation, durable source incarnation and acknowledgment/resume proof. Ordinary query refresh completion remains insufficient.
