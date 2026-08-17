import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

// Flue HEAD (durable-streams) requires every workflow module to default-export
// a defineWorkflow() definition; a bare `export function run` no longer
// normalizes. This builtin is not an LLM agent — the scout orchestrates via the
// loom driver SDK and delegates the LLM work to its scout-task-runner leaf — so
// the bound agent is a credential-free stub (model: false, no harness usage)
// and the invocation payload arrives via env: the driver launcher sets
// LOOM_FLUE_INVOKE_PAYLOAD (sandbox/launcher.go).
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: async () => toJsonResult(await run({ payload: builtinInvokePayload() })),
});

function builtinInvokePayload() {
  const raw = process.env.LOOM_FLUE_INVOKE_PAYLOAD || process.env.LOOM_TASK_RUN_REQUEST_JSON || '{}';
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

// Flue HEAD validates the workflow return value with a strict JSON check that
// rejects undefined/function/symbol/bigint; round-trip through JSON so optional
// result fields left undefined never throw.
function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

// scout: proactive workspace analysis (slice 1 — the manually-triggered
// end-to-end vertical; no creation hook, no cron binding yet).
//
// One run is a single linear pass:
//
//   1. enqueue + await the ANALYZE leaf task run (scout-task-runner): the leaf
//      anchors at the workspace root, enumerates repo checkouts, runs the
//      workspace-default backend CLI agentically, and returns the normalized
//      analysis (recommendations, skipped candidates, agents.md content, repo
//      SHAs, warnings) on runtimeMetadata.scout_analysis;
//   2. create at most MAX_RECOMMENDATIONS quarantined issues through the
//      create-issue driver op (loom.issues.create). Creation happens HERE, not
//      in the leaf: the op takes run-token Bearer auth and stamps the actor
//      driver-run:{runId} from the verified parent run, and only the workflow
//      process holds LOOM_RUN_TOKEN. When the issues namespace is not present
//      in the SDK yet, the run degrades to journal-only mode — the
//      recommendations land in history.md with a warning instead of failing;
//   3. enqueue + await the WRITE leaf task run: first-generation auto-apply of
//      agents.md (scout-fenced) and one appended history.md run section that
//      records the created issue IDs — which is why the write phase runs after
//      issue creation;
//   4. completed with a summary + counts.
//
// Re-entry is deterministic: both leaf task runs use deterministic ids derived
// from the driver run id, each is requested exactly once (a conflict on
// re-entry is expected and awaited instead), and same-run create-issue retries
// dedupe on fleet-db's actor-scoped hard idempotency (same run id + same day +
// same body).
export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  const runner = stringValue(input.runner) || "scout-task-runner";
  const maxRecommendations = clampInt(input.maxRecommendations, 1, MAX_RECOMMENDATIONS, MAX_RECOMMENDATIONS);

  // This identity comes from the executor's authenticated DriverRun, never
  // from the caller-controlled invocation payload. Keep the bytes verbatim;
  // the leaf owns grammar validation before deriving instance paths.
  const agentServiceID = process.env.LOOM_AGENT_SERVICE_ID ?? "";

  const analysisRun = await runScoutTask(loom, runner, "scout-analyze", agentServiceID, {
    kind: "scout_analyze",
    phase: "analyze",
    maxRecommendations,
  });
  if (!analysisRun.ok) {
    return loom.failed({ summary: analysisRun.summary, errorClass: analysisRun.errorClass || "scout_analysis_failed" });
  }
  const analysis = parseAnalysis(analysisRun.metadata.scout_analysis);
  if (!analysis.ok) {
    return loom.failed({ summary: analysis.summary, errorClass: "invalid_scout_analysis" });
  }

  const outcome = await createRecommendedIssues(loom, analysis.value, maxRecommendations);

  const writeRun = await runScoutTask(loom, runner, "scout-write", agentServiceID, {
    kind: "scout_write",
    phase: "write",
    agentsMd: analysis.value.agentsMd || "",
    historyEntry: historyEntry(loom, analysis.value, outcome),
  });
  if (!writeRun.ok) {
    return loom.failed({
      summary: "scout journal write failed: " + writeRun.summary + createdNote(outcome),
      errorClass: writeRun.errorClass || "scout_write_failed",
    });
  }

  return loom.completed({
    summary: runSummary(analysis.value, outcome),
    issuesCreated: outcome.created.length,
    issueIds: outcome.created.map((c) => c.id),
    skippedCandidates: analysis.value.skipped.length,
    journaledOnly: outcome.journaledOnly.length,
    nothingToAnalyze: Boolean(analysis.value.nothingToAnalyze),
  });
}

