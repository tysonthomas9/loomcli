# Supervisor-disabled migration proof

`supervisor-disabled-matrix.yaml` is the executable contract for proving Loom's
retained behavior without a workspace daemon or auto `agentdef` control plane.
Run the proof entrypoint from the repository root:

```sh
make test-supervisor-disabled
```

The checked-in Phase 1 execution row is deliberately **RED**. The target
reports its owner and blocker, does not run setup or verification, and exits
nonzero. A declared-red row can never count as proof. Schema-only validation is
available for ordinary development checks and does not provision Compose:

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

The current setup commands describe the intended fresh Compose lane. Its
existing `local-mode-verify` command covers task freshness, planner design and
review, coder completion, sessions, transcripts, and diff evidence, but it does
not yet cover the negative daemon assertions or prompt-agent seeding. Keep the
row red until one deterministic verification path proves every listed
assertion, then remove its blocker and change `state` to `green` in the same
change.
