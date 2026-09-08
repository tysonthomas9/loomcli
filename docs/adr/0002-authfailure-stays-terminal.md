# AuthFailure stays terminal

`agentpolicy.decideHarness` continues to return `StopFatal` for `wrapper.ErrAuth`, with no
bounded-retry rung. The reasoning, recorded so the next occurrence does not have to
re-derive it:

1. **There is no evidence for the alternative.** PUPPET-425 was filed at 10 fatal stops in
   three days across five agents; the measured rate on 2026-09-08 is **one** stop in the full
   retained daemon-log window and AuthFailure in 2 of 24 daily event files — a ~13x drop with
   no fix applied, and two of that ticket's three symptom clusters were reassigned as not-auth
   (PUPPET-433 output timeout, PUPPET-431 wall detector). Changing a terminal disposition
   against a signal that thin is tuning against noise.
2. **Bounded retry is the more expensive failure mode.** A retry against a genuinely expired
   login is not a cheap HTTP re-attempt: it is a full agent respawn — worktree prep, claim,
   prompt, harness boot — and a claim taken then abandoned at the auth gate churns the task
   (the completion release forces `status=open`). One extra cycle per agent per stop buys
   nothing when the condition cannot self-heal without a human, and it *delays* the operator
   signal that is the actual fix.
3. **The current failure mode is bounded and now visible.** A false terminal stop parks one
   agent until `loom agentdef start <agent>`. It does not cascade, does not consume a task
   budget (`QuarantineEligible` excludes auth), and — with PUPPET-573 — leaves a record that
   says which rule fired, on what text, and what the screen looked like. "Costly but
   diagnosable in one occurrence" is a better place to stand than "cheap to retry and still
   unexplained".
4. **Pre-declared revisit trigger.** Reopen this decision when **any** of these holds:
   - **≥3 `AuthFailure` stops in a rolling 7 days with `source=harness_marker` and
     `screen.composer_witnessed=true`** — the readiness-gate path with a composer visibly on
     screen, i.e. what a healthy-but-unrecognised claude looks like. Two precedents already
     exist: claude 2.1.251's placeholder composer, and the TS composer regex rejecting it.
   - **≥1 `AuthFailure` with `screen.dialog_witnessed=true`** — the verdict was about a
     folder-trust or bypass-acceptance dialog, not a login. One is enough: it is a
     misclassification on its face.
   - **≥1 `AuthFailure` with `source=residual_pattern rule=residual.auth` whose `Match` is
     drawn from ordinary task output** rather than an API error — the over-broad-regex path.
   - **≥3 `AuthFailure` stops in a rolling 7 days with `source=harness_marker` and
     `screen.scanned=false`** — we are stopping agents fatally without ever seeing a screen,
     which means the instrumentation is not reaching the path and the decision is resting on
     nothing.

   The remedy differs by trigger and is deliberately not "soften `ErrAuth`":
   - composer / dialog triggers → a bounded rung for the *uncorroborated* verdict only,
     introduced as its own class so `QuarantineEligible`, the backoff bucket and the operator
     message can be reasoned about separately — never by widening `ErrAuth` itself;
   - `residual.auth` trigger → narrow that pattern (anchor it to API-error context), not the
     disposition;
   - `scanned=false` trigger → fix the plumbing before re-arguing the policy.

The identifiers those triggers name are a stable contract, not descriptions: `source` is an
`agenterr.EvidenceSource` (`internal/agenterr/evidence.go`), `rule=residual.auth` is an id on
the `residualPatterns` table (`internal/agenterr/classify.go`), and the `screen.*` fields are
`agenterr.ScreenEvidence`. Renaming any of them silently breaks the trigger it belongs to.
`TestClassifyEvidenceOverBroadResidualAuth` in `internal/agenterr/classify_test.go` is the
executable form of the third trigger.

## Considered Options

- **Bounded retry now (1–2 counted retries, then `StopFatal`)** — rejected: it would be
  chosen on one observed event, it re-runs a full agent cycle against a condition that cannot
  self-heal, and it postpones the human signal that is the only real fix. Kept as the named
  remedy behind the triggers above.
- **Classify an uncorroborated auth verdict as `Transient` in `agenterr`** — rejected for
  now: it moves a *policy* question into the *classifier*, and on the day a login really does
  expire it would burn the restart budget and surface as a generic block instead of
  "renew the harness login". Revisit only with the trigger data in hand.
- **A single `banner_witnessed` boolean as the discriminator** — rejected on inspection of
  `harness-wrapper@v0.7.7`: every producer of `ReasonAuthRequired` is already gated on
  `authRequired(screen)` (`ready.go:82`, `ready.go:107`, `conversation.go:615`,
  `conversation.go:966`), so a mirrored banner regex is `true` by construction on every
  conversation-path AuthFailure. It is a precondition of the verdict, not a discriminator of
  it. Replaced by the composer / dialog / matched-line record.
- **Fix the ambiguity upstream first (a producer detail on `ReasonAuthRequired`)** — the
  right long-term shape and filed as a follow-up, but rejected as a dependency:
  `harness-wrapper` reaches this fleet only by tag, so the change would not be observable
  here until a bump lands, and loom can capture the decisive screen evidence today with no
  wrapper change.
- **Do nothing (accept current behaviour, record no evidence)** — rejected: it is the state
  that made PUPPET-425 unanswerable. Accepting the behaviour is defensible *only* alongside
  the instrumentation that makes the next single occurrence sufficient to revisit it.
