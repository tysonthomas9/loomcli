# Loom v6 Execution-Agent Priorities and TypeScript Examples

**Status:** Decision companion; proposed APIs are illustrative, not accepted<br>
**Date:** 2026-07-22<br>
**Loom base:** `faedb6e00472286d5dfa858a1bcef40154f6ac7b`<br>
**Companion research:**
[`2026-07-22-v6-execution-agent-layer-research.md`](2026-07-22-v6-execution-agent-layer-research.md)

## Decision summary

Loom should build a provider-neutral execution-agent layer under the existing
lease-bound runner client. The first release is coherent only if it handles the
entire path from a fenced `TaskRun` through environment setup, agent execution,
repository change capture, durable evidence, guarded delivery, cleanup, and
idempotent completion.

The v6 priority rule is:

- **Must-have** means required before Loom freezes or advertises a reusable,
  provider-neutral execution-agent SDK contract.
- **Good-to-have** means the contract must leave room for the capability, but
  the first execution-agent release does not depend on it.
- **Non-goal** means deliberately excluded because it would collapse authority
  boundaries or duplicate another system.

SDK parity is capability parity within the correct authority boundary, not one
TypeScript method for every Loom CLI command.

| Surface | Current state | v6 decision |
|---|---|---|
| `@loom/sdk/driver` | Checked-in run-scoped orchestration client | Preserve it for `DriverRun` orchestration and child `TaskRun` dispatch. |
| `@loom/sdk/runner` | Checked-in client for one leased `TaskRun` | Add an `execution` namespace without widening its lease or fence authority. |
| `@loom/sdk/runtime-adapters` | Checked-in authority-free transcript/usage helpers | Reuse the helpers, then evolve them behind provider adapter contracts. |
| `@loom/sdk/control` | Not shipped | Build separately for operator/application actions; never inject it into TaskRun code. |
| Loom CLI | Shipped human/operator interface | Keep as a valid bridge, but do not treat subprocess access as native SDK support. |

The npm package is not yet published. “Checked-in SDK” below means the v6
package contract, not public npm availability.

## Must-have before the public execution contract

The list is ordered by dependency, although events, fencing, cancellation,
redaction, and budgets cut across every stage.

| Order | Capability | Minimum coherent scope | First gate |
|---|---|---|---|
| 1 | Authority and compatibility boundary | Additive `TaskRunClient.execution`; one TaskRun lease/fence; no operator or foreign-run authority; preserve frozen v1 methods. | Internal vertical slice |
| 2 | Execution identity and lifecycle | Stable `executionKey` and invocation keys; `open/attach`; fence validation; cancellation observation; idempotent finalization. A new top-level database resource is not required initially. | Internal vertical slice |
| 3 | Server-resolved profile, credential containment, and preflight | Caller selects an authorized profile name; the server resolves placement, harness, environment, budgets, egress, credential references, repository, delivery, and cleanup. Only the trusted component that needs a credential can access it. Unsupported requirements fail before model work. | Internal vertical slice |
| 4 | Canonical events and durable evidence | Monotonic identity-bearing events; logs, transcript, usage, diff, tests, commit, delivery, placement, errors, redaction, and cleanup evidence; required artifacts finalize before completion or deletion. | Internal vertical slice |
| 5 | Portable environment core | Provision/attach identity, health, structured process spawn, streams, input, timeout, process-tree cancellation, bounded filesystem operations, capability discovery, and policy-driven release/delete/retain. | Internal vertical slice |
| 6 | Generic process-agent adapter | Structured `argv`, invocation identity, canonical events, transcript/usage parsing, timeout/cancellation, and structured process/model failures. Invalid invocation specifications throw. | Internal vertical slice |
| 7 | Repository service | Exact ref/SHA verification, checkout/worktree, status, changed files, text/binary diff, patch application, branch/add/commit, and durable output-branch/commit metadata. | Internal vertical slice |
| 8 | Trusted delivery service | Guarded push and PR create/update with freshness conditions, deterministic effect IDs, connector policy, durable provider responses, and no raw forge credential in model-controlled state. | Public-contract gate |
| 9 | Cancellation, cleanup, and finalization ordering | Propagate cancellation through harness and environment processes; redact and scrub; publish evidence before cleanup; persist cleanup outcome; finalize idempotently. | Internal vertical slice |
| 10 | Contract and protocol tests | Fence loss, duplicate open/finalize, cancellation, agent failure, artifact failure, and cleanup ordering gate the internal slice. Environment conformance for local/Daytona, harness conformance for process/Flue, ambiguous-delivery tests, and supported environment × harness vertical slices gate the public contract. | Both gates |

