# meta-harness sandbox runner (Part C/D) — design

Status: **design / not yet implemented.** The local leaf (Parts A/B) and the
meta-harness **structured runner** (meta-harness v0.1.0, `meta-harness-structured-run`)
are done and tested; this doc specifies the remaining sandbox (Daytona) path. It is
**disabled by default** and must be developed/verified against a real Daytona sandbox
before enabling — it changes the execution model and handles credentials, and neither
is unit-testable without a sandbox (the daytona tests also need flue's `@daytona/sdk`).

## Execution model change

Today `daytona-task-runner.ts` is **host-driven**: `configureCodexAuth` registers a
flue provider with a host-side API key and `harness.session().prompt()` runs the model
via flue's HTTP provider (`@loom/sdk/runtime-adapters`); the sandbox is only a git
workspace (clone + diff via `setup.shell`).

The meta-harness path runs the **actual CLI in-guest** via the structured runner. This
gets real-PTY behavior + a canonical transcript, but the CLI now needs its **own**
credentials inside the sandbox — the crux of the credential contract below.

## Gate

`LOOM_DAYTONA_META_HARNESS` (default off). When unset/false, the existing host-driven
flue path is unchanged. When set to `claude`/`codex`/`all` (∩ the meta-harness-capable
set), the sandbox path below is used for those backends.

## Structured-runner invocation (safe transport)

The runner is the meta-harness `meta-harness-structured-run` bin (Part D stages it
in-guest). Drive it with a **file-based prompt** — never a shell-interpolated prompt:

1. Write the prompt to a guest file via the sandbox fs API
   (`DaytonaSandboxApi.writeFile` → `sandbox.fs.uploadFile`), e.g. `/tmp/loom-prompt-<taskRunId>`.
2. Invoke with a strictly-quoted argv (no interpolation of prompt/args):
   ```js
   // every token single-quoted (POSIX '\'' escaping); injection-safe.
   function argvToShell(argv) { return argv.map(shellQuote).join(" "); }
   const cmd = argvToShell([
     "node", structuredRunnerPath, "--prompt-file", promptPath, harness,
   ]);
   const res = await setup.shell(cmd, { timeout });
   ```
3. Parse the **last stdout line** as the JSON result
   `{ status, reply, harnessSessionID, transcript_entries, reason?, transcript_error?, working_dir }`
   and map it onto the existing `transcript_entries` / patch / delivery shape. The
   transcript is read **in-guest** by the runner (the on-disk session lives there), so
   no host-side Reader call is needed.

`argvToShell` + adversarial cases (quotes, newlines, spaces, leading `-`, `$()`,
backticks) are the one **unit-testable** piece here — land it with the code.

## Credential contract (the blocking security item)

The in-guest CLI needs credentials, but `sandboxLeakProbeCommand()` fails the run if any
sensitive value reaches the sandbox **environment**. Key reconciliation:

> **The leak probe is env-based. Provision the CLI's credential as a FILE, not env.**

- **Provision** the backend's credential into the CLI's expected on-disk auth path via
  the sandbox fs API (e.g. codex `~/.codex/auth.json` shaped like `loadCodexAuth`
  reads: `{ tokens: { access_token, refresh_token } }`; claude its token file). Use a
  **scoped, short-lived** token (refresh/mint per task), **not** the host's long-lived
  credential where avoidable.
- **Leak probe stays intact.** Because creds go to a file, the env-based probe still
  runs unchanged and still catches unintended env leaks. Do **not** widen the probe to
  allow cred env vars — file provisioning means we never need to.
- **Redaction.** Push the provisioned token into the existing `secrets[]` array so it is
  scrubbed from logs/transcript/patch (same as `auth.accessToken`/`githubToken` today).
- **Teardown.** Remove the auth file on completion (best-effort) alongside sandbox
  auto-stop/auto-delete.

If a backend's CLI only accepts creds via env (no auth file), that backend stays on the
host-driven flue path (do not env-inject into the sandbox).

## Part D — sandbox image layout

The sandbox image must materialize (mirror orche `sandbox/materialize.ts`), e.g. under
`/opt/meta-harness`:
- the built `dist` tree (so the structured runner **and** the transcript Readers run
  under the guest's Node — the transcript is read in-guest);
- `node_modules/node-pty` built for the **sandbox** OS/arch (linux glibc/musl — **≠** the
  host's prebuild; a host copy is invalid) + co-located `ptyHost.mjs`;
- guest env `META_HARNESS_PTY_HOST=/opt/meta-harness/dist/wrapper/internal/ptyHost.mjs`;
- `node` + `claude`/`codex` on `PATH` (or via `HARNESS_BINARY_*`).

Pin meta-harness in the image at the same SHA as `internal/workflows/META_HARNESS_COMMIT`.

## Test strategy

- **Unit (here):** `argvToShell` adversarial quoting; JSON result parsing/mapping.
- **Needs a sandbox (e2e):** the full turn — cred provisioning, leak probe still green,
  in-guest structured-runner turn, transcript back, patch/diff, redaction, teardown.
- The structured runner itself is already tested upstream (meta-harness
  `test/cli/structured-runner.test.ts`).
