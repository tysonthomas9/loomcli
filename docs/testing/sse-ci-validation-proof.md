# SSE stack CI prerequisite repair

PR #657's [Go Quality Gate](https://github.com/tysonthomas9/loomcli/actions/runs/33999891185/job/101396742167)
reported zero golangci-lint issues, then failed the separate raw-exec guard on
`internal/cli/git/git_status_recovery_test.go:58`. That test intentionally invokes
Git in a `t.TempDir()` repository to cover clean, detached, and missing-target
behavior. The call now carries the existing `norawexec` exemption convention and
its fixture rationale; production command execution is unchanged.

The same job revealed a second problem: `check-control-plane-paths.sh` printed
`rg: command not found` four times and still reported success because its searches
allow a no-match exit. The Go CI job now installs ripgrep explicitly. The script
also checks the dependency before starting and exits 127 when it is unavailable,
so an unperformed architecture scan cannot appear successful for this reason.
A shared search helper accepts only ripgrep exit 0 (matches) and exit 1 (no
matches); search errors propagate. The mode search runs outside the downstream
filter pipeline so its error cannot be swallowed there.

The [FleetDB Action Contract job](https://github.com/tysonthomas9/loomcli/actions/runs/33999891185/job/101396742196)
has a separate configuration blocker: `FLEET_DB_REPO_TOKEN` was empty. Its log
states that access to `BrowserOperator/fleet-db` is required and that pinned
producer and upstream drift checks did not execute. This repair does not provide
credentials or mark that contract check as passed.

## Local evidence

This is pure-code validation: shell checks and the existing real Git subprocess
fixture in an isolated temporary repository. No Loom runtime, Fleet service,
browser, or external agent was started. Commands ran from the isolated
`fix/sse-ci-validation` worktree.

- `bash -n scripts/check-control-plane-paths.sh` and `git diff --check` passed.
- The workflow parsed with Ruby's YAML parser. This checks syntax, not GitHub
  execution or whether package installation succeeds on a hosted runner.
- Running the path guard with an empty `PATH` returned exit 127 with the explicit
  ripgrep requirement and no success message. The same script with the normal
  tool path passed.
- Deterministic temporary `rg` executables verify exit 1 permits a clean scan,
  exit 0 with forbidden matches fails the guard, and exit 2 propagates failure.
  An additional fixture errors only on the final mode search to exercise the
  former pipeline failure path. Each temporary executable is removed afterward.
- `scripts/check-no-raw-exec.sh` passed with the justified exemption.

Logs are `/private/tmp/sse-ci-control-plane-missing-rg.log`,
`/private/tmp/sse-ci-control-plane-success.log`, and
`/private/tmp/sse-ci-exec-guard.log`. Search-status fixtures are recorded in
`/private/tmp/sse-ci-control-plane-rg-{0,1,2}.log` and
`/private/tmp/sse-ci-control-plane-mode-rg-error.log`. The inspected CI logs are
`/private/tmp/loom-pr657-go-quality.log` and
`/private/tmp/loom-pr657-fleet-contract.log`.

The targeted Git command is:

```sh
GOCACHE=/private/tmp/loom-sse-integration-go-cache go test -race -p 1 ./internal/cli/git -run '^TestGitStatusSummary' -count=1
```

The targeted race run passed (exit 0, package time 1.820s). Its output is
retained in `/private/tmp/sse-ci-git-recovery.log`.
A subsequent hosted CI run is still required; local guard checks do not establish
that the full Go Quality Gate or FleetDB Action Contract is green.
