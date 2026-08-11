// NOTE: this module must stay self-contained (no local imports): callers such as
// scripts/run-slack-codex-epic-runner-stack.sh vendor driver.js as a single file.
//
// Workflow driver operations use the loom serve driver-op HTTP API with
// camelCase JSON and structured errors.

const DEFAULT_HTTP_TIMEOUT_MS = 30_000;
const TASK_RUN_REQUEST_MAX_ATTEMPTS = 2;

// DriverApiError carries the structured v2 error envelope:
// {code, message, retryable} plus the HTTP status.
export class DriverApiError extends Error {
  constructor(message, { code, retryable, status, details } = {}) {
    super(message);
    this.name = "DriverApiError";
    this.code = code || "internal";
    this.retryable = Boolean(retryable);
    this.status = status || 0;
    if (details !== undefined) {
      this.details = details;
    }
  }
}

// WorkflowSuspended is the suspend sentinel thrown by loom.events.await and
// loom.workflows.await when the server suspended the run (await registered, no
// matching event yet). It is NOT a failure: the runner recognizes it (by
// .type/.name, or by the "workflow_suspended:" message prefix when only the
// message survives serialization) and exits cleanly with a suspended
// completion shape — no loom.completed call. Resume re-runs the workflow
// from the top; the same await call then replays its recorded event inline.
// Never swallow it: a catch block around an await must rethrow when
// isWorkflowSuspended(err) is true (or `return err.result`).
export class WorkflowSuspended extends Error {
  constructor(awaitIndex) {
    super(`workflow_suspended: await #${awaitIndex} suspended the run; exiting until the event arrives`);
    this.name = "WorkflowSuspended";
    this.type = "workflow_suspended";
    this.awaitIndex = awaitIndex;
    // result is the suspended completion shape for harnesses that prefer
    // `return err.result` over letting the sentinel propagate.
    this.result = Object.freeze({
      status: "suspended_awaiting_event",
      summary: "workflow suspended awaiting event",
    });
  }
}

export function isWorkflowSuspended(err) {
  return Boolean(err) && (err.type === "workflow_suspended" || err.name === "WorkflowSuspended");
}

export class LoomDriverClient {
  static fromEnv(options = {}) {
    return new LoomDriverClient({
	  env: options.env || process.env,
	  input: options.input,
	  apiUrl: options.apiUrl,
	  runToken: options.runToken,
    });
  }

  constructor(options = {}) {
    this.env = options.env || process.env;
	this.input = options.input || {};
	this.apiUrl = stripTrailingSlash(String(options.apiUrl || pickEnv(this.env, "LOOM_DRIVER_API_URL")));
	// runToken is the mandatory run-scoped bearer token minted at claim. It is
	// the only credential the client sends; the server derives run ownership
	// from its signed claims.
    this.runToken = String(options.runToken || pickEnv(this.env, "LOOM_RUN_TOKEN"));
    this.workspace = pickEnv(this.env, "LOOM_DRIVER_WORKSPACE");
    this.driverRunId = pickEnv(this.env, "LOOM_DRIVER_RUN_ID");
    this.taskRunResultsByTaskId = new Map();
    this.taskRunResultsByRunId = new Map();
    this.epics = Object.freeze({
      get: (input = {}) => this.getEpic(input),
      snapshot: (input = {}) => this.epicSnapshot(input),
      watch: (input = {}) => this.watchEpic(input),
    });
    this.agents = Object.freeze({
      list: (input = {}) => this.listAgents(input),
      orchestrationSession: (input = {}) => this.agentOrchestrationSession(input),
      updateParent: (input = {}) => this.updateAgentParent(input),
      deliverAssignment: (input = {}) => this.deliverLeadAssignment(input),
      message: (input = {}) => this.messageAgent(input),
    });
    this.tasks = Object.freeze({
      claimReady: (input = {}) => this.claimReady(input),
      claim: (input = {}) => this.claimTask(input),
      claimReview: (input = {}) => this.claimReview(input),
      handoffReview: (input = {}) => this.handoffReview(input),
      diff: (input = {}) => this.taskDiff(input),
      complete: (input = {}) => this.completeTask(input),
      release: (input = {}) => this.releaseTask(input),
      releaseReview: (input = {}) => this.releaseReview(input),
    });
    this.taskRuns = Object.freeze({
      request: (input = {}) => this.requestTaskRun(input),
      get: (input = {}) => this.getTaskRun(input),
      await: (input = {}) => this.awaitTaskRun(input),
      active: (input = {}) => this.activeTaskRuns(input),
      recoverStale: (input = {}) => this.recoverStaleTaskRuns(input),
    });
    this.connectorCallSeqs = new Map();
    this.connectors = buildConnectorsNamespace(this);
    this.awaitSeq = 0;
    this.workflowStartSeq = 0;
    this.events = Object.freeze({
      await: (input = {}) => this.awaitEvent(input),
      list: (input = {}) => this.listAwaits(input),
    });
    this.workflows = Object.freeze({
      start: (input = {}) => this.startWorkflow(input),
      await: (input = {}) => this.awaitChildWorkflow(input),
    });
    // <gen:namespaces> — generated by sdk/gen.mjs from sdk/op-spec.mjs (do not edit by hand)
    this.issues = Object.freeze({
      get: (input = {}) => this.#httpCall("issue-get", { issueId: input.issueId }),
      list: (input = {}) => this.#httpCall("issue-list", { externalRef: input.externalRef, type: input.type, status: input.status, limit: input.limit }),
      listComments: (input = {}) => this.#httpCall("issue-list-comments", { issueId: input.issueId }),
      comment: (input = {}) => this.#httpCall("issue-comment", { issueId: input.issueId, body: input.body }),
      update: (input = {}) => this.#httpCall("issue-update", { issueId: input.issueId, status: input.status, priority: input.priority, labels: input.labels, assignee: input.assignee, externalRef: input.externalRef }),
      blockRepositoryRequired: (input = {}) => this.#httpCall("issue-block-repository-required", { issueId: input.issueId }),
      addLabel: (input = {}) => this.#httpCall("issue-add-label", { issueId: input.issueId, label: input.label }),
      removeLabel: (input = {}) => this.#httpCall("issue-remove-label", { issueId: input.issueId, label: input.label }),
    });
    this.roles = Object.freeze({
      get: (input = {}) => this.#httpCall("role-get", { name: input.name }),
    });
    this.binding = Object.freeze({
      config: (input = {}) => this.#httpCall("binding-config", {}),
    });
    // </gen:namespaces>
  }

