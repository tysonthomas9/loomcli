# Supervisor-disabled migration proof

`supervisor-disabled-matrix.yaml` is the executable contract for proving Loom's
retained behavior without a workspace daemon or auto `agentdef` control plane.
Run the proof entrypoint from the repository root:

```sh
make test-supervisor-disabled
```

The checked-in Phase 4 Execution row is **green**. Running
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

Each row fixes `LOOM_LOCAL_MODE_PLANE=ts` and `LOOM_TASK_READY_EVENTS=1` and
must retain all Phase 1 assertions: no auto agent definitions, daemon process,
or daemon sockets; public-API creation of plan/task AgentServices and bindings;
deterministic planner/coder completion; and planner/coder transcript plus coder
diff evidence. The coordinates make the proof's depth, realness, provisioning,
polarity, and target explicit.

The setup and verifier own one fresh checkout-scoped Compose lane. Verification
covers task freshness, public-API plan/task agent creation, planner design and
review, coder completion, sessions, transcripts, diff evidence, and the
negative zero-agentdef/zero-daemon-process/zero-daemon-socket assertions. A
green row counts only when the runner executes that verifier successfully; the
manifest label alone is not evidence. This Execution row does not authorize
supervisor deletion: Phase 6 still requires the complete Agents/Interaction
parity matrix with no skipped or disabled rows.