// Hard cap of created recommended issues per run (spec). Skipped duplicates do
// not count against it.
const MAX_RECOMMENDATIONS = 5;

// runScoutTask enqueues one leaf task run with a deterministic id and awaits
// it, mirroring github-review-agent's runReviewTask: the request is made once,
// a conflict means a previous incarnation of this run already enqueued it, and
// the terminal run's runtimeMetadata carries the structured result.
async function runScoutTask(loom, runner, label, agentServiceID, taskInput) {
  const taskRunId = deterministicTaskRunId(loom.driverRunId, label);
  const request = {
    taskId: label,
    taskRunId,
    runner,
    input: { ...taskInput, agent_service_id: agentServiceID },
  };
  try {
    await loom.taskRuns.request(request);
  } catch (err) {
    if (!isConflictError(err)) {
      return { ok: false, errorClass: connectorErrorClass(err, "scout_task_request_failed"), summary: label + " task request failed: " + errorMessage(err) };
    }
  }
  const run = await loom.taskRuns.await({ taskRunId });
  const status = stringValue(run && run.status);
  if (status !== "completed") {
    return {
      ok: false,
      errorClass: stringValue(run && (run.errorClass || run.error_class)) || "scout_task_" + (status || "incomplete"),
      summary: label + " task " + taskRunId + " ended " + (status || "without a terminal status"),
    };
  }
  const metadata = (run && (run.runtimeMetadata || run.runtime_metadata)) || {};
  return { ok: true, taskRunId, metadata };
}

// parseAnalysis validates the leaf's scout_analysis payload. The leaf already
// normalized it; this is the workflow-side shape check so a malformed leaf
// result fails with a specific error class instead of a crash mid-create.
function parseAnalysis(raw) {
  if (typeof raw !== "string" || raw.trim() === "") {
    return { ok: false, summary: "analysis task returned no scout_analysis metadata" };
  }
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    return { ok: false, summary: "scout_analysis was not valid JSON: " + errorMessage(err) };
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return { ok: false, summary: "scout_analysis must be a JSON object" };
  }
  return {
    ok: true,
    value: {
      nothingToAnalyze: Boolean(parsed.nothingToAnalyze),
      repos: Array.isArray(parsed.repos) ? parsed.repos : [],
      recommendations: Array.isArray(parsed.recommendations) ? parsed.recommendations : [],
      skipped: Array.isArray(parsed.skipped) ? parsed.skipped : [],
      agentsMd: typeof parsed.agentsMd === "string" ? parsed.agentsMd : "",
      warnings: Array.isArray(parsed.warnings) ? parsed.warnings.map((w) => stringValue(w)).filter(Boolean) : [],
    },
  };
}

// createRecommendedIssues turns the capped recommendations into real fleet-db
// issues through the create-issue driver op. Every created issue is
// quarantined by construction: created in review status (so it lands in the
// human review queue, not the ready set) with the "recommended" label
// (canonical lowercase — fleet-db compares labels case-sensitively) plus
// repo:<name> re-asserted here so the guarantee does not rest on prompt
// adherence. Rationale and
// anchors fold into the description (the create path has no field for them).
async function createRecommendedIssues(loom, analysis, cap) {
  const created = [];
  const journaledOnly = [];
  const warnings = analysis.warnings.slice();
  const recommendations = analysis.recommendations.slice(0, cap);
  if (analysis.nothingToAnalyze || recommendations.length === 0) {
    return { created, journaledOnly, warnings };
  }
  // The issues SDK namespace ships with the create-issue op. Until the SDK in
  // this workspace carries it, degrade to journal-only mode rather than fail:
  // the recommendations are still recorded in history.md.
  const issuesApi = loom.issues && typeof loom.issues.create === "function" ? loom.issues : null;
  if (!issuesApi) {
    warnings.push("issues.create is unavailable in this SDK build; recommendations were journaled without creating issues");
    for (const rec of recommendations) {
      journaledOnly.push({ title: stringValue(rec.title), repo: stringValue(rec.repo) });
    }
    return { created, journaledOnly, warnings };
  }
  for (const rec of recommendations) {
    const title = stringValue(rec.title);
    const repo = stringValue(rec.repo);
    if (!title || !repo) {
      warnings.push("skipped a recommendation missing title or repo");
      continue;
    }
    const labels = normalizedLabels(rec.labels, repo);
    try {
      const issue = await issuesApi.create({
        title,
        description: issueDescription(rec),
        issueType: "task",
        labels,
        repo,
        priority: clampInt(rec.priority, 0, 4, 2),
        // review status parks the recommendation in the human review queue;
        // the label is the enforcement backstop if the status ever flips.
        status: "review",
      });
      created.push({ id: stringValue(issue && issue.id), title });
    } catch (err) {
      warnings.push('create-issue failed for "' + title + '": ' + errorMessage(err));
      journaledOnly.push({ title, repo });
    }
  }
  return { created, journaledOnly, warnings };
}