  completed(input = {}) {
    return { status: "completed", summary: input.summary || "completed" };
  }

  failed(input = {}) {
    return {
      status: "failed",
      summary: input.summary || "failed",
      errorClass: input.errorClass || "driver_failed",
    };
  }

  needsReview(input = {}) {
    return {
      status: "needs_review",
      summary: input.summary || "needs review",
      errorClass: input.errorClass || "needs_review",
      taskRunId: input.taskRunId,
      logsRef: input.logsRef,
      artifactsRef: input.artifactsRef,
    };
  }

  async claimReady(input = {}) {
    return this.#httpCall("claim-ready", {
      epicId: this.#epicID(input),
      // actor is accepted for wire-compat but IGNORED server-side: the lock is
      // keyed by the run's derived actor, never caller input (prevents
      // cross-agent lock takeover). Pass `type` to narrow the ready queue.
      actor: input.actor || "",
      type: input.type || "",
      sourceRepo: input.sourceRepo || "",
      limit: input.limit || "",
    });
  }

  // claimTask claims one SPECIFIC ready task by id (the event-driven counterpart
  // to claimReady's queue-order pull). taskId is required; the lock actor is
  // ALWAYS the run's derived actor server-side (a body actor is ignored — it
  // cannot key another run's lock); epicId is an OPTIONAL ready-view narrowing
  // hint (it is NOT defaulted from the run's epic, so any task can be targeted).
  // A not-ready / already-claimed target rejects with a DriverApiError code
  // "conflict".
  async claimTask(input = {}) {
    const taskId = taskPayloadID(input);
    if (!taskId) {
      throw new Error("tasks.claim requires taskId");
    }
    return this.#httpCall("claim-task", {
      taskId: String(taskId),
      actor: input.actor || "",
      epicId: input.epicId || "",
      limit: input.limit || "",
    });
  }

  async claimReview(input = {}) {
    const taskId = taskPayloadID(input);
    if (!taskId) {
      throw new Error("tasks.claimReview requires taskId");
    }
    return this.#httpCall("claim-review", { taskId: String(taskId) });
  }

  // taskDiff returns the bounded review diff for a card stamped with
  // external_ref="local-branch:<branch>@<sha>". The server resolves the
  // workspace repo's filesystem origin and computes defaultBranch...sha there;
  // non-local origins and missing refs fail closed with DriverApiError.code
  // task_diff_*.
  async taskDiff(input = {}) {
    const taskId = taskPayloadID(input);
    if (!taskId) {
      throw new Error("tasks.diff requires taskId");
    }
    return this.#httpCall("task-diff", { taskId: String(taskId) });
  }

  async getEpic(input = {}) {
    return this.#httpCall("epic-get", { epicId: this.#epicID(input) });
  }

  async epicSnapshot(input = {}) {
    return this.#httpCall("epic-snapshot", { epicId: this.#epicID(input) });
  }

  // watchEpic returns an async iterator over the epic watch SSE stream
  // (GET /api/workspaces/{ws}/driver/watch/epic), yielding {type, id, data}
  // where type is "snapshot" | "taskRun" | "closed". The iterator reconnects
  // automatically after stream end or network errors, resuming with the
  // Last-Event-ID cursor; it honors server "retry:" hints, ends after a
  // "closed" event, ends silently when input.signal aborts, and throws
  // DriverApiError for non-retryable HTTP failures (e.g. 401).
  watchEpic(input = {}) {
    this.#requireHttpConfig();
    return this.#watchEpicStream(input);
  }

  async *#watchEpicStream(input) {
    const epicId = this.#epicID(input);
    const signal = input.signal;
    let retryMs = Math.max(0, Number(input.reconnectMs ?? 2000));
    let lastEventId =
      input.afterSeq === undefined || input.afterSeq === null || input.afterSeq === ""
        ? ""
        : String(input.afterSeq);
    const query = new URLSearchParams();
    if (epicId) {
      query.set("epicId", String(epicId));
    }
    const queryString = query.toString();
    const url = `${this.apiUrl}/api/workspaces/${encodeURIComponent(this.workspace)}/driver/watch/epic`
      + (queryString ? `?${queryString}` : "");
    while (true) {
      if (signal?.aborted) {
        return;
      }
      const controller = new AbortController();
      const onAbort = () => controller.abort(signal?.reason);
      signal?.addEventListener("abort", onAbort, { once: true });
      try {
        const headers = this.#identityHeaders();
        headers.Accept = "text/event-stream";
        if (lastEventId !== "") {
          headers["Last-Event-ID"] = lastEventId;
        }
        const response = await fetch(url, { headers, signal: controller.signal });
        if (!response.ok) {
          throw await watchHttpError(response);
        }
        for await (const frame of sseFrames(response.body)) {
          if (frame.retryMs !== undefined) {
            retryMs = frame.retryMs;
          }
          if (frame.id !== undefined && frame.id !== "") {
            lastEventId = frame.id;
          }
          if (frame.data === undefined) {
            continue;
          }
          const event = { type: frame.event || "message", id: lastEventId, data: parseSSEData(frame.data) };
          yield event;
          if (event.type === "closed") {
            return;
          }
        }
        // Stream ended without a "closed" event: reconnect with the cursor.
      } catch (err) {
        if (signal?.aborted) {
          return;
        }
        if (err instanceof DriverApiError && !err.retryable) {
          throw err;
        }
        // Retryable API errors and network/stream failures fall through to
        // the reconnect path below.
      } finally {
        signal?.removeEventListener("abort", onAbort);
        controller.abort();
      }
      await watchDelay(retryMs, signal);
    }
  }

  async listAgents(_input = {}) {
    return this.#httpCall("list-agents", {});
  }

  async agentOrchestrationSession(input = {}) {
    const agent = agentNameOf(input);
    if (!agent) {
      throw new Error("agents.orchestrationSession requires agent");
    }
    return this.#httpCall("agent-orchestration-session", { agent: String(agent) });
  }

  async updateAgentParent(input = {}) {
    const agent = agentNameOf(input);
    const parent = input.parent || input.parentEpicId || "";
    if (!agent || !parent) {
      throw new Error("agents.updateParent requires agent and parent");
    }
    const params = {
      agent: String(agent),
      parent: String(parent),
      expectParent: input.expectParent || "",
    };
    return this.#httpCall("update-agent-parent", params);
  }

  async deliverLeadAssignment(input = {}) {
    const agent = agentNameOf(input);
    if (!agent) {
      throw new Error("agents.deliverAssignment requires agent");
    }
    return this.#httpCall("deliver-lead-assignment", { agent: String(agent) });
  }

  async messageAgent(input = {}) {
    const agent = agentNameOf(input);
    const message = input.message || input.text || input.body || "";
    if (!agent || !message) {
      throw new Error("agents.message requires agent and message");
    }
    return this.#httpCall("deliver-agent-message", { agent: String(agent), message: String(message) });
  }

  async requestTaskRun(input = {}) {
    const taskId = input.taskId;
    if (!taskId) {
      throw new Error("taskRuns.request requires taskId");
    }
    const params = {
      taskId: String(taskId),
      runner: input.runner || "",
      taskRunId: input.taskRunId || "",
      workerProfileId: input.workerProfileId || "",
      parentSessionId: input.parentSessionId || "",
      nodeId: input.nodeId || "",
      runnerId: input.runnerId || "",
      driverStepId: input.driverStepId || "",
      capabilities: stringList(input.capabilities),
      deferCompletion: true,
    };
    const repoRef = input.repoRef || input.repo_ref || (input.sandboxPlacement && (input.sandboxPlacement.repoRef || input.sandboxPlacement.repo_ref));
    if (repoRef) {
      params.repoRef = String(repoRef);
      params.sandboxPlacement = { repoRef: String(repoRef) };
    }
    // input.input is the optional task-run payload (e.g. a review diff+rubric):
    // it is persisted on the run and delivered verbatim to the runner. Sent
    // only when present, and via rawKeys so compactParams never rewrites the
    // caller's nested data.
    if (input.input !== undefined && input.input !== null) {
      params.input = input.input;
    }
    // closeTask (optional) overrides whether the serve task worker closes the
    // task issue on success. Omitted => worker default (true); a planner run
    // passes false so the card stays in design+review. Sent only when the caller
    // set it, and via rawKeys so a boolean false is never dropped by compaction.
    if (input.closeTask !== undefined && input.closeTask !== null) {
      params.closeTask = booleanInput(input.closeTask);
    }
    // Review hosts may need the exact DriverRun Work Item claim to survive a
    // successful child long enough to perform connector egress and the final
    // lifecycle handoff. The server converts this opt-in into trusted TaskRun
    // metadata; arbitrary runtime metadata is never accepted from the SDK.
    if (input.retainWorkItemClaim === true) {
      params.retainWorkItemClaim = true;
    }
    let response;
    for (let attempt = 1; attempt <= TASK_RUN_REQUEST_MAX_ATTEMPTS; attempt += 1) {
      try {
        response = await this.#httpCall("exec-task", { ...params, enqueueOnly: true }, { rawKeys: ["input", "closeTask"] });
        break;
      } catch (err) {
        // A timeout, disconnect, or invalid 2xx body may happen after Fleet
        // committed the immutable TaskRun request receipt. Replay the exact
        // same command once before exposing the error; callers must never
        // interpret an ambiguous transport failure as proof of no commit.
        if (!taskRunRequestMayHaveCommitted(err) || attempt === TASK_RUN_REQUEST_MAX_ATTEMPTS) {
          throw err;
        }
      }
    }
    const result = sanitizeTaskRunResult(response);
    rememberTaskRunResult(this, result || {});
    return result;
  }

  async getTaskRun(input = {}) {
    const taskRunId = input.taskRunId || "";
    if (!taskRunId) {
      throw new Error("taskRuns.get requires taskRunId");
    }
    return sanitizeTaskRunResult(await this.#httpCall("task-run-get", { taskRunId: String(taskRunId) }));
  }

  async awaitTaskRun(input = {}) {
    const taskRunId = input.taskRunId || "";
    if (!taskRunId) {
      throw new Error("taskRuns.await requires taskRunId");
    }
    const pollMs = Math.max(100, Number(input.pollMs || 2000));
    const timeoutMs = Math.max(0, Number(input.timeoutMs || 0));
    const started = Date.now();
    while (true) {
      const result = await this.getTaskRun({ taskRunId });
      if (isTerminalTaskRunStatus(result?.status)) {
        rememberTaskRunResult(this, result || {});
        return result;
      }
      if (timeoutMs > 0 && Date.now() - started >= timeoutMs) {
        throw new DriverApiError(`task run ${taskRunId} did not finish within ${timeoutMs}ms`, { code: "timeout", retryable: true });
      }
      await sleep(Math.min(pollMs, timeoutMs > 0 ? Math.max(1, timeoutMs - (Date.now() - started)) : pollMs));
    }
  }

  async activeTaskRuns(input = {}) {
    return this.#httpCall("active-task-runs", {
      epicId: this.#epicID(input),
      limit: input.limit || "",
    });
  }

  async recoverStaleTaskRuns(input = {}) {
    return this.#httpCall("recover-stale-tasks", {
      staleBefore: input.staleBefore || "",
      maxAgeSeconds: input.maxAgeSeconds || "",
      errorClass: input.errorClass || "",
      errorMessage: input.errorMessage || "",
    });
  }

  async completeTask(input = {}) {
    const taskId = taskPayloadID(input);
    const requestedTaskRunId = input.taskRunId || "";
    const remembered = requestedTaskRunId
      ? this.taskRunResultsByRunId.get(String(requestedTaskRunId))
      : this.taskRunResultsByTaskId.get(String(taskId));
    const taskRunId = requestedTaskRunId || remembered?.taskRunId || remembered?.id || "";
    if (!taskId && !taskRunId) {
      throw new Error("tasks.complete requires taskId or taskRunId");
    }
    const params = {
      taskId: taskId || "",
      taskRunId: taskRunId || "",
      reason: input.reason || "",
      completionId: input.completionId || "",
      leaseToken:
        pickEnv(this.env, "LOOM_TASK_RUN_LEASE_TOKEN") || pickEnv(this.env, "LOOM_RUNNER_LEASE_TOKEN"),
      logsRef: input.logsRef || remembered?.logsRef || "",
      artifactsRef: input.artifactsRef || remembered?.artifactsRef || "",
      artifactIds: stringList(input.artifactIds || remembered?.artifactIds),
    };
    return this.#httpCall("complete-task", params);
  }

  async releaseTask(input = {}) {
    const taskId = taskPayloadID(input);
    if (!taskId) {
      throw new Error("tasks.release requires taskId");
    }
    const params = { taskId: String(taskId), actor: input.actor || "" };
    return this.#httpCall("release-task", params);
  }

  async releaseReview(input = {}) {
    const taskId = taskPayloadID(input);
    if (!taskId) {
      throw new Error("tasks.releaseReview requires taskId");
    }
    return this.#httpCall("release-review", { taskId: String(taskId) });
  }

  async handoffReview(input = {}) {
    const taskId = taskPayloadID(input);
    const taskRunId = input && typeof input === "object" ? String(input.taskRunId || "") : "";
    const status = input && typeof input === "object" ? String(input.status || "") : "";
    if (!taskId) {
      throw new Error("tasks.handoffReview requires taskId");
    }
    if (!taskRunId) {
      throw new Error("tasks.handoffReview requires taskRunId");
    }
    if (status !== "open" && status !== "review" && status !== "closed") {
      throw new Error('tasks.handoffReview status must be "open", "review", or "closed"');
    }
    const hasPriority = Object.prototype.hasOwnProperty.call(input, "priority");
    const hasLabels = Object.prototype.hasOwnProperty.call(input, "labels");
    const hasCommentBody = Object.prototype.hasOwnProperty.call(input, "commentBody");
    const hasExternalRef = Object.prototype.hasOwnProperty.call(input, "externalRef");
    if (status !== "review" && (
      hasPriority || hasLabels || hasCommentBody || hasExternalRef
    )) {
      throw new Error(
        "tasks.handoffReview priority, labels, commentBody, and externalRef are only valid for review status",
      );
    }
    if (status === "review") {
      if (!Number.isInteger(input.priority) || input.priority < 0 || input.priority > 4) {
        throw new Error("tasks.handoffReview review status requires priority as an integer from 0 through 4");
      }
      if (typeof input.commentBody !== "string" || input.commentBody.trim() === "") {
        throw new Error("tasks.handoffReview review status requires nonblank commentBody");
      }
      if (hasLabels && (!Array.isArray(input.labels) || input.labels.some((label) => typeof label !== "string"))) {
        throw new Error("tasks.handoffReview labels must be an array of strings");
      }
      if (
        hasExternalRef &&
        (
          typeof input.externalRef !== "string" ||
          !isCanonicalLocalBranchExternalRef(input.externalRef)
        )
      ) {
        throw new Error("tasks.handoffReview externalRef must be a canonical local-branch reference");
      }
    }
    const params = {
      taskId: String(taskId),
      taskRunId,
      status,
      reason: String(input.reason || ""),
    };
    if (status === "review") {
      params.priority = input.priority;
      params.commentBody = input.commentBody;
      if (hasLabels) {
        params.labels = input.labels;
      }
      if (hasExternalRef) {
        params.externalRef = input.externalRef;
      }
    }
    return this.#httpCall("handoff-review", params, { rawKeys: ["labels"] });
  }

  // dispatchConnector posts one connector egress call to the run-scoped
  // "connector-dispatch" driver op. Actions registered as precondition-gated
  // (irreversible server-side, or provider-enforced like github.review.post)
  // are refused with a SYNCHRONOUS throw when the required freshness field is
  // missing — before any callSeq is consumed and before any network I/O — so
  // a workflow bug cannot reach egress, and the server enforces the same
  // registry as defense in depth. Server refusals (grant_denied,
  // precondition_required, stale_subject, rate_limited, upstream_error)
  // surface as DriverApiError with that code; stale_subject is never
  // auto-retried — the workflow decides (typically ending skipped).
  dispatchConnector(input = {}) {
    const action = String(input.action || "").trim();
    if (!action) {
      throw new Error("connectors.dispatch requires action");
    }
    const preconditions = { ...(input.preconditions || {}) };
    const missing = (CONNECTOR_REQUIRED_PRECONDITIONS[action] || [])
      .filter((field) => String(preconditions[field] ?? "").trim() === "");
    if (missing.length > 0) {
      throw new DriverApiError(
        `connector action ${action} requires ${missing.map((f) => "preconditions." + f).join(", ")}: `
          + "pass the value observed when the run decided to act (refused client-side, no request was sent)",
        { code: "precondition_required", retryable: false },
      );
    }
    const callSeq = input.callSeq === undefined || input.callSeq === null || input.callSeq === ""
      ? this.#nextConnectorCallSeq(action)
      : Number(input.callSeq);
    return this.#httpCall("connector-dispatch", {
      connectorId: input.connectorId || "",
      action,
      resource: input.resource || "",
      args: input.args || {},
      preconditions,
      callSeq,
    });
  }

  // #nextConnectorCallSeq auto-increments the run-local sequence per action:
  // the server derives the call/idempotency id from (runId, action, callSeq),
  // so a re-entered driver issuing the same calls in the same order produces
  // the same idempotency keys.
  #nextConnectorCallSeq(action) {
    const next = (this.connectorCallSeqs.get(action) || 0) + 1;
    this.connectorCallSeqs.set(action, next);
    return next;
  }

  // awaitEvent (loom.events.await) registers the run's next await on an
  // EXACT rendered subject key (no glob) and either returns the recorded
  // event inline — {status:"satisfied"|"timed_out", instanceKey, pattern,
  // deadline, event:{id,payload,actor,occurredAt}} — or throws the
  // WorkflowSuspended sentinel once the server suspended the run. A timed-out
  // await returns normally with the synthetic timeout event: branch on
  // status, do not expect a throw.
  //
  // DETERMINISM (RULE 3): awaitIndex derives from CALL ORDER via a
  // per-process monotonic counter. Resume re-runs the workflow from the
  // top, so the nth events.await/workflows.await call always maps to
  // runId#await-{n} and replays its recorded event (multi-turn loops
  // fast-forward through already-consumed turns this way). Awaits must
  // NEVER be conditionally skipped or reordered across re-entries — the
  // same determinism rule as deterministic task run ids and connector
  // callSeq.
  //
  // FRESHNESS (vet A2): state observed before a satisfied await may be
  // arbitrarily stale by the time it returns (a suspend can last days).
  // Re-run non-memoized freshness checks after every await before acting:
  //   const { event } = await loom.events.await({
  //     pattern: "approval:octo/hello#123@" + headSha,
  //     actor: eligibleApprovers, timeoutMs: 7 * 24 * 3600e3,
  //   });
  //   const pr = await loom.connectors.github.readPullRequest({ ... });
  //   if (pr.body.head.sha !== headSha) return loom.failed({ summary: "stale" });
  // Slack-thread flow: pattern "slack.thread_reply:C123/1718012345.0001",
  // actor: requester.
  awaitEvent(input = {}) {
    const pattern = String(input.pattern || "").trim();
    if (!pattern) {
      throw new Error('events.await requires pattern (the fully rendered subject key, e.g. "approval:owner/repo#123@sha")');
    }
    // Validation throws SYNCHRONOUSLY (before the index is consumed and
    // before any network I/O), matching the connector refusal convention.
    const timeoutMs = requiredAwaitTimeoutMs(input, "events.await");
    const params = {
      pattern,
      actor: stringList(input.actor),
      timeoutMs,
      awaitIndex: this.#nextAwaitIndex(input),
    };
    return this.#postAwait("events/await", params);
  }

  // listAwaits (loom.events.list) returns {runId, awaits:[...]} — terminal
  // awaits with their recorded events plus pending rows, in index order —
  // for rebuilding context on re-entry without consuming await slots.
  async listAwaits(_input = {}) {
    return this.#httpCall("events/awaits", undefined, { method: "GET" });
  }

  // startWorkflow (loom.workflows.start) creates the deterministic child
  // run: identity is keyed by idempotencyKey when given, else by a
  // per-process 1-based start counter (the same call-order determinism as
  // awaitIndex), so a re-entered parent re-issuing the same start gets the
  // same childRunId back, never a duplicate. The child does NOT inherit the
  // parent's epicId. Returns {childRunId, workflowName, status, parentRunId}.
  startWorkflow(input = {}) {
    const workflowName = String(input.workflow || input.workflowName || "").trim();
    if (!workflowName) {
      throw new Error("workflows.start requires workflow");
    }
    const params = { workflowName, idempotencyKey: String(input.idempotencyKey || "").trim() };
    if (!params.idempotencyKey) {
      params.startIndex = this.#nextWorkflowStartIndex(input);
    }
    if (input.input !== undefined && input.input !== null) {
      params.input = input.input;
    }
    // rawKeys: the child's input payload crosses the wire verbatim — the
    // usual empty-value compaction must not rewrite user data.
    return this.#httpCall("workflows/start", params, { rawKeys: ["input"] });
  }

  // awaitChildWorkflow (loom.workflows.await) awaits the child's
  // run.finished event through the normal await machinery — it consumes the
  // SAME awaitIndex counter as events.await (RULE 3 applies). Satisfied and
  // timed_out responses additionally carry child:{runId,status,summary,
  // errorClass} (the fresher read; event.payload holds the journaled
  // run-finished payload). Suspension throws WorkflowSuspended like
  // events.await.
  awaitChildWorkflow(input = {}) {
    const childRunId = String(input.childRunId || input.runId || "").trim();
    if (!childRunId) {
      throw new Error("workflows.await requires childRunId");
    }
    const timeoutMs = requiredAwaitTimeoutMs(input, "workflows.await");
    const params = {
      childRunId,
      timeoutMs,
      awaitIndex: this.#nextAwaitIndex(input),
    };
    return this.#postAwait("workflows/await", params);
  }

  // #postAwait posts one await op and converts a suspended response into the
  // WorkflowSuspended sentinel.
  async #postAwait(op, params) {
    return throwIfSuspended(await this.#httpCall(op, params), params.awaitIndex);
  }

  // #nextAwaitIndex consumes the run-local await ordinal (1-based, call
  // order). An explicit awaitIndex overrides without advancing the counter,
  // mirroring connector callSeq.
  #nextAwaitIndex(input) {
    if (input.awaitIndex !== undefined && input.awaitIndex !== null && input.awaitIndex !== "") {
      return Number(input.awaitIndex);
    }
    this.awaitSeq += 1;
    return this.awaitSeq;
  }

  #nextWorkflowStartIndex(input) {
    if (input.startIndex !== undefined && input.startIndex !== null && input.startIndex !== "") {
      return Number(input.startIndex);
    }
    this.workflowStartSeq += 1;
    return this.workflowStartSeq;
  }

  #epicID(input) {
    return input.epicId || this.input.epicId || "";
  }

  #requireHttpConfig() {
	if (!this.runToken) {
	  throw new Error("LOOM_RUN_TOKEN is required for the driver HTTP API");
    }
    if (!this.workspace) {
      throw new Error("LOOM_DRIVER_WORKSPACE is required for the driver HTTP API");
    }
    if (!this.apiUrl) {
      throw new Error("LOOM_DRIVER_API_URL is required for the driver HTTP API");
    }
  }

	// #identityHeaders builds the sole run-token credential for JSON and SSE.
	#identityHeaders() {
	  return { Authorization: "Bearer " + this.runToken };
  }

  async #httpCall(op, params, options = {}) {
    this.#requireHttpConfig();
    const url = `${this.apiUrl}/api/workspaces/${encodeURIComponent(this.workspace)}/driver/${op}`;
    const method = options.method || "POST";
    const headers = this.#identityHeaders();
    let requestBody;
    if (method !== "GET") {
      headers["Content-Type"] = "application/json";
      const compacted = compactParams(params);
      // rawKeys bypass compaction for keys whose values are caller data
      // (e.g. a child workflow's input payload) rather than wire params.
      for (const key of options.rawKeys || []) {
        if (params && params[key] !== undefined && params[key] !== null) {
          compacted[key] = params[key];
        }
      }
      requestBody = JSON.stringify(compacted);
    }
    const timeoutMs = options.timeoutMs === undefined ? DEFAULT_HTTP_TIMEOUT_MS : options.timeoutMs;
    const controller = timeoutMs > 0 ? new AbortController() : null;
    const timer = controller
      ? setTimeout(() => controller.abort(new DriverApiError(`driver op ${op} timed out after ${timeoutMs}ms`, { code: "timeout", retryable: true })), timeoutMs)
      : null;
    let response;
    let text;
    try {
      response = await fetch(url, {
        method,
        headers,
        body: requestBody,
        signal: controller ? controller.signal : undefined,
      });
      text = await response.text();
    } catch (err) {
      if (err instanceof DriverApiError) {
        throw err;
      }
      if (controller && controller.signal.aborted && controller.signal.reason instanceof DriverApiError) {
        throw controller.signal.reason;
      }
      throw new DriverApiError(`driver op ${op} request failed: ${err?.message || err}`, { code: "unavailable", retryable: true });
    } finally {
      if (timer) {
        clearTimeout(timer);
      }
    }
    let parsed = null;
    if (text && text.trim() !== "") {
      try {
        parsed = JSON.parse(text);
      } catch (err) {
        if (response.ok) {
          throw new DriverApiError(`driver op ${op} returned invalid JSON: ${err.message}`, { code: "internal", status: response.status });
        }
      }
    }
    if (!response.ok) {
      const envelope = parsed && typeof parsed.error === "object" && parsed.error !== null ? parsed.error : null;
      const message = envelope?.message
        || (typeof parsed?.error === "string" ? parsed.error : "")
        || `driver op ${op} failed with HTTP ${response.status}`;
      throw new DriverApiError(message, {
        code: envelope?.code,
        retryable: envelopeRetryable(envelope),
        status: response.status,
        details: envelope?.details,
      });
    }
    return parsed;
  }
}

