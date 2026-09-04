# Authored material needs an adversary, not a proofreader

Established while building this course, 2026-09-03.

Every artifact in this workspace was written by someone who had just read the
relevant source and believed each claim at the moment of writing. An independent
verification pass — one whose only instruction was *falsify these claims against
the pinned trees* — still returned **31 findings across nine artifacts, 12 of them
factually wrong**.

The wrong ones were not sloppy. They cluster into four recognisable shapes:

1. **Citation drift within a file.** `internal/entity/agent.go:12-19` was cited as
   the definition of `entity.Agent`. That range is the role constants; the struct
   is at `:42-66`. The author had opened the right file and taken the wrong lines.
2. **A rule assumed to cover more than it does.** An exercise sent the learner to
   find the `sdk-leaf` depguard rule that constrains `internal/domain`. No such
   coverage exists — `internal/domain` appears in no depguard rule at all. The
   claim was *plausible from the architecture* and false in the config.
3. **Ordering asserted from intuition.** A trace listed cobra's
   `PersistentPreRunE` before argument-arity validation. `cobra@v1.10.2` calls
   `ValidateArgs` at `command.go:968` and only then walks the parents for
   `PersistentPreRunE` at `:985-986`. Settling this required reading a vendored
   dependency, which no author had thought to do.
4. **A happy-path behaviour stated unconditionally.** "The deferred `h.Close()`
   stops the embedded fleet-db subprocess" is true only when that command spawned
   it. `openLocalStore` tries reuse first, and the reuse path returns a handle with
   `embedded` left nil, so `Close` skips the stop entirely
   (`internal/bootstrap/openstore.go:122-124`, `:145-159`, `:64-68`).

One finding was a **safety** defect rather than a factual one: an exercise told the
learner to run `make local-mode-verify` bare, and that script defaults to port
`:8282` — a colleague's stack on this shared machine.

## Implications

- **Budget the verification pass as real work.** It found more per unit of effort
  than a second authoring pass would have. Self-review does not substitute: an
  author re-reading their own lesson re-derives the same wrong belief.
- **Falsification beats proofreading.** The instruction that worked was "find the
  branch, the early return, the config flag that makes this untrue in a common
  case", not "check this is accurate".
- **Count things with a command, never from memory.** Two findings were miscounts
  by roughly 10% that felt right.
- **The dependency is in scope.** Claims about cobra, `net/http`, or Redis
  semantics are checkable in the module cache, and library behaviour is exactly
  where confident intuition is least reliable.
- Ordering claims and unconditional behavioural claims deserve the most suspicion,
  because they are the two shapes a reader cannot spot-check while reading.

## What this rules in for the course itself

Lesson 30, *When the docs are wrong*, teaches the learner to verify a claim before
acting on it. This record is why that lesson is honest rather than smug: the same
failure mode that produced the drift in `README.md` produced twelve wrong claims in
this course, written by people actively trying to avoid it. See
[0002-docs-drift-from-code.md](0002-docs-drift-from-code.md).