### Two delivery gates

**Gate A — internal vertical slice**

Build on the existing TaskRun transport with:

1. `runner.execution` context and server-resolved profile.
2. Local environment adapter.
3. Generic process-agent adapter.
4. Repository and evidence services.
5. Patch/commit output with deterministic cleanup and finalization.
6. Core fencing, cancellation, artifact, cleanup, and idempotency tests.

This gate can demonstrate the architecture without claiming provider
neutrality.

**Gate B — public-contract stabilization**

Before freezing the public surface, add:

1. Daytona as a second environment family.
2. Public Flue integration as a second harness family.
3. Trusted guarded push and PR delivery.
4. Cross-adapter conformance and failure-injection tests.

One implementation is not enough evidence for a portable abstraction; it is
too easy to freeze local-process assumptions into the interface.

### Dependency path

```text
authority/schema
    -> resolved profile and capability preflight
    -> lease-bound execution envelope
    -> environment allocation
    -> exact repository checkout
    -> harness invocation
    -> change, test, and commit capture
    -> trusted delivery effect
    -> cleanup
    -> idempotent TaskRun finalization
```

## Good-to-have after the coherent first release

| Area | Capability | Why it does not block the first execution release |
|---|---|---|
| Control plane | Public `@loom/sdk/control` for driver/version/profile/binding/approval/fleet administration | Essential for a complete TypeScript platform, but it has different credentials and authority from TaskRun execution and can ship on a parallel track. |
| Triggers | TypeScript management of cron, webhook, GitHub, CI, and internal-event bindings | Existing CLI/API paths work; trigger administration is not runner execution. |
| Approvals | Durable mid-agent approval parking and resume | The first release can enforce policy and gate delivery after the agent returns. Exact mid-turn parking needs waiting semantics and reattachable harness/environment state. |
| Composition | Same-TaskRun subordinate harness invocations with depth, fan-out, budgets, and cancellation | Useful for subagents, but child `TaskRun` and `DriverRun` orchestration already belongs to the Driver SDK. |
| Continuity | Cross-run `AgentThread` or `Conversation` | One invocation can remain one `AgentSession`; persistent conversation identity need not block execution. |
| Environment lifetime | Reuse across TaskRun attempts, snapshots, forks, persistent/cache volumes | The safe default is one environment lease per attempt. |
| Interactive processes | PTY reattachment, managed background processes, and preview endpoints | Valuable product capabilities, but not required for finite non-interactive coding work. |
| Rich environments | Browser/GUI/computer use, stateful interpreters, and LSP | Model as negotiated optional capabilities after the portable core is proven. |
| Connections | General non-readable connection and secret broker | Credential containment is mandatory now; a universal broker can follow. |
| Additional runtimes | Eve, remote agents, and other application runtimes | The adapter seam should allow them, but Flue plus a generic process adapter prove the first harness contract. |
| Additional environments | Kubernetes, E2B, Modal, and other providers | Daytona plus local execution are sufficient for the first portability gate. |
| Rich delivery | Review, comment, merge, reparenting, and stack reconciliation | The first delivery contract needs guarded push and PR create/update; later effects can reuse the same ledger and policy model. |
| Operations | Provider capacity, metrics, retained-resource administration, richer evals, and OpenTelemetry views | Important operational work that can build on canonical events and placement records. |

### Scope-dependent control SDK decision

`@loom/sdk/control` is **good-to-have for the execution-agent v1 release** but
**must-have for a complete Loom TypeScript platform**. Keeping those statements
separate avoids putting operator credentials into the runner merely to achieve
CLI parity.

## Hard non-goals