function normalizedLabels(rawLabels, repo) {
  const labels = [];
  for (const label of Array.isArray(rawLabels) ? rawLabels : []) {
    const value = stringValue(label);
    if (value && !labels.includes(value)) {
      labels.push(value);
    }
  }
  if (!labels.includes("recommended")) {
    labels.push("recommended");
  }
  if (!labels.includes("repo:" + repo)) {
    labels.push("repo:" + repo);
  }
  return labels;
}

// issueDescription folds rationale + anchors into the description body; the
// leaf's prompt already folds acceptance criteria in as a "## Acceptance
// Criteria" section.
function issueDescription(rec) {
  const parts = [rawString(rec.description).trim()];
  const rationale = rawString(rec.rationale).trim();
  if (rationale) {
    parts.push("## Why\n\n" + rationale);
  }
  const anchors = (Array.isArray(rec.anchors) ? rec.anchors : []).map((a) => stringValue(a)).filter(Boolean);
  if (anchors.length > 0) {
    parts.push("## Anchors\n\n" + anchors.map((a) => "- " + a).join("\n"));
  }
  return parts.filter(Boolean).join("\n\n");
}

// historyEntry assembles the journal section data for the write phase. The
// timestamp is journal metadata, not an identity: a re-entered run may stamp a
// fresh one (the journal is memory, not truth — fleet-db is the durable
// record).
function historyEntry(loom, analysis, outcome) {
  return {
    timestamp: new Date().toISOString(),
    driverRunId: stringValue(loom.driverRunId),
    nothingToAnalyze: Boolean(analysis.nothingToAnalyze),
    repos: analysis.repos,
    created: outcome.created,
    journaledOnly: outcome.journaledOnly,
    skipped: analysis.skipped,
    warnings: outcome.warnings,
  };
}

function runSummary(analysis, outcome) {
  if (analysis.nothingToAnalyze) {
    return "Scout run: nothing to analyze (no repos attached); journaled.";
  }
  const bits = [
    "Scout run: " + outcome.created.length + " recommended issue(s) created",
    analysis.skipped.length + " candidate(s) skipped",
  ];
  if (outcome.journaledOnly.length > 0) {
    bits.push(outcome.journaledOnly.length + " journaled without issue creation");
  }
  if (outcome.warnings.length > 0) {
    bits.push(outcome.warnings.length + " warning(s)");
  }
  return bits.join(", ") + ".";
}

function createdNote(outcome) {
  if (outcome.created.length === 0) {
    return "";
  }
  return " (issues already created: " + outcome.created.map((c) => c.id).join(", ") + ")";
}

function deterministicTaskRunId(driverRunId, label) {
  return "task-run-" + slug(driverRunId || "run") + "-" + slug(label || "task");
}

function isConflictError(err) {
  switch (stringValue(err && err.code)) {
    case "conflict":
    case "already_exists":
    case "invalid_transition":
      return true;
    default:
      return false;
  }
}

function connectorErrorClass(err, fallback) {
  return stringValue(err && err.code) || fallback;
}

function slug(value) {
  return stringValue(value).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "item";
}

function clampInt(value, min, max, fallback) {
  const n = Number(value);
  if (!Number.isInteger(n)) {
    return fallback;
  }
  return Math.min(max, Math.max(min, n));
}

function stringValue(value) {
  return value === undefined || value === null ? "" : String(value).trim();
}

function rawString(value) {
  return value === undefined || value === null ? "" : String(value);
}

function errorMessage(err) {
  return err && err.message ? err.message : String(err);
}