export function createLoomDriverClient(options = {}) {
  if (options && !("input" in options) && !("env" in options) && !("apiUrl" in options) && !("runToken" in options)) {
    return LoomDriverClient.fromEnv({ input: options });
  }
  return LoomDriverClient.fromEnv(options);
}

export const createLoomClient = createLoomDriverClient;

// CONNECTOR_OPS maps the workflow-facing loom.connectors.{source}.{method}
// surface onto the dotted connector action names the dispatch layer
// implements. Methods are thin: friendly input fields become camelCase
// provider args, the four expected* freshness fields become preconditions,
// and everything rides the single "connector-dispatch" driver op.
const CONNECTOR_OPS = {
  github: {
    merge: "github.merge",
    postReview: "github.review.post",
    readPullRequest: "github.pull_request.read",
    listPulls: "github.pulls.list",
    compare: "github.compare.read",
    postIssueComment: "github.issue_comment.post",
  },
  slack: {
    post: "slack.chat.post",
    readConversations: "slack.conversations.read",
  },
  datadog: {
    readMonitors: "datadog.monitors.read",
    readAlert: "datadog.alert.read",
    declareIncident: "datadog.incidents.write",
  },
};

// CONNECTOR_REQUIRED_PRECONDITIONS mirrors the server-side irreversible
// registry (internal/connector/grants.go irreversiblePreconditions) plus
// provider-enforced precondition gates (github.review.post demands the
// expected head sha for its pre-egress liveness read). The client refuses
// these BEFORE any network call; the server enforces the same rules, so this
// is defense in depth, not the authority.
const CONNECTOR_REQUIRED_PRECONDITIONS = {
  "github.merge": ["expectedHeadSha"],
  "github.review.post": ["expectedHeadSha"],
  "github.branch.delete": ["expectedHeadSha"],
  "github.pull_request.close": ["expectedHeadSha"],
  "issues.set_priority": ["expectedIssueRevision"],
  "slack.chat.delete": ["expectedMessageTs"],
  "datadog.monitor.delete": ["expectedMonitorRevision"],
  "datadog.monitor.mute": ["expectedMonitorRevision"],
};