- One-to-one Loom CLI method parity in a single client.
- `epicRunner`, `cronRunner`, `gitRunner`, or provider-named authority types.
- Shell, filesystem, or environment control in the Driver SDK.
- Operator/control-plane authority in TaskRun code.
- Direct FleetDB authority inside Daytona or another sandbox.
- Raw organization, provider, or GitHub credentials in model-controlled state.
- Treating local Git command details as the cloud delivery contract.
- A second DriverRun checkpoint/replay engine modeled after Eve.
- Transparent replay of arbitrary TypeScript or effects with unknown outcomes.
- Reimplementing Flue's agent loop, tools, skills, compaction, or providers.
- Wrapping every Daytona administrative API in Loom.
- Treating a Flue or Eve subagent as a Loom child `TaskRun`.
- Silently redefining `AgentSession` as a durable multi-run conversation.
- Loom-generated Flue projects or restoration of `@loom/sdk/flue`.
- A universal nullable environment interface containing every provider feature.

## How to read the examples

Each of the ten examples has two snippets:

- **Possible now** uses the exact checked-in SDK where one exists. Labels such
  as “composition,” “CLI bridge,” “vendor SDK,” or “raw HTTP” identify work the
  public Loom SDK does not own.
- **Future proposed** uses illustrative names. Those snippets intentionally do
  not compile against v6 today and do not freeze package or method names.

The snippets are intentionally small. Production code still needs complete
validation, retries where safe, redaction, timeouts, and error handling.
Identifiers such as `assignedRepository`, `repoDir`, `patch`, `usage`,
`maintenanceVersionId`, and pull-request fields represent validated values
produced by the surrounding trusted runner flow; they are not arbitrary model
output.

| # | Scenario | Possible now through | Proposed home | Priority |
|---|---|---|---|---|
| 1 | Bootstrap one TaskRun execution | Public Runner SDK | `runner.execution` | Must-have |
| 2 | Run an external coding-agent CLI | Node process APIs plus Runner SDK | Process-agent adapter | Must-have |
| 3 | Integrate Flue and persist Loom evidence | Flue plus runtime-adapter helpers | First-party Flue adapter | Must-have before public freeze |
| 4 | Acquire Daytona compute | Daytona SDK plus runtime credential | Environment adapter | Must-have before public freeze |
| 5 | Inspect and commit Git changes | Git CLI | Repository service | Must-have |
| 6 | Publish evidence and complete | Runner artifacts plus completion | Evidence/finalization service | Must-have |
| 7 | Push and create a pull request | Git CLI, raw GitHub API, raw credential | Trusted delivery service | Must-have before public freeze |
| 8 | Dispatch work from `epic-runner` | Public Driver SDK | Same Driver SDK plus named execution profile | Must-have boundary |
| 9 | Create a cron binding from TypeScript | Loom CLI subprocess | Control SDK binding client | Good-to-have for execution; must-have for full TS platform |
| 10 | Run an Eve-backed agent | Application-owned HTTP integration | Eve remote-agent/runtime adapter | Good-to-have |

## 1. Bootstrap one TaskRun execution

**Possible now — checked-in Runner SDK**

```ts
import { TaskRunClient } from "@loom/sdk/runner";

const run = TaskRunClient.fromEnv();
const request = run.request<Record<string, unknown>>();
const input = run.input<{ prompt: string }>();
const taskRun = await run.getTaskRun();

await run.heartbeat({
  runtimeMetadata: { phase: "preflight" },
});
await run.logs.append({
  stream: "stdout",
  text: `starting ${taskRun.task_run_id}\n`,
});
```

Today this exposes the request, TaskRun, heartbeat, log, artifact, credential,
and completion primitives. It does not create a recoverable execution envelope
or resolve a capability profile.

**Future proposed — lease-bound execution envelope**

```ts
import { TaskRunClient } from "@loom/sdk/runner";

const run = TaskRunClient.fromEnv();
const execution = await run.execution.open({
  executionKey: "task-run-attempt",
  profile: "coding-default",
});

const readiness = await execution.preflight();
readiness.require(["process", "filesystem", "git"]);
await execution.heartbeat({ phase: "ready" });
```

