# Supervisor-disabled migration proof

`supervisor-disabled-matrix.yaml` is the executable contract for proving Loom's
retained behavior without a workspace daemon or auto `agentdef` control plane.
Run the proof entrypoint from the repository root:

```sh
make test-supervisor-disabled
```

The checked-in Phase 6 Execution row is **green**. Running
`make test-supervisor-disabled` provisions its isolated Compose project and
must satisfy every declared positive and negative assertion; schema-only
validation remains available for ordinary development checks and does not
provision Compose:

```sh
go run ./scripts/supervisordisabled --validate
```

## Matrix contract

Every row supplies explicit argv-based setup, verification, and teardown steps
with timeouts. Shell command strings are not accepted. The runner validates the
entire manifest before executing any green row, runs teardown after failures or
timeouts, and kills timed-out process groups so child processes cannot outlive
the proof.

Each row exercises the single `loom serve` runtime with task-ready eventing as
an unconditional platform default. It retains all Phase 1 assertions: no
auto legacy agent definitions, daemon process, or daemon sockets; public-API
creation of plan/task Agent identities and bindings;
deterministic planner/coder completion; and planner/coder transcript plus coder
diff evidence. A second exact-test step executes
`phase6-parity-matrix.yaml`; its runner rejects missing, renamed, extra, or
disabled rows before running the Phase 6 tests. Those rows prove stale timing
and launch heartbeats, explicit task-type arbitration, preflight and bounded
retry/recovery/concurrency/epic policy, periodic desired-state reconciliation,
canonical `agentdef` mutation routing, and physical supervisor retirement.
The coordinates make the proof's depth, realness, provisioning, polarity, and
target explicit.

The setup and verifier own one fresh checkout-scoped Compose lane. Verification
covers task freshness, public-API plan/task agent creation, planner design and
review, coder completion, sessions, transcripts, diff evidence, and the
negative zero-agentdef/zero-daemon-process/zero-daemon-socket assertions. A
green row counts only when the runner executes that verifier successfully; the
manifest label alone is not evidence. Phase 6 expands this contract to the
complete Agents/Interaction parity matrix with no skipped or disabled rows.
