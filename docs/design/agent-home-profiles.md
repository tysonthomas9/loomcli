# Agent Home Profiles

Each agent the daemon supervises can run against its **own harness
configuration root** instead of the operator's `~/.claude` / `~/.codex`:

```
<workspace>/.loom/agent-profiles/<agent>/claude   -> exported as CLAUDE_CONFIG_DIR
<workspace>/.loom/agent-profiles/<agent>/codex    -> exported as CODEX_HOME
```

Directory existence is the whole contract. There is no config key and no flag:
an agent with no profile directory inherits the operator's roots exactly as
before, which is why the feature can be rolled out — and rolled back — one
agent at a time by renaming a directory.

Path resolution lives in `internal/agentprofile`, deliberately stdlib-only, so
the half that *injects* the paths (the supervisor, at spawn) and the halves that
*discover* them from outside the process (transcript mirroring, `loom doctor`)
cannot drift apart. A duplicated `filepath.Join` on each side is precisely how
the writer ends up in one directory and every reader in another, and that drift
is silent.

## The manifest

A provisioned profile root carries `.manifest.json`:

```json
{
  "files": ["CLAUDE.md", "settings.json"],
  "fingerprint": "<sha256 hex>",
  "harness_version": "2.1.237 (Claude Code)"
}
```

`fingerprint` is sha256 over the concatenation, in listed order, of
`relative path + NUL + file bytes`.

`files` is an **allowlist, not an inventory**. Files present in the directory
but absent from the list are never read: the harness owns them and rewrites
them at runtime by design (`.credentials.json`, `.claude.json`, `sessions/`).
Hashing the whole directory would make every profile fail verification within
minutes of first use.

The manifest carries **two independent guarantees**, and they fail for
different reasons and are repaired by different people:

| field | guarantee | violated when |
|---|---|---|
| `fingerprint` | the profile's *content* is what was provisioned | a file was edited, truncated, or deleted underneath the agent |
| `harness_version` | this content was *blessed against this harness build* | the harness auto-updated since provisioning |

## Why the version pin is exact, and must never become a range

The pin does not assert "this profile probably works with this version". It
asserts that a human ran the profile against exactly this harness build and
accepted the result. That claim cannot be extrapolated: harness releases change
settings schemas, hook contracts, and prompt-file handling within a patch
digit, and the failure mode is not a crash but an agent that comes up subtly
misconfigured and keeps working.

A range would convert every future release into an untested assertion made in
advance by whoever wrote the range. Widening it is always the tempting fix when
an agent refuses to boot at 3am, and it always removes the only thing that made
the pin worth having. **No ranges, no prefix matching, no "close enough".**

For the same reason there is **no automatic re-blessing anywhere in the
daemon**. Nothing on the spawn path may write a manifest. Blessing an upgrade
is an operator-typed command, because it is the moment the claim above is made.

## Verify or refuse

The supervisor verifies a profile root *before* it exports the variable
(`appendProfileEnv` in `internal/cli/daemon/supervisor/spawn.go`). Verification
failure is a **boot failure for that one agent**, never a fallback to the
legacy roots — silently running an agent against the operator's full `~/.claude`
is the exact leak per-agent profiles exist to close. Degrading per agent keeps
the blast radius at the one agent whose profile is broken.

Each refusal names which repair applies:

| sentinel | meaning | repair |
|---|---|---|
| `ErrProfileManifestMissing` | directory exists, was never provisioned | `scripts/provision-profile.sh <agent>` |
| `ErrProfileManifestUnreadable` | manifest corrupt, or a listed file is gone | `scripts/provision-profile.sh <agent>` |
| `ErrProfileFingerprintMismatch` | content changed since provisioning | `scripts/provision-profile.sh <agent>` |
| `ErrProfileVersionDrift` | harness upgraded since the profile was blessed | `loom doctor --fix` |
| `ErrProfileVersionUnknown` | `<harness> --version` produced nothing | fix the harness install |

## The failure signature: an agent that looks idle

This is the symptom to recognize, because it is not obviously a profile
problem. A refused agent never reaches the harness, so there is no transcript,
no session, and no error in the agent's own log — from the board it simply
**looks idle**, indefinitely, while everything else keeps running.

The path from symptom to fix is:

```
agent looks idle  ->  loom doctor        # names the profile and the reason
                  ->  loom doctor --fix  # re-blesses a harness upgrade
```

Version drift is the common case and it arrives on its own schedule: the
harness auto-updates, and every profiled agent stops booting at the next spawn
without anyone having changed anything in this repo.

## Who repairs what

The split is not cosmetic — it is why `--fix` is safe to run:

- **`loom doctor --fix` re-blesses a version.** It rewrites `harness_version`
  on a profile whose fingerprint still verifies. It never provisions content,
  never writes profile files, and never touches credentials. (The doctor-side
  check and its `--fix` land with their own phase of this feature; the
  refusal messages already point at it.)
- **`scripts/provision-profile.sh <agent>` is the only thing that provisions.**
  It is workspace-owned, outside this repo, creates or replaces the profile's
  content, seeds the keychain slot the harness authenticates against, and
  writes the manifest from scratch. It is the operator's tool and stays that
  way: anything that can mint a profile can mint an agent's credentials.

Editing a profile file by hand is therefore never the repair. It breaks the
fingerprint, and the next spawn refuses.

## `loom lead` and the same profile root

`loom lead` is the one agent the supervisor does **not** spawn. The workspace
launcher (`scripts/lead-isolated.sh`) exports `CLAUDE_CONFIG_DIR` itself, and
`lead` inherits it because the variable is on the envfilter allowlist — so
`appendProfileEnv`'s verification never ran on it. Measured 2026-08-20: the
`lead` profile sat two harness versions behind every other profile and took
effect anyway, on the one agent that runs interactively in the operator's own
terminal.

`runLead` now applies the same rule before any backend work
(`internal/cli/agent/lead/profile.go`):

1. `CLAUDE_CONFIG_DIR` unset — nothing to verify. An unprofiled `lead` is a
   supported configuration and stays completely silent.
2. Set to a directory *outside* `<workspace>/.loom/agent-profiles/` — nothing to
   verify. An operator pointing `lead` at their own alternate config root is
   not this check's business; nothing here provisioned it and nothing here can
   repair it.
3. Otherwise verify, and on failure print the reason and the repair to stderr
   and exit non-zero.

It refuses rather than unsetting the variable and continuing, for the same
reason the spawn path does. The launcher script needs no change: it is
workspace-owned, and the check belongs where the guarantee is, in the binary.