The server resolves the named profile and rejects missing capabilities before
an agent process or session exists.

## 2. Run an external coding-agent CLI

**Possible now — Node composition around the Runner SDK**

```ts
import { execFile } from "node:child_process";
import { TaskRunClient } from "@loom/sdk/runner";

const run = TaskRunClient.fromEnv();
const { prompt } = run.input<{ prompt: string }>()!;

// Assigned by trusted runner setup after it establishes the outer isolation
// and repository boundary. Never take this cwd from TaskRun input.
declare const assignedRepository: { cwd: string; isolationVerified: boolean };
if (!assignedRepository.isolationVerified) {
  throw new Error("Codex bypass mode requires Loom-managed isolation");
}
const cwd = assignedRepository.cwd;

const result = await new Promise<{
  code: number;
  stdout: string;
  stderr: string;
}>((resolve, reject) => {
  const child = execFile(
    "codex",
    ["exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "-"],
    { cwd, timeout: 30 * 60_000, maxBuffer: 16 * 1024 * 1024 },
    (error, stdout, stderr) => {
      if (
        error &&
        typeof error.code !== "number" &&
        !error.killed &&
        !error.signal
      ) {
        reject(error);
        return;
      }
      resolve({
        code: error && typeof error.code === "number"
          ? error.code
          : error?.killed || error?.signal
            ? 124
            : 0,
        stdout: String(stdout ?? ""),
        stderr: String(stderr ?? ""),
      });
    },
  );
  // Headless Codex reads the prompt from stdin. Closing stdin prevents a
  // non-TTY invocation from hanging. The bypass flag is safe only inside the
  // Loom-managed isolation and policy boundary used by this runner.
  child.stdin?.end(prompt);
});

await run.logs.append({ stream: "stdout", text: result.stdout });
if (result.stderr) {
  await run.logs.append({ stream: "stderr", text: result.stderr });
}
if (result.code !== 0) throw new Error(`Codex exited ${result.code}`);
```

This is possible, and the built-in local runner does substantially more robust
work, but no public Loom process or agent-invocation contract exists.

**Future proposed — generic process-agent adapter**

```ts
import { processAgent } from "@loom/adapter-process";

const result = await execution.agent.invoke({
  invocationKey: "implement",
  adapter: processAgent({ transcript: "codex-jsonl" }),
  argv: ["codex", "exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "-"],
  stdin: execution.input.prompt,
  cwd: execution.repository.path,
  timeoutMs: 30 * 60_000,
});

if (!result.ok) {
  await execution.finalize({ status: "failed", error: result.error });
}
```

The adapter owns process capture and normalization. The leaf still owns the
chosen command arguments and semantic interpretation of the result.

## 3. Integrate Flue and persist Loom evidence

**Possible now — persist an application-owned Flue event stream**

```ts
import { TaskRunClient } from "@loom/sdk/runner";
import {
  createFlueTranscriptCollector,
  flueEventsToTaskUsage,
  redactTranscriptEntries,
  serializeTranscriptJSONL,
} from "@loom/sdk/runtime-adapters";

async function persistFlueEvents(
  run: TaskRunClient,
  flueEvents: AsyncIterable<Record<string, unknown>>,
  secrets: string[] = [],
) {
  const collector = createFlueTranscriptCollector();
  const events: Record<string, unknown>[] = [];

  for await (const event of flueEvents) {
    events.push(event);
    collector.push(event);
  }

  const transcript = await run.artifacts.declare({
    type: "transcript",
    mimeType: "application/x-ndjson",
    summary: "Flue transcript",
  });
  const redacted = redactTranscriptEntries(collector.entries, secrets);
  await transcript.upload(serializeTranscriptJSONL(redacted));
  await transcript.finalize();

  return { transcriptId: transcript.id, usage: flueEventsToTaskUsage(events) };
}
```

The helpers normalize events after an application has produced them. The
current Daytona runner embeds Flue through `@flue/runtime/internal`; that
private API is not a Loom SDK contract.

**Future proposed — first-party Flue adapter after an embedding decision**

