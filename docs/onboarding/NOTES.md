# Teaching Notes

Working notes on how this learner wants to be taught, and how this workspace is built.

## Learner profile (stated 2026-09-03)

- Competent Go engineer. Do not teach the language.
- Already understands event sourcing, Redis, and AI-agent orchestration generically.
  Teach this system's specifics only.
- NOT assumed fluent in modern React/TypeScript. Frontend lessons must show the
  pattern explicitly rather than lean on idiom.
- Track: full-stack, backend-first. Must be able to ship a vertical slice by week 6.
- Explicitly asked for: repo architecture, implementation patterns, how to navigate
  the repo, and how to start working on new tasks. Navigation is a first-class goal,
  not a side effect.

## Workspace conventions

- Lives at `docs/onboarding/` inside the loomcli repo, on branch `docs/onboarding`,
  in worktree `.worktrees/onboarding/loomcli`. It is checked in so a real new hire
  gets the course with a clone.
- Research is pinned to two read-only checkouts so lessons teach shipped state:
  - loomcli: this worktree (branched from `v5`)
  - fleet-db: `.worktrees/onboarding/fleet-db`, detached at `origin/main`
  Re-pin these when the course is refreshed, and note the commit in the lesson footer.
- Every lesson links `assets/course.css`. No lesson inlines styling that a second
  lesson would duplicate — it becomes a component in `assets/` instead.
- Citations are `path:line` relative to a repo root, and every one must have been
  opened and verified. A lesson that cannot cite does not make the claim.

## Teaching decisions

- **Vocabulary before architecture.** This repo reuses ordinary words as domain terms.
  `docs/loom-glossary.md` and `CONTEXT.md` already exist and are good — the course
  builds on them rather than replacing them, and the workspace GLOSSARY is the
  learner's own compressed version.
- **One spine.** A single end-to-end trace runs through the whole course and is
  revisited at increasing depth each week. Newcomers bootstrap from one real trace
  far better than from ten abstractions.
- **Ordered by dependency, not by repo.** Do not teach all of loomcli then all of
  fleet-db; interleave so prerequisites are always already taught.
- **Every week ends in a doing, not a knowing.**
- Exercises must respect shared machine state: never touch a compose project or
  agent-browser session you did not create; always pick an unclaimed port block.
  This is a real hazard on this team's machines, so it is taught in week 1, not
  discovered later.

## How the course was built, and how to extend it

The pipeline that produced these lessons, in case you add to them:

1. **Research.** Fourteen subsystem briefs, one per domain, written from the pinned
   trees into a scratch directory. They are research notes, not sources of truth —
   measured against the code afterwards, they run about 95% accurate.
2. **Author.** One lesson per agent, each given its brief, the house style, and the
   instruction to treat every brief line as a hypothesis to re-check.
3. **Verify adversarially.** A second, independent pass per artifact whose only job
   is to falsify claims against the pinned trees. This is not optional theatre.
4. **Fix, then re-check.** A third agent re-confirms each finding against source
   before editing, and may reject one it cannot reproduce.

**What that cost, and what it caught.** Across four rounds, verification raised **349
findings** on 35 artifacts. 348 were confirmed and applied; exactly one was rejected as
stale. By severity: 98 wrong, 154 imprecise, 58 uncited, 7 unsafe. Every artifact was
checked, including the ones written slowly and by hand — those were not cleaner.

The single most useful number: citation density went from a low of **1** citation in the
worst lesson to a floor of **16**, median 39. Most of that came from asking specifically
for *uncited* claims, which the first two rounds did not do. If you run this again, ask
for uncited from the start — it is a different question from "is this citation correct",
and it finds a different class of defect.

The three defect shapes that recurred, in order of frequency:
- **Unconditional statements** — "only", "always", "exactly one", "nothing else" — that
  hold on the happy path, or for non-test code, or for one of several services.
- **Citations that drift inside the right file**, landing on a doc comment or a struct
  field rather than the code being described.
- **Counts recomputed from memory**, wrong by about 10% and confidently stated.

**The lesson for whoever maintains this:** a citation nobody re-opened is a defect
waiting to be found by a new hire, which is the worst possible reviewer to find it.
Budget for the verification pass — it cost more than the writing, and was worth it.

**The safety rule earned its place.** Seven findings were exercises that would have
mutated shared machine state, and they were written by people who had read the safety
rule and were trying to follow it. The three hazards that recur, worth knowing before
you write an exercise for this codebase:

- `go test ./internal/cli` kills every tmux session on the host named `loom-test-*` or
  `loom-e2e-test-*`, through that package's `TestMain` — and `-run` does not stop it,
  because `TestMain` runs in full whatever the filter selects. Sub-packages such as
  `internal/cli/data` are separate packages and are fine. Taught in Lesson 26.
- `make hooks` installs a pre-push hook into the parent clone's hooks directory, which
  every sibling worktree shares. Taught in Lessons 04 and 31.
- `go build -o ./loom` writes into a checkout, and the output is gitignored — so the
  working tree still looks clean afterwards and the next person never sees it.

All three look read-only and are not. That is the pattern: on this machine the dangerous
commands are the ones that read as harmless.

## Structural decisions

- **Numbers are file identity, weeks are presentation.** A lesson's number never
  changes once written — other lessons link to it. Which week it belongs to is set
  by the kicker and by `index.html`, and can be rebalanced freely.
- **The nav bar is sequence, the body is prerequisites.** Lesson prev/next always
  runs 01 → 31 in reading order. Conceptual prerequisites are linked inline where
  they are needed, not in the nav.
- **Six weeks, 31 lessons:** orientation (1–5), the spine (6–7), the runtime (8–11),
  the web layer and what proves it (12–17), fleet-db (18–24), across both repos and
  shipping (25–31).

## Open threads

- Decide whether the course should also cover on-call / production incident response.
  Lesson 27 (seeing what happened) is the closest thing and may be enough.
- Confirm whether the learner wants the `loom-pr-test` runtime-testing skill taught
  as a lesson or left as a reference. Lesson 16 currently treats it as reference.
- The course is pinned to loomcli `ce4df56fb` and fleet-db `000793b`. When re-pinning,
  re-run the verification pass rather than assuming citations survived — the drift
  lesson (30) exists because these repos move faster than their docs.
