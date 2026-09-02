# Lead-agent graph migration

This package preserves the 23 executions from `zz-lead-agent.test.yaml` while
making their prerequisites explicit. Eleven leaves reuse the primary Lead
creation trunk, two continue through its live-session trunk, and three reuse a
secondary Lead trunk. Assignment and epic branches replay their own state on
the selected root-to-leaf path.

The graph-scoped fixture only bootstraps the isolated workspace and contrast
worker. Fixed Lead identities are reset before their creation transitions, and
every saved file or API value is produced before a consuming leaf. Setup and
teardown use `clean-test-workspace.sh` so terminal tabs are closed before agent
or workspace deletion.

Original test names, test intents, routes, and ordered mechanics are retained;
state contracts add visible branch and terminal proofs for human review.