```ts
import { flueAgent } from "@loom/adapter-flue";

const result = await execution.agent.invoke({
  invocationKey: "implement",
  adapter: flueAgent(agentDefinition),
  cwd: execution.repository.path,
  input: { prompt: execution.input.prompt },
});

await execution.evidence.publish(result.evidence);
```

This exact adapter requires Loom to upgrade to and test a public Flue embedding
API, call Flue out-of-process through its public routes/SDK, or maintain an
explicitly version-pinned compatibility shim. The current pinned beta.2 public
surface does not expose the embedding hook used by the Daytona runner.

Flue owns its canonical agent conversation and conservative turn recovery when
durable-agent storage is configured; it does not checkpoint arbitrary workflow
TypeScript. Loom owns the TaskRun evidence projection while preserving Flue
source identities and offsets, plus placement, delivery, cleanup, and outcome.

## 4. Acquire Daytona compute

**Possible now — vendor SDK plus the checked-in credential broker**

```ts
import { Daytona } from "@daytona/sdk";
import { TaskRunClient } from "@loom/sdk/runner";

const run = TaskRunClient.fromEnv();
// Requires LOOM_TASK_RUN_API_URL. The direct-FleetDB runner transport cannot
// broker runtime credentials.
const { value: apiKey } = await run.runtimeCredentials.get({
  provider: "daytona",
});

const daytona = new Daytona({ apiKey });
const sandbox = await daytona.create({
  labels: { loom: "task-runner", task_run_id: run.taskRunId },
  autoStopInterval: 15,
  autoDeleteInterval: 0,
});

try {
  const result = await sandbox.process.executeCommand(
    "git status --short",
    undefined,
    undefined,
    60,
  );
  await run.logs.append({ stream: "stdout", text: result.result ?? "" });
} finally {
  await sandbox.delete(60);
}
```

The built-in runner also reports sandbox identity in terminal runtime metadata,
adapts filesystem/process operations to Flue, clones the repository, and
applies cleanup policy itself. The Daytona account credential belongs in
trusted runner code, never in the model-controlled sandbox.

**Future proposed — provider-neutral environment handle**

```ts
const execution = await run.execution.open({
  executionKey: "task-run-attempt",
  profile: "coding-daytona-flue-v1",
});

const command = await execution.environment.spawn({
  argv: ["git", "status", "--short"],
  cwd: execution.repository.path,
  timeoutMs: 60_000,
});

for await (const event of command.events) {
  console.log(event); // Observation only; the adapter persists canonical events.
}
const result = await command.wait();
```

The profile chooses Daytona and the trusted executor provisions it. TaskRun
code receives a scoped capability handle, not organization-wide provider
credentials. A conforming adapter persists canonical events automatically so a
caller cannot omit, duplicate, or reorder required evidence.

## 5. Inspect and commit Git changes

**Possible now — Git CLI composition**

```ts
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

await execFileAsync("git", ["-C", repoDir, "add", "-A"]);
const { stdout: patch } = await execFileAsync(
  "git",
  ["-C", repoDir, "diff", "--cached", "--binary", "--", "."],
  { encoding: "utf8", maxBuffer: 32 * 1024 * 1024 },
);
const { stdout: status } = await execFileAsync(
  "git",
  ["-C", repoDir, "status", "--porcelain=v2"],
  { encoding: "utf8" },
);

if (status.trim()) {
  await execFileAsync("git", ["-C", repoDir, "commit", "-m", taskTitle]);
}
```

Local and Daytona built-ins implement variants of this directly. The public
SDK has no repository namespace, exact-ref contract, conflict model, or durable
change object.

**Future proposed — repository service**

```ts
const repo = await execution.repository.checkoutAssigned();

const status = await repo.status();
const change = await repo.captureChange({ binary: true });
const commit = await repo.commit({
  message: `${execution.taskId}: ${execution.input.title}`,
  expectedBaseSha: repo.baseSha,
});
```

Checkout-local Git is execution authority. Push, PR, review, and merge are
separate externally visible effects.

## 6. Publish evidence and complete the TaskRun

**Possible now — checked-in artifacts plus manual completion**