// connectorPreconditionFields are the camelCase wire fields recognized inside
// "preconditions"; they may be passed flat on a connectors.* input and are
// routed here instead of into args.
const connectorPreconditionFields = new Set([
  "expectedHeadSha",
  "expectedIssueRevision",
  "expectedMessageTs",
  "expectedMonitorRevision",
]);

// connectorReservedFields are connectors.* input keys that address the
// dispatch envelope itself rather than the provider call.
const connectorReservedFields = new Set(["action", "connectorId", "resource", "callSeq", "args", "preconditions"]);

function buildConnectorsNamespace(client) {
  const namespace = {
    dispatch: (input = {}) => client.dispatchConnector(input),
  };
  for (const [source, ops] of Object.entries(CONNECTOR_OPS)) {
    const surface = {};
    for (const [method, action] of Object.entries(ops)) {
      surface[method] = (input = {}) => client.dispatchConnector({ ...splitConnectorInput(input), action });
    }
    namespace[source] = Object.freeze(surface);
  }
  return Object.freeze(namespace);
}

// splitConnectorInput routes a flat connectors.* input into the dispatch
// envelope: reserved keys address the envelope, expected* freshness keys
// become preconditions, every other key is a provider arg. Explicit args /
// preconditions objects win over flat fields of the same name.
function splitConnectorInput(input) {
  const args = {};
  const preconditions = {};
  for (const [key, value] of Object.entries(input || {})) {
    if (connectorReservedFields.has(key)) {
      continue;
    }
    if (connectorPreconditionFields.has(key)) {
      preconditions[key] = value;
      continue;
    }
    args[key] = value;
  }
  return {
    connectorId: input.connectorId || "",
    resource: input.resource || "",
    callSeq: input.callSeq,
    args: { ...args, ...(input.args || {}) },
    preconditions: { ...preconditions, ...(input.preconditions || {}) },
  };
}

