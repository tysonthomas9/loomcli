# Lead-agent graph migration

This package preserves the 23 executions from `zz-lead-agent.test.yaml` while
making their prerequisites explicit. Eleven leaves reuse the primary Lead
creation trunk, four continue through its connected controlled-Codex trunk, and
three reuse a secondary Lead trunk. LED-D8 takes the creation trunk directly but
reuses the same connected-runtime and launch-attribution checks. Case 16 has its
own connected disposable-Lead trunk, then exposes the orphaned PTY state before
explicitly reclaiming it. Assignment and epic branches replay their own state
on the selected root-to-leaf path.

The graph-scoped fixture only bootstraps the isolated workspace and contrast
worker. Fixed Lead identities are reset before their creation transitions, and
every saved file or API value is produced before a consuming leaf. Setup and
teardown use `clean-test-workspace.sh` so terminal tabs are closed before agent
or workspace deletion.

Original routes and ordered product mechanics are retained. Runtime test names
and intents are phrased as reviewer-facing outcomes, and state contracts add
visible branch and terminal proofs for human review.
Connected-runtime checks prove deterministic Codex bootstrap, one live
agent-bound tab, and exact launch attribution. They do not claim provider model
behavior. The migrated Claude side of case 14 remains launch-spec-only by
design; the prior controlled Codex terminal is the visible runtime evidence.