```ts
import { TaskRunClient } from "@loom/sdk/runner";

const run = TaskRunClient.fromEnv();

const patchArtifact = await run.artifacts.declare({
  id: `patch-${run.taskRunId}`,
  type: "patch",
  mimeType: "text/x-diff",
  summary: "Repository patch",
});
await patchArtifact.upload(patch, { mimeType: "text/x-diff" });
await patchArtifact.finalize({
  sizeBytes: Buffer.byteLength(patch),
  summary: "Repository patch",
});

await run.completeRun({
  completionId: `complete-${run.taskRunId}`,
  status: "completed",
  artifactIds: [patchArtifact.id],
  requireArtifacts: true,
  inputTokens: usage.input_tokens,
  outputTokens: usage.output_tokens,
  estimatedCostUsd: usage.estimated_cost_usd,
});
```

This is a strong existing base, but every leaf currently decides artifact
types, event normalization, required evidence, cleanup order, and terminal
mapping independently.

**Future proposed — evidence-aware idempotent finalization**

```ts
await execution.evidence.publish(change);
await execution.evidence.publish(result.transcript);
await execution.evidence.publish(result.usage);
await execution.evidence.publish(validation);

const cleanup = await execution.environment.release({ policy: "resolved-profile" });
await execution.evidence.publish(cleanup);

await execution.finalize({
  completionKey: "semantic-outcome-v1",
  status: result.ok && validation.ok ? "completed" : "failed",
  requiredEvidence: ["transcript", "change", "validation", "cleanup"],
});
```

The helper can compose existing artifact and completion calls initially; the
semantics do not require an immediate second transport.

## 7. Push and create a pull request

**Possible now — hand-wired Git and GitHub effects**

```ts
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { TaskRunClient } from "@loom/sdk/runner";

const execFileAsync = promisify(execFile);
const run = TaskRunClient.fromEnv();
// Requires LOOM_TASK_RUN_API_URL. The direct-FleetDB runner transport cannot
// broker runtime credentials.
const { value: githubToken } = await run.runtimeCredentials.get({
  provider: "github",
});

const credentialHelper =
  '!f() { echo username=x-access-token; echo "password=$LOOM_PR_GIT_PASSWORD"; }; f';
await execFileAsync("git", [
  "-C",
  repoDir,
  "-c",
  "credential.helper=",
  "-c",
  `credential.helper=${credentialHelper}`,
  "push",
  "--force-with-lease",
  `https://github.com/${owner}/${repo}.git`,
  `HEAD:refs/heads/${branch}`,
], {
  env: {
    ...process.env,
    LOOM_PR_GIT_PASSWORD: githubToken,
    GIT_TERMINAL_PROMPT: "0",
  },
});

const response = await fetch(`https://api.github.com/repos/${owner}/${repo}/pulls`, {
  method: "POST",
  headers: {
    authorization: `Bearer ${githubToken}`,
    accept: "application/vnd.github+json",
    "content-type": "application/json",
  },
  body: JSON.stringify({ title, head: branch, base: baseBranch, body }),
});
if (!response.ok) throw new Error(`GitHub PR create failed: ${response.status}`);
```

This mirrors the responsibilities currently hand-wired in built-ins, including
the local runner's environment-backed Git credential helper. Trusted runner
code still receives the credential, there is no shared effect ledger, and this
is not a public Runner SDK operation. The current Driver SDK has guarded
merge/review connectors but no PR-create operation.

**Future proposed — trusted delivery service**

```ts
const delivery = await execution.delivery.publish({
  effectId: `publish:${execution.taskRunId}:${change.contentHash}`,
  kind: "pull_request",
  change,
  expectedBaseSha: repo.baseSha,
});

await execution.evidence.publish(delivery);
```

The model chooses the change content. Trusted policy chooses the credential,
repository, destination, freshness rule, and whether approval is required.

## 8. Dispatch work from `epic-runner`

**Possible now — checked-in Driver SDK**

```ts
import { createLoomClient } from "@loom/sdk/driver";