// compactParams strips empty values so the wire payload only carries what the
// caller actually set; nested objects are compacted recursively and dropped
// when empty.
// booleanInput coerces an optional boolean param that may arrive as a real
// boolean (workflow dispatch) or a string (workflow-run --input): "false"/0/no/
// off read false, everything else truthy reads by JS truthiness.
function booleanInput(v) {
  if (typeof v === "boolean") return v;
  if (typeof v === "string") {
    return !["", "0", "false", "no", "off"].includes(v.trim().toLowerCase());
  }
  return Boolean(v);
}

function compactParams(params) {
  const out = {};
  for (const [key, value] of Object.entries(params || {})) {
    if (value === undefined || value === null || value === "") {
      continue;
    }
    if (Array.isArray(value)) {
      if (value.length > 0) {
        out[key] = value;
      }
      continue;
    }
    if (typeof value === "object") {
      const nested = compactParams(value);
      if (Object.keys(nested).length > 0) {
        out[key] = nested;
      }
      continue;
    }
    out[key] = value;
  }
  return out;
}

// requiredAwaitTimeoutMs surfaces RULE 5 at the API the developer touches:
// every await must carry a positive bounded timeout (milliseconds; the
// server additionally caps it). Thrown SYNCHRONOUSLY — before any
// awaitIndex is consumed and before any network I/O — matching the
// connector precondition refusal convention.
function requiredAwaitTimeoutMs(input, op) {
  const raw = input.timeoutMs ?? input.timeout;
  const ms = Number(raw);
  if (raw === undefined || raw === null || raw === "" || !Number.isFinite(ms) || ms <= 0) {
    throw new DriverApiError(
      op + " requires a positive timeoutMs (RULE 5: every await carries a bounded timeout; on expiry the run resumes with a timeout event)",
      { code: "await_timeout_required", retryable: false },
    );
  }
  return Math.floor(ms);
}

