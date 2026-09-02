# Review actions graph migration

This package preserves both `review-actions.test.yaml` outcomes while replacing duplicated
fixture creation, review transition, queue navigation, and row-opening mechanics with one
shared trunk. Each replay receives the neutral case-isolated title
`review-action-${RUN_ID}-${AFT_CASE_ID}` and a shared saved variable name; the approval and
request-changes assertions remain distinct leaves with their original test names/intents.

Added state proofs are the initial blank-browser assertion and a mounted review-workspace
check after the shared trunk. Original branch mechanics remain ordered. Because each path
replays the trunk with its own case ID, neither outcome consumes another leaf's issue.
