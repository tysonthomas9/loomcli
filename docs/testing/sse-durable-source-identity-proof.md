# Durable source identity in Loom recovery

Authoritative Fleet mutation reads now require a concrete opaque `c2` cursor and an opaque `s1` source identity. Initial `0` and `$` requests remain selectors; responses, event IDs and emitted checkpoints must use `c2`. Loom validates canonical base64url envelopes with a 1024-character limit. It does not decode embedded JSON, derive positions, or order cursor contents. Numeric and `c1` resume values, incomplete pages and missing or foreign event workspaces fail closed.

The process-local source binding now also pins the durable identity returned by its first head read. Each page, subsequent head and recovery read must retain it. An identity mismatch permanently retires that captured source, even if a later response returns to the original identity. Fleet's `409 mutation_source_changed` is a typed terminal source error. The handler sends a resync without advancing its accepted cursor; this error does not mint an ordinary expiration-recovery offer.

Recovery responses require Fleet's `X-Fleet-Source-Identity` header. The short-lived registry stores the expected identity and includes it as the offer's required sixth field, `source_identity`. It verifies both identity and a concrete `c2` recovery boundary before returning native bytes. The browser requires `X-Loom-Recovery-Source` to match the offer alongside the echoed handle. Prepared native metadata retains the identity. V2's eight native document fields remain unchanged.

## Limits

Durable source identity is not authentication or repository authorization. Fleet owns binding the requested workspace and validating cursor identities against the database. A database UUID copied by a backup cannot distinguish a restore carrying the same UUID: restore/clone operations must rotate the source epoch and fence old writers according to Fleet's lifecycle contract. Normal process restarts preserve identity.

This change does not publish a native snapshot into browser caches or acknowledge a reset. Browser attempt ownership, complete query coverage and time-derived ready membership remain separate requirements. The synthetic opaque payloads in Loom fixtures test framing and identity propagation; only Fleet tests its inner cursor codec and database provenance.

## Validation

The fixture matrix includes same-backend identity changes with identical cursor strings across head/page/recovery reads, permanent retirement after A→B→A, missing source identity/header, missing or foreign event workspace, typed HTTP source changes, strict old-cursor rejection, and native byte preservation. Existing deterministic 201-event HTTP replay and source replacement proofs are migrated to the new contract.

These are package, local HTTP fixture, and frontend tests. They are not a deployed paired Loom/Fleet browser proof or hosted CI result. Final terminal validation is recorded with the PR; logs for this work use `/private/tmp/loom-source-*`.

Final local validation also includes:

- `go test -race -p 1 ./internal/backend/... ./internal/webui/subscription ./internal/webui/server/realtime -timeout 180s`: passed. This covers local HTTP fixtures and deterministic replay, with no deployed Fleet process.
- Scoped `golangci-lint run` over those Go packages: zero issues.
- `make check-frontend`: all six stages passed; 423 files and 9,221 tests passed. Coverage: 81.58% statements, 73.99% branches, 80.23% functions, 83.66% lines.

Full gate logs are `/private/tmp/sse-stack-review/durable-source-loom-{final-race,final-lint,check-frontend}.log`. A subsequent focused race run passed for recovery HTTP source-change classification and permanent retirement after a real HTTP 409 (Fleet package 1.311s; subscription package 1.368s). Independent review caught the initial generic-error mapping; the fix bounds error-body parsing to 64 KiB and retires only on the explicit source-change code.