// throwIfSuspended converts the server's {status:"suspended"} await response
// into the WorkflowSuspended sentinel; satisfied / timed_out responses pass
// through with the recorded event inline.
function throwIfSuspended(response, awaitIndex) {
  if (response && response.status === "suspended") {
    throw new WorkflowSuspended(awaitIndex);
  }
  return response;
}

function stringList(values) {
  const list = Array.isArray(values) ? values : values ? [values] : [];
  return list.map(String).filter((value) => value.trim() !== "");
}

function stripTrailingSlash(value) {
  return value.replace(/\/+$/, "");
}

function isTerminalTaskRunStatus(status) {
  switch (String(status || "")) {
    case "completed":
    case "failed":
    case "cancelled":
    case "needs_review":
      return true;
    default:
      return false;
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// envelopeRetryable maps a structured error envelope's retryable flag,
// forcing token_expired (HTTP 401) to NON-retryable regardless of what the
// envelope says: the run token's TTL is the hard max-run-duration cap, so a
// retry with the same token can never succeed — the run must end (a watch
// loop in particular must not reconnect forever on an expired token).
function envelopeRetryable(envelope) {
  if (envelope?.code === "token_expired") {
    return false;
  }
  return Boolean(envelope?.retryable);
}

function taskRunRequestMayHaveCommitted(err) {
  if (!(err instanceof DriverApiError)) {
    return false;
  }
  switch (String(err.code || "")) {
    case "timeout":
    case "unavailable":
    case "internal":
      return true;
    default:
      return false;
  }
}

// watchHttpError maps a non-OK watch response onto DriverApiError using the
// structured {code, message, retryable} envelope when present. Without an
// envelope, 5xx/429 default to retryable so transient proxy errors reconnect.
async function watchHttpError(response) {
  let envelope = null;
  try {
    const parsed = JSON.parse(await response.text());
    if (parsed && typeof parsed.error === "object" && parsed.error !== null) {
      envelope = parsed.error;
    }
  } catch {
    // Non-JSON error bodies fall back to the status-based defaults.
  }
  const retryable = envelope
    ? envelopeRetryable(envelope)
    : response.status >= 500 || response.status === 429;
  return new DriverApiError(envelope?.message || `epic watch failed with HTTP ${response.status}`, {
    code: envelope?.code,
    retryable,
    status: response.status,
    details: envelope?.details,
  });
}

// sseFrames is a minimal SSE parser over a fetch body stream: frames are
// separated by blank lines; "event:", "id:", "data:" and "retry:" fields are
// recognized; comment lines (leading ":") are ignored. A trailing partial
// frame at stream end is dropped, per the SSE spec.
async function* sseFrames(body) {
  const decoder = new TextDecoder();
  let buffer = "";
  for await (const chunk of body) {
    buffer += decoder.decode(chunk, { stream: true });
    let boundary;
    while ((boundary = buffer.indexOf("\n\n")) !== -1) {
      const raw = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const frame = parseSSEFrame(raw);
      if (frame) {
        yield frame;
      }
    }
  }
}

function parseSSEFrame(raw) {
  const frame = { event: "", id: undefined, data: undefined, retryMs: undefined };
  let sawField = false;
  for (const rawLine of raw.split("\n")) {
    const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;
    if (line === "" || line.startsWith(":")) {
      continue;
    }
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) {
      value = value.slice(1);
    }
    switch (field) {
      case "event":
        frame.event = value;
        sawField = true;
        break;
      case "id":
        frame.id = value;
        sawField = true;
        break;
      case "data":
        frame.data = frame.data === undefined ? value : frame.data + "\n" + value;
        sawField = true;
        break;
      case "retry": {
        const ms = Number(value);
        if (Number.isFinite(ms) && ms >= 0) {
          frame.retryMs = ms;
          sawField = true;
        }
        break;
      }
      default:
        break;
    }
  }
  return sawField ? frame : null;
}

function parseSSEData(text) {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

// watchDelay sleeps for the reconnect backoff but resolves early when the
// caller's abort signal fires so iteration can end promptly.
function watchDelay(ms, signal) {
  if (signal?.aborted) {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    const timer = setTimeout(done, ms);
    function done() {
      clearTimeout(timer);
      signal?.removeEventListener("abort", done);
      resolve();
    }
    signal?.addEventListener("abort", done, { once: true });
  });
}

function rememberTaskRunResult(client, result = {}) {
	result = sanitizeTaskRunResult(result);
  const runId = result.taskRunId || result.id || "";
  const taskId = result.taskId || "";
  if (runId) {
    client.taskRunResultsByRunId.set(String(runId), result);
  }
  if (taskId) {
    client.taskRunResultsByTaskId.set(String(taskId), result);
  }
}

// TaskRun lease tokens belong only to the worker that claimed the run. Strip
// legacy server fields defensively so generic workflow results and remembered
// polling state cannot turn a watch/request response into a bearer credential.
function sanitizeTaskRunResult(result = {}) {
  if (!result || typeof result !== "object" || Array.isArray(result)) {
    return result;
  }
  const sanitized = { ...result };
  delete sanitized.leaseToken;
  delete sanitized.lease_token;
  return sanitized;
}

function isCanonicalLocalBranchExternalRef(externalRef) {
  if (
    externalRef !== externalRef.trim() ||
    !externalRef.startsWith("local-branch:")
  ) {
    return false;
  }
  const body = externalRef.slice("local-branch:".length);
  const separator = body.lastIndexOf("@");
  if (separator <= 0 || !/^[0-9a-f]{40}$/.test(body.slice(separator + 1))) {
    return false;
  }
  const branch = body.slice(0, separator);
  return branch !== "@" &&
    !branch.startsWith("-") &&
    !branch.startsWith("/") &&
    !branch.endsWith("/") &&
    !branch.startsWith(".") &&
    !branch.endsWith(".") &&
    !branch.endsWith(".lock") &&
    !branch.includes("..") &&
    !branch.includes("//") &&
    !branch.includes("@{") &&
    !/[\s\u0000-\u001f\u007f~^:?*[\]\\]/u.test(branch);
}

function pickEnv(env, key) {
  return String(env?.[key] || "").trim();
}

function agentNameOf(input) {
  return input.agent || input.agentName || input.name || "";
}

function taskPayloadID(input) {
  if (typeof input === "string") {
    return input;
  }
  return input.taskId || input.id || "";
}
