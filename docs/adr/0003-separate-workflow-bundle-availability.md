# Separate workflow validation from bundle availability

Status: accepted

Workflow Catalog records validation and bundle availability as different
invariants. Authoring persists a pending immutable version, promotes and
verifies its digest-addressed bundle, then marks it available; only available
versions may be approved, activated, or executed. This prevents catalog state
from claiming executable content exists when promotion was interrupted or the
bundle later drifted.

Restart reconciliation may retry bounded transient promotion failures. Digest
mismatch, containment violation, or invalid staged metadata marks availability
failed, and dispatch fails closed on missing or drifted content. Existing
development versions receive no backfill because this stack is not live and
requires fresh Workflow Catalog state.
