# PR Review agents graph migration

This package preserves all eight `zz-pr-review-agents.test.yaml` outcomes and their exact
test names/intents. The agent-list and creation-dialog prefixes are reusable graph trunks.
Scenarios that formerly relied on the first linear test's durable `prr-*` agent now replay
an explicit primary-agent prerequisite before testing terminal launch, role reuse, or role
conflict. This removes execution-order coupling.

Only replayed prerequisite primary-agent names append `${AFT_CASE_ID}` so independently
executed dependent leaves do not collide while graph-scoped workspace fixtures remain active;
the original creation outcome and second-agent identity remain unchanged. Original scenario
mechanics stay ordered after their prerequisite. Added observable proofs cover the blank
browser, shared agent-list/dialog states, and terminal states whose legacy last mechanic was
a click, API call, or runner-side readback. The prompt-list failure remains a direct leaf so
its route-aware readiness intents stay byte-for-byte equivalent; its abort route is aggregated
before that path's first browser action.

Setup and teardown use the shared isolated-workspace cleanup helper, which enumerates terminal
tabs and agents before deleting the workspace. That explicitly reclaims case-isolated
prerequisite identities instead of relying on the old fixed four-name cleanup list.
