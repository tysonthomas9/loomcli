# Custom-prompt graph migration

This package preserves the 22 product executions from
`zz-custom-prompt.test.yaml`. Fifteen paths reuse the mounted Create Agent
dialog trunk. The two template-looking-prompt outcomes share a deeper reset and
creation trunk before branching into storage and argv proofs.

Every terminal leaf remains independently runnable. Shared graph fixtures only
bootstrap the two isolated workspaces; any agent, role, prompt file, or saved API
value consumed by a leaf is created on that leaf's root-to-terminal path. The
first root transition on every path invokes the shared
`reset-independent-path-agent-terminals` block. It lists tabs in both fixture
workspaces, deletes only tabs whose canonical `kind` is `agent`, and verifies
both that no agent tabs remain and that every pre-existing non-agent tab
identity is still present with the same kind. Deleting through the terminal-tabs
API also stops the owned PTY. Global teardown still uses
`clean-test-workspace.sh` to cover the last path and later runs. Keeping cleanup
inside the existing root transitions preserves the graph's reusable
descendant-prefix contract.

Original graph IDs and product semantics are retained. CUS-D5 remains the approved controlled-Codex
reference. The runtime-review paths now apply that evidence shape to CUS-D6, D8d, D10, D11, D12,
D17a, and D19: each opened agent must show a reviewer-visible controlled-backend readiness marker
and own exactly one live PTY before its scenario-specific launch contract can pass. Every inline
Claude custom-prompt path captures only the mounted form value's SHA-256 and UTF-8 byte count before
submission, then matches that safe fingerprint against the stub's measurement of its actual final
argument. Raw prompt bytes are never interpolated into the comparison mechanic, including prompts
containing JavaScript template-literal syntax. The stub reports its
supplied Claude session UUID while keeping prompt bytes out of terminal output. API and CLI
readbacks remain before the final terminal navigation so the terminal screenshot and its related
checks collapse into one review moment. CUS-D6 additionally creates one no-trailing-newline XSS
fixture, uses that file as the browser input, and compares the stored role bytes to the same file
before making either inert-DOM assertion. The negative rendering fence therefore cannot pass by
accidentally creating an empty or different prompt, although it remains an absence fence rather
than sanitizer coverage. The migrated D8d leaf retains its exact role kind, prompt, and legacy
description readback, and D12 reads the role again after the unsupported agent PATCH so an ignored
prompt field cannot be inferred merely from the already-running process. D19 likewise retains an
independent stored-role readback for `kind`, `builtin:pr-review`, and the empty inline prompt before
the final controlled-launch evidence. D17a keeps its explicit normalized-role lookup as well as the
agent and live-launch identity checks.

Deterministic AFT runs opt into a persistent Claude harness-lead stub. Its gate requires Claude's
interactive `--session-id` signature and rejects print/stream-json invocations, so background and
one-shot calls preserve their prior behavior. Both Claude stub layouts share the same lifecycle:
they become visibly ready, report only a safe authored-prompt fingerprint, byte count,
safety-block count, and supplied session UUID, remain alive while stdin is open, and exit when the
PTY closes. The harness runtime attaches its live output sink, and the controlled stub redraws its
ready frame on `SIGWINCH` after terminal attachment, so fast startup is deterministic without
replaying pre-attachment output or adding a stub-only readiness sleep. Focused tests
distinguish expected, different, and missing prompt inputs without
echoing any prompt. The deterministic server pins the controlled runtime and the exact
`STUB_CLAUDE_LEAD` test control is allowlisted through the child environment filter; other
`STUB_CLAUDE_*` values remain filtered. This is deterministic process and orchestration evidence,
not proof of Claude model behavior.

Low-risk repeated final checks in CUS-D2, D3, D8a-c, D15, and D16 are also expressed through
suite-local terminal blocks containing only waits and expectations. Their negative semantics are
unchanged, while the storyboard presents one outcome card with expandable underlying checks.
