# File metadata and Git status recovery

The subsequent [skills recovery proof](sse-skills-recovery-proof.md) covers
the shared skills catalog and capability reads.

This milestone enrolls enabled agent Git status, mounted file capabilities, and
checkout metadata in strict query recovery. Request owners fence workspace,
agent, enabled-state and unmount changes, including ignored cancellation.
Ordinary refreshes join an active recovery request rather than displacing it.
The Git status hook retains its five-second polling fallback.

Checkout catalog responses have two useful contracts: ordinary browsing may
display explicit partial results, while strict recovery requires a complete
metadata read. A recovery-only validation hook checks completeness before
committing or acknowledging the response. Missing checkouts are valid known
states; inspection errors, partial flags and limit flags cannot acknowledge
complete metadata recovery. Checkout repair also requires a successful strict
metadata refresh before reporting success.

## Source read honesty

Checkout membership now comes from declared topology. Unsafe or uninspectable
paths produce partial/error rows instead of disappearing during enumeration.
Successful inspection of an absent path still reports a missing checkout.
Existing best-effort discovery consumers retain their filtering behavior.

Git summary reads propagate branch, status, conflict, comparison and stash
errors. One porcelain result supplies both cleanliness and changed-file state.
Detached HEAD is a successful Git state and is compared through HEAD; a missing
upstream or malformed comparison result returns an error instead of fabricated
zero counts. Valid Unicode and @ branch names remain supported. No network
fetch is introduced. Frontend cancellation fences stale results; it does not
currently terminate the backend Git subprocesses. Multiple Git commands do not
constitute an atomic repository snapshot.

## Remaining query contracts

Skills catalog/capability stores still swallow read failures and need shared
strict ownership/enrollment. File-tree recovery must reread root and expanded
directories while preserving expansion and selection, rejecting any failed
path and rechecking expanded membership. Document recovery must preserve dirty
content and draft revisions, record server conflicts, and wait for a save
before starting a fresh read or reject busy state. Existing document refresh
returns success during saves and after caught errors, so it cannot certify
recovery yet. These participants are not silently treated as covered here.

A committed source fence and retained cursor-reset acknowledgment still require
the production Fleet integration described in
[the committed runtime plan](sse-committed-runtime-next.md). Real storage restart
and paired browser proofs remain outstanding.

## Validation

- Full frontend suite: 414 files, 8,950 tests passed.
- Focused metadata/browser tests: 109 passed; Git hook/API tests: 41 passed.
- Frontend typecheck, scoped ESLint and formatting passed.
- Full race-enabled Git package tests passed (11.948s), including disposable
  real repositories with attached/detached HEAD and Unicode/@ branch names.
- Full service race tests passed (4.933s); checkout cases passed again after
  result-assembly extraction (2.589s). Scoped Go lint reported zero issues.
- Independent review caught and verified fixes for omitted unsafe checkout
  candidates and overly restrictive Git branch validation; metadata request
  ownership and partial-result semantics had no remaining scoped blocker.

Frontend proofs are deterministic. Go proofs use owned temporary filesystem
fixtures and local Git repositories. No application browser or storage-server
restart is claimed for this milestone.
