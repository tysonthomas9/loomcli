# fleet-db capability preflight

The daemon checks, once at startup, that the fleet-db it is pointed at actually
serves the routes this loom build calls. The failure it exists to prevent is a
daemon that prints a healthy banner and then fails every spawn against a route
the server does not have — in one incident, 774 identical failed round trips.

## What it checks

`internal/fleetdbcap` declares the route families this build needs, each at one
of two levels:

- **Required** — the daemon cannot do its job without it (issues, agents,
  agent-leases, skills). Absence refuses the boot.
- **Degrades** — a named, bounded loss of function
  (`skill-materialization-leases`). Absence is printed on the banner and the
  daemon starts.

The manifest is one file on purpose: the boot check and the runtime degradation
paths read the same judgement, so they cannot disagree about whether a missing
route is fatal.

The check reads `GET /api/v1/capabilities`, which fleet-db computes by probing
its own live mux. The comparison is a **subset test**: a fleet-db newer than
loom, advertising capabilities loom has never heard of, is compatible.
`api_version` is printed for humans and never compared — the capability set is
authoritative.

## Outcomes

| outcome | trigger | behaviour |
|---|---|---|
| `compatible` | every Required capability present | normal start, nothing printed |
| `incompatible` | ≥1 Required capability absent | named error on stderr, exit 1, **no banner** |
| `degraded` | only Degrades capabilities absent | banner gains a `Degraded:` block, daemon starts |
| `unreachable` | dial failure, timeout, 5xx, or an unparseable 200 | warning on the banner, daemon starts |
| `unverified` | the capability endpoint itself 404s (fleet-db predating it) | mode-dependent, see below |

`unreachable` deliberately does not block boot: the store already retries, and
turning a transient blip into a manual restart trades one outage class for
another. It is distinguishable from `incompatible` both in the log line
(`reason=unreachable`) and by exit code — the daemon simply keeps running.

The probe is bounded at five seconds, so a hung fleet-db cannot wedge startup;
the timeout is reported as `unreachable`.

An unparseable 200 is `unreachable`, never `compatible` and never "everything
missing". A body that failed to decode is not an answer.

## `LOOM_FLEETDB_PREFLIGHT`

Governs the `unverified` case only. Everything else is mode-independent.

- `warn` (**default**) — log "fleet-db predates capability reporting;
  compatibility unverified" and continue. This is the default for one release
  so that rolling out a loom with preflight does not require rolling out
  fleet-db first.
- `strict` — treat an unverifiable fleet-db as incompatible and exit 1.
- `off` — skip the preflight entirely. The escape hatch for local and
  development fleet-dbs.

An unrecognised value reads as `warn`: a typo must not turn a boot into an exit,
and must not silently disable the check either.

## Reading the incompatibility message

```
Error: fleet-db is not compatible with this loom build (reason=incompatible).

  loom build:  a74c7e18e
  fleet-db:    adca220cdce0  (api_version 3, http://127.0.0.1:3012)

  Missing, required:
    - skills                         GET  /api/v1/{workspace}/skills
        needed by: skill materialization

  Deploy a fleet-db that serves the required routes, or run a loom build that does not
  require them. Set LOOM_FLEETDB_PREFLIGHT=off to bypass this check.
```

Both builds are named because the fix is always to move one of them: deploy a
fleet-db that serves the route, or run a loom build that does not need it. The
route line is the exact path an operator has to see served.

## Adding a requirement

Add an entry to `fleetdbcap.Requirements()` when loom starts calling a new
fleet-db route family — and only then. The `Capability` name must match the name
fleet-db reports (its `internal/api` `CapabilityManifest`); a name fleet-db can
never report would make every deployment look incompatible. A `Degrades` entry
must carry a `DegradedEffect` saying, in one line, what an operator will observe.