export default async function runEpic() {
  const loom = createLoomClient();
  const epicId = String(loom.input.epicId ?? "");
  const inFlight = new Set<string>();

  async function topUp() {
    while (inFlight.size < 2) {
      const task = await loom.tasks.claimReady({ epicId });
      if (!task) return;
      const taskId = String(task.id);
      await loom.taskRuns.request({
        taskId,
        runner: "local-task-runner",
        workerProfileId: "coding-default",
      });
      inFlight.add(taskId);
    }
  }

  await topUp();
  for await (const event of loom.epics.watch({ epicId })) {
    if (event.type === "closed") {
      return loom.failed({
        summary: `epic watch for ${epicId} closed unexpectedly`,
        errorClass: "epic_watch_closed",
      });
    }
    if (event.type === "taskRun") {
      const data = event.data as { taskID?: string; type?: string };
      if (["taskRunCompleted", "taskRunFailed", "taskRunCancelled"].includes(
        String(data.type),
      )) {
        // Production epic-runner also reconciles completion/failure evidence.
        inFlight.delete(String(data.taskID));
        await topUp();
      }
    }
    if (inFlight.size === 0) {
      const snapshot = await loom.epics.snapshot({ epicId });
      if (Number(snapshot?.openChildrenCount ?? 0) === 0) {
        return loom.completed({ summary: `epic ${epicId} drained` });
      }
    }
  }
  return loom.failed({
    summary: `epic watch for ${epicId} ended unexpectedly`,
    errorClass: "epic_watch_ended",
  });
}
```

This is a condensed one-file view of the shipped
`claimReady → taskRuns.request → epics.watch → topUp → snapshot` loop. The
production workflow also reconciles re-entry, deterministic TaskRun IDs,
terminal evidence, blocked DAG branches, and task completion. `epic-runner` is
not a missing `epicRunner()` primitive.

**Future proposed — same orchestration API, named execution profile**

```ts
await loom.taskRuns.request({
  taskId,
  runner: "execution-agent",
  workerProfileId: "coding-default",
  input: { prompt: task.description },
});
```

V6 can resolve the server-owned execution policy from the existing authorized
`workerProfileId`. Introduce a separate `executionProfile` field only if it has
genuinely distinct semantics; do not create two overlapping profile selectors.
The driver cannot supply arbitrary secrets or sandbox policy.

## 9. Create a cron binding from TypeScript

**Possible now — TypeScript shells out to the Loom CLI**

```ts
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);
const { stdout } = await execFileAsync("loom", [
  "trigger",
  "bindings",
  "create",
  "--source",
  "cron",
  "--route-key",
  "cron.nightly-maintenance",
  "--workflow",
  "maintenance",
  "--schedule",
  "0 2 * * *",
  "--schedule-timezone",
  "UTC",
  "--concurrency-policy",
  "forbid",
  "--json",
]);

const binding = JSON.parse(stdout);
```

This is a valid automation bridge around the shipped CLI. It is not native
TypeScript SDK support and depends on local workspace/config resolution.

**Future proposed — operator-scoped Control SDK**

```ts
import { createLoomControlClient } from "@loom/sdk/control";

const control = createLoomControlClient({ workspace: "acme" });
const binding = await control.bindings.create({
  sourceKind: "cron",
  routeKey: "cron.nightly-maintenance",
  driverVersionId: maintenanceVersionId,
  schedule: "0 2 * * *",
  scheduleTimezone: "UTC",
  concurrencyPolicy: "forbid",
});
```

Cron is durable trigger configuration. It invokes a driver; it does not create
a `cronRunner` and it never belongs on the lease-scoped Runner SDK.

Current v6 behavior note: the binding record is durable, but due ticks come
from the `loom serve` cron loop. Its cursor is process-local, first observation
does not backfill history, and missed ticks are collapsed. A Control SDK would
expose configuration; it would not by itself upgrade those scheduler semantics.

## 10. Run an Eve-backed agent

**Possible now by composition — not implemented or verified in Loom**

```ts
import { TaskRunClient } from "@loom/sdk/runner";

const run = TaskRunClient.fromEnv();
const endpoint = process.env.EVE_AGENT_ENDPOINT!;

