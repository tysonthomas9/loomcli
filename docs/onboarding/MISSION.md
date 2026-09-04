# Mission: Loom (loomcli + fleet-db)

## Why

I am a new engineer on the Loom team. Loom is an AI-agent orchestration platform:
it runs many coding agents in parallel across git worktrees, each claiming work from
a shared issue tracker (fleet-db) and integrating back through a structured
push/pull/sync workflow. I need to go from zero to shipping real, reviewed changes
in both repos — not to admire the architecture from a distance.

The bottleneck is not language skill. It is that this codebase is ~500k lines across
two repos, reuses ordinary English words (`loom`, `fleet`, `skill`, `sandbox`, `trust`,
`local`, `real`, `live`) as precise domain terms, and has more design docs than any
one person can read. I need a path through it that is ordered by dependency, grounded
in real files, and ends every step with something I can run.

## Success looks like

- I can trace one request end to end — CLI flag to Go handler to fleet-db event to
  Redis projection to SSE frame to React render — naming the file at each hop.
- I can use the team's vocabulary precisely, including the overloaded words, and I
  notice when someone else is using one of them loosely.
- I can find the code for any given CLI command, HTTP route, or UI screen in under
  two minutes without asking anyone.
- I can stand up a local-mode stack safely alongside my teammates' stacks and prove
  a change works against a real backend, not a mock.
- I can add a new CLI command, a new fleet-db endpoint, and a new UI screen, each
  following the conventions the repo already has, each with the tests the repo expects.
- I can take a task from `loom data ready`, work it in a worktree, pass `make gate`,
  and land it — including the mandatory "Landing the Plane" sequence.
- I know which parts of this system are a real boundary and which only sound like one
  (trust levels, `read_only`, sandbox modes) — so I do not build on a guarantee that
  is not there.

## Constraints

- Six weeks, part-time alongside real work. Lessons must be 15-40 minutes and
  self-contained; I will not get a clear afternoon.
- Backend-first. Go is my home; I am competent there and do not need it taught.
- I already understand event sourcing, Redis, and AI-agent orchestration as general
  concepts. Teach me THIS system's version, not the textbook.
- I am not fluent in modern React/TypeScript. Frontend lessons must show the pattern,
  not assume the idiom.
- Every exercise must run against the real repos on my machine. Shared machine state
  (podman stacks, ports, worktrees) is a hazard — exercises must respect it.

## Out of scope

- Teaching Go, Redis, or event sourcing as concepts.
- Deep React ecosystem theory — I need this app's patterns, enough to ship a screen.
- Cloud/GCP operations beyond understanding how fleet-db is deployed.
- Becoming an expert in every one of the 46 loomcli packages. Depth where it is
  load-bearing, a map everywhere else.