// The endpoint and body are defined by the deployed Eve application, not Loom.
const response = await fetch(endpoint, {
  method: "POST",
  headers: {
    authorization: `Bearer ${process.env.EVE_APP_TOKEN}`,
    "content-type": "application/json",
  },
  body: JSON.stringify({
    threadId: run.taskRunId,
    input: run.input(),
  }),
});
if (!response.ok) throw new Error(`Eve invocation failed: ${response.status}`);

const eveResult = await response.json();
await run.logs.append({
  stream: "stdout",
  text: `${JSON.stringify(eveResult)}\n`,
});
```

This can work today, but the integration owns correlation, authentication,
cancellation, streaming, checkpoint semantics, transcript conversion, usage,
artifacts, and outcome mapping.

**Future proposed — Eve remote-agent/runtime adapter**

```ts
import { eveRemoteAgent } from "@loom/adapter-eve";

const invocation = await execution.agent.attachOrInvoke({
  invocationKey: "review",
  adapter: eveRemoteAgent({ connection: "eve-reviewer" }),
  input: execution.input,
});

execution.cancellation.onRequested(() => invocation.cancel());
for await (const event of invocation.observe()) {
  console.log(event);
}
const result = await invocation.wait();
await execution.evidence.publish(result.evidence);
```

Loom should govern assignment, capability grants, lineage, evidence, and final
TaskRun outcome without recreating Eve's internal sessions, tools, approvals,
subagents, sandboxes, schedules, or checkpoint engine. The adapter must map the
stable invocation key to an external Eve thread/run, reattach after retries,
and support observe and cancel. Its credential and outbound egress stay in a
runner-side scoped capability, never model-controlled state.

## Recommended implementation order

1. Add internal execution types and an additive `TaskRunClient.execution`
   facade that composes existing heartbeat, logs, artifacts, credentials, and
   completion operations.
2. Implement local environment plus generic process-agent adapters.
3. Add canonical events/evidence, repository operations, and deterministic
   cleanup/finalization.
4. Move one production local built-in to the new surface.
5. Implement trusted guarded push/PR delivery.
6. Add Daytona and public Flue adapters.
7. Run environment conformance for local and Daytona, harness conformance for
   process and Flue, and vertical-slice tests for each supported combination.
8. Freeze the public execution contract only after both paths pass.
9. Build the operator Control SDK as a separate authority track, starting with
   driver/version/profile and trigger-binding operations.
10. Add durable mid-turn approvals, Eve, richer environments, and other
    good-to-have adapters based on demonstrated product demand.

## Current implementation evidence

- [`../../sdk/package.json`](../../sdk/package.json) — current package exports.
- [`../../sdk/runner.d.ts`](../../sdk/runner.d.ts) — TaskRun-scoped runner API.
- [`../../sdk/driver.d.ts`](../../sdk/driver.d.ts) — DriverRun orchestration and connector API.
- [`../../sdk/runtime-adapters.d.ts`](../../sdk/runtime-adapters.d.ts) — current Flue transcript and usage helpers.
- [`../../sdk/README.md`](../../sdk/README.md) — frozen v1 semantics and publishing status.
- [`../../internal/workflows/builtin/local-task-runner.ts`](../../internal/workflows/builtin/local-task-runner.ts) — current process/Git implementation.
- [`../../internal/workflows/builtin/daytona-task-runner.ts`](../../internal/workflows/builtin/daytona-task-runner.ts) — current Daytona/Flue/Git/delivery implementation.
- [`../../internal/workflows/builtin/epic-runner.ts`](../../internal/workflows/builtin/epic-runner.ts) — current orchestration workflow.
- [`../../internal/cli/trigger/trigger_cmd.go`](../../internal/cli/trigger/trigger_cmd.go) — current trigger/cron CLI.

## Bottom line

The must-have is not a long list of named runners. It is one narrow,
lease-bound execution path with trustworthy identity, environment, agent,
repository, evidence, delivery, cleanup, and conformance semantics. Cron,
operator diagnostics, Eve, richer sandboxes, and other conveniences can then
plug into the correct control or adapter boundary without widening TaskRun
authority.
