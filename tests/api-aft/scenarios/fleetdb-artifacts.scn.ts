// Scenario: fleet-db's *durable side-effect* control plane -- artifacts, connectors
// and their egress audit journal, leases, the action ledger, and the await registry.
//
// Why they matter more than their obscurity suggests: every row here is a durability
// or safety record written once, in production, by a machine. Nobody clicks these, so
// the feedback loop that keeps an issue endpoint honest -- a human noticing a wrong
// number on a screen -- never runs, and a dropped field survives indefinitely.
//
//   * artifacts     are the only record that a run produced output; the declared ->
//                   uploading -> finalized ladder makes the work reviewable later.
//   * connectors    hold sealed credentials behind a deny-by-default grant model, and
//                   connector-audit is the ONLY place a DENIED egress is written down.
//   * leases        are the mutual exclusion primitive: a lease that reports active
//                   when it is not is how two runners write the same object.
//   * action ledger is the idempotency spine for externally-visible actions.
//   * awaits        suspend a workflow on an external event; only the non-blocking
//                   legs are driven here (see the note above that section).
//
// No assertions here, by design (check-no-asserts.mjs). This file moves state
// and calls w.checkpoint(); the invariant registry auto-dispatches at each one.

import type { World } from "../src/world.ts";
import { unwrap } from "../src/world.ts";
import { call } from "../src/wire.ts";

export const name = "fleet-db artifacts, connectors, leases, action ledger, awaits";

// Stable ids: each run gets a fresh config dir and embedded miniredis (src/stack.ts),
// so readable ids beat random ones in a transcript diff.
const DRIVER = "drv-apiaft-artifacts";
const VERSION = "drvver-apiaft-1";
const RUN = "run-apiaft-artifacts-1";
const BINDING = "bind-apiaft-artifacts-1";
const ARTIFACT = "artifact-apiaft-1";
const CONNECTOR = "conn-apiaft-1";
const GRANT = "grant-apiaft-1";
const LEASE = "lease-apiaft-1";
const ACTION = "action-apiaft-1";

// Two lease legs authenticate with a header, not a body: heartbeat and release both
// 400 with "X-Lease-Token required" before reading anything (control_plane.go:844,856).
// World.fleet() has no header parameter, so they go through the recorder directly --
// same transcript, same conformance lane, just a header World does not model yet.
function fleetWithToken(
  w: World,
  method: string,
  path: string,
  token: string,
  body?: unknown,
): Promise<{ status: number; body: unknown }> {
  return call(w.rec, {
    service: "fleetdb",
    baseUrl: w.stack.fleetUrl,
    method,
    path,
    headers: { "X-Lease-Token": token },
    body,
  });
}

export async function run(w: World): Promise<void> {
  const ws = w.workspace;

  // fleet-db's WorkspaceValidator 404s every {workspace} route until the workspace
  // row exists (internal/api/workspace_middleware.go:26), and this pack must stand up
  // alone when the runner is filtered to one file. Creating it through loom is the
  // user path; a second pack creating it too is a harmless 409/200.
  await w.loom("POST", "/api/workspaces", { name: ws, type: "empty" });
  await w.checkpoint("workspace-ready");

  // --- anchor state -------------------------------------------------------------
  //
  // connector-audit rows and awaits are both keyed by a RUN, and the audit LIST leg
  // hard-400s without run_id or binding_id (connector.go:265). A fabricated run id
  // would make both endpoints answer 200-with-nothing and score as covered, so the run
  // is created for real. CreateDriverRun re-reads the driver version and rejects a
  // mismatch (storage/platform.go:982), forcing the chain driver -> version -> run.
  await w.fleet("POST", `/api/v1/${ws}/drivers`, {
    driver_id: DRIVER,
    name: "api-aft artifact/connector pack driver",
    description: "anchor for artifact, connector-audit and await rows",
  });
  await w.fleet("POST", `/api/v1/${ws}/drivers/${DRIVER}/versions`, {
    version_id: VERSION,
    version: 1,
    source_digest: "sha256:apiaft-source-digest",
    bundle_digest: "sha256:apiaft-bundle-digest",
    runtime: "node",
  });
  const runRes = await w.fleet("POST", `/api/v1/${ws}/driver-runs`, {
    run_id: RUN,
    driver_id: DRIVER,
    driver_version_id: VERSION,
    entrypoint: "main",
  });
  const driverRun = unwrap(runRes.body, "driver_run") as { run_id?: string } | null;

  // A grant and an audit row are both keyed by a binding id that fleet-db stores as a
  // free-form reference -- CreateConnectorGrant checks the CONNECTOR exists but never
  // the binding (internal/storage/connector.go:233). Creating the binding anyway
  // keeps the reference honest rather than dangling.
  await w.fleet("POST", `/api/v1/${ws}/trigger-bindings`, {
    binding_id: BINDING,
    name: "api-aft connector binding",
    source_kind: "internal",
    driver_id: DRIVER,
    driver_version_id: VERSION,
  });

  if (driverRun?.run_id) {
    w.bind("drivers.driver_id", DRIVER);
    w.bind("driver-versions.version_id", VERSION);
    w.bind("driver-runs.run_id", driverRun.run_id);
    w.bind("trigger-bindings.binding_id", BINDING);
  }
  await w.checkpoint("driver-run-anchored");

  // --- artifacts ----------------------------------------------------------------
  //
  // owner_type is sent explicitly as driver_run because Normalize() derives an
  // owner_id for task_run/session/agent_service but deliberately leaves it EMPTY for
  // driver_run (internal/models/control_plane.go:373), and Validate() then rejects an
  // owner_type with no owner_id -- the one owner type a client must pair by hand.
  const artRes = await w.fleet("POST", `/api/v1/${ws}/artifacts`, {
    artifact_id: ARTIFACT,
    type: "log",
    owner_type: "driver_run",
    owner_id: RUN,
    summary: "api-aft synthetic run log",
    mime_type: "text/plain",
    visibility: "workspace",
    metadata: { pack: "fleetdb-artifacts" },
  });
  // Bind the id the server echoed, never the one requested: a bind on a create that
  // failed would point the read sweep at a 404 and score it as coverage.
  const artRow = unwrap(artRes.body, "artifact") as { artifact_id?: string } | null;
  if (artRow?.artifact_id) w.bind("artifacts.artifact_id", artRow.artifact_id);
  // No uri was sent, so Normalize() lands this on durable_status "declared" rather
  // than "finalized" -- the distinction the whole upload ladder rests on.
  await w.checkpoint("artifact-declared");

  await w.fleet("GET", `/api/v1/${ws}/artifacts?owner_type=driver_run&owner_id=${RUN}&limit=50`);
  await w.fleet("GET", `/api/v1/${ws}/artifacts?durable_status=declared&type=log`);
  await w.fleet("GET", `/api/v1/${ws}/artifacts/${ARTIFACT}`);

  // PATCH takes the pointer-per-field ArtifactUpdate shape verbatim
  // (internal/storage/storage.go:407): an absent key means "leave alone", so a
  // partial patch that silently blanks a sibling field would be a real defect.
  await w.fleet("PATCH", `/api/v1/${ws}/artifacts/${ARTIFACT}`, {
    summary: "api-aft synthetic run log (patched)",
    redaction_status: "clean",
  });
  await w.checkpoint("artifact-patched");

  // The content legs use the local content store, which is the DEFAULT backend with
  // no env set (internal/service/artifact_content.go:48). Upload rewrites uri,
  // size_bytes, checksum, content_hash and moves durable_status to "uploading"; the
  // download leg must then serve the same bytes back as an inert attachment.
  await w.fleet("PUT", `/api/v1/${ws}/artifacts/${ARTIFACT}/content`, "api-aft artifact content bytes");
  await w.fleet("GET", `/api/v1/${ws}/artifacts/${ARTIFACT}/content`);
  await w.checkpoint("artifact-content-uploaded");

  // finalize re-verifies the stored bytes against the row's hash and size before it
  // flips durable_status, returning 422 if they drifted (control_plane.go:625). This
  // is the step that makes a finalized artifact a claim rather than a hope.
  await w.fleet("POST", `/api/v1/${ws}/artifacts/${ARTIFACT}/finalize`, {
    summary: "api-aft synthetic run log (finalized)",
  });
  await w.checkpoint("artifact-finalized");

  // --- connectors ---------------------------------------------------------------
  //
  // source_kind "internal" needs no third party. Every non-privileged read runs
  // through Redacted(), which blanks inbound_secret and drops the sealed outbound
  // credential (internal/models/connector.go:120) -- so a secret in the create, list
  // or get response is a leak, not a feature.
  const connRes = await w.fleet("POST", `/api/v1/${ws}/connectors`, {
    connector_id: CONNECTOR,
    source_kind: "internal",
    display_name: "api-aft internal connector",
    inbound_endpoint_path: "/hooks/api-aft",
    inbound_secret: "apiaft-inbound-secret-v1",
    status: "active",
    created_by: "api-aft",
  });
  const connRow = unwrap(connRes.body, "connector") as { connector_id?: string } | null;
  if (connRow?.connector_id) w.bind("connectors.connector_id", connRow.connector_id);
  await w.checkpoint("connector-registered");

  await w.fleet("GET", `/api/v1/${ws}/connectors?source_kind=internal&status=active&limit=50`);
  await w.fleet("GET", `/api/v1/${ws}/connectors/${CONNECTOR}`);
  // The privileged resolve endpoint -- the ONLY surface that may return the secrets.
  await w.fleet("GET", `/api/v1/${ws}/connectors/${CONNECTOR}/secrets`);

  // Rotation opens the dual-secret window: the previous inbound secret must keep
  // verifying until previous_secret_valid_until, or every in-flight webhook from the
  // source fails signature check at the moment of rotation.
  await w.fleet("POST", `/api/v1/${ws}/connectors/${CONNECTOR}/rotate`, {
    new_inbound_secret: "apiaft-inbound-secret-v2",
    previous_secret_valid_until: new Date(Date.now() + 900_000).toISOString(),
  });
  await w.fleet("GET", `/api/v1/${ws}/connectors/${CONNECTOR}/secrets`);
  await w.checkpoint("connector-rotated");

  // Grants are deny-by-default and standalone: TriggerBinding.Permissions[] is NOT
  // auto-migrated into them (models/connector.go:145), so egress fails closed until a
  // grant row exists. The action must be dotted provider.verb (ValidateConnectorAction).
  await w.fleet("POST", `/api/v1/${ws}/connector-grants`, {
    grant_id: GRANT,
    connector_id: CONNECTOR,
    binding_id: BINDING,
    action: "internal.publish",
    resource_pattern: `workspace:${ws}`,
  });
  await w.fleet("GET", `/api/v1/${ws}/connector-grants?binding_id=${BINDING}&limit=50`);
  await w.checkpoint("connector-grant-created");

  // Revoke stamps revoked_at rather than deleting: an audit trail that deletion can
  // rewrite is not an audit trail. The empty body is not decorative -- fleet-db's
  // ContentType middleware 415s every POST without application/json
  // (internal/api/middleware.go:150), including the several that read no body at all.
  await w.fleet("POST", `/api/v1/${ws}/connector-grants/${GRANT}/revoke`, {});
  await w.fleet("GET", `/api/v1/${ws}/connector-grants?connector_id=${CONNECTOR}`);
  await w.checkpoint("connector-grant-revoked");

  // --- connector-call audit -----------------------------------------------------
  //
  // Both outcomes are journaled, and that is the point: the denied row is the only
  // evidence that the deny-by-default model actually stopped something. call_id is
  // derived as "runID#action#seq" server-side and a duplicate append is rejected as
  // already-exists, which is what makes a retried egress attempt safe to record.
  await w.fleet("POST", `/api/v1/${ws}/connector-audit`, {
    seq: 1,
    run_id: RUN,
    binding_id: BINDING,
    connector_id: CONNECTOR,
    source_kind: "internal",
    action: "internal.publish",
    resource: `workspace:${ws}`,
    decision: "granted",
    upstream_status: 200,
    sanitized_summary: "api-aft synthetic granted egress",
  });
  await w.fleet("POST", `/api/v1/${ws}/connector-audit`, {
    seq: 2,
    run_id: RUN,
    binding_id: BINDING,
    connector_id: CONNECTOR,
    source_kind: "internal",
    action: "internal.publish",
    resource: `workspace:${ws}`,
    decision: "denied",
    error_class: "grant_revoked",
    sanitized_summary: "api-aft synthetic denied egress",
  });
  // The list leg 400s unless one of run_id / binding_id is supplied
  // (internal/api/connector.go:265) -- an unscoped audit dump is refused by design.
  await w.fleet("GET", `/api/v1/${ws}/connector-audit?run_id=${RUN}&limit=50`);
  await w.fleet("GET", `/api/v1/${ws}/connector-audit?binding_id=${BINDING}&decision=denied`);
  await w.checkpoint("connector-audit-journaled");

  // --- leases -------------------------------------------------------------------
  //
  // The generic lease is the resource-scoped one. The response carries the plaintext
  // token exactly once; the row itself stores only a hash and publicLease() strips
  // even that (control_plane.go:869). Losing the token means losing the ability to
  // renew or release -- so the token is captured from the acquire response here.
  const acquired = await w.fleet("POST", `/api/v1/${ws}/leases/acquire`, {
    lease_id: LEASE,
    resource_type: "artifact_upload",
    resource_id: ARTIFACT,
    holder_node_id: "node-apiaft-1",
    holder_runner_id: "runner-apiaft-1",
    ttl_seconds: 300,
  });
  const leaseToken = (acquired.body as { token?: string } | null)?.token ?? "";
  const leaseRow = unwrap(acquired.body, "lease") as { lease_id?: string } | null;
  if (leaseRow?.lease_id) w.bind("leases.lease_id", leaseRow.lease_id);
  await w.checkpoint("lease-acquired");

  await w.fleet("GET", `/api/v1/${ws}/leases?resource_type=artifact_upload&status=active&limit=50`);
  await w.fleet("GET", `/api/v1/${ws}/leases/${LEASE}`);

  if (leaseToken) {
    // ttl_seconds rides on the QUERY string for renew, not the body: parseTTL reads
    // r.URL.Query() (control_plane.go:1256). A body ttl here is silently ignored.
    await fleetWithToken(w, "POST", `/api/v1/${ws}/leases/${LEASE}/heartbeat?ttl_seconds=600`, leaseToken, {});
    await w.checkpoint("lease-renewed");
    await fleetWithToken(w, "POST", `/api/v1/${ws}/leases/${LEASE}/release`, leaseToken, {});
    await w.checkpoint("lease-released");
  }

  // --- action ledger ------------------------------------------------------------
  //
  // The ledger exists so an externally-visible action (merge a PR, close a task) is
  // performed at most once. The whole value is in the replay leg below.
  const ledgerRes = await w.fleet("POST", `/api/v1/${ws}/action-ledger`, {
    action_id: ACTION,
    idempotency_key: "apiaft-close-task-1",
    action_type: "close_task",
    target_ref: `driver_run:${RUN}`,
    requested_by: "api-aft",
    status: "pending",
    request_ref: `artifact:${ARTIFACT}`,
  });
  const ledgerRow = unwrap(ledgerRes.body, "action") as { action_id?: string } | null;
  if (ledgerRow?.action_id) w.bind("action-ledger.action_id", ledgerRow.action_id);
  await w.checkpoint("action-ledger-pending");

  await w.fleet("GET", `/api/v1/${ws}/action-ledger?status=pending&limit=50`);
  await w.fleet("GET", `/api/v1/${ws}/action-ledger?idempotency_key=apiaft-close-task-1`);
  await w.fleet("GET", `/api/v1/${ws}/action-ledger/${ACTION}`);

  // Replay under the SAME idempotency key with a DIFFERENT action_id. CreateActionLedger
  // returns the pre-existing row instead of minting a second one
  // (internal/storage/platform.go:875) -- so the response must carry action_id
  // ACTION, not the id just sent.
  await w.fleet("POST", `/api/v1/${ws}/action-ledger`, {
    action_id: "action-apiaft-replay",
    idempotency_key: "apiaft-close-task-1",
    action_type: "close_task",
    target_ref: `driver_run:${RUN}`,
    requested_by: "api-aft",
  });
  await w.checkpoint("action-ledger-replayed");

  // complete refuses a non-terminal target status and is itself idempotent for a
  // repeated identical status (platform.go:934).
  await w.fleet("POST", `/api/v1/${ws}/action-ledger/${ACTION}/complete`, {
    status: "applied",
    response_ref: `artifact:${ARTIFACT}`,
  });
  await w.checkpoint("action-ledger-applied");

  // --- awaits (non-blocking legs only) ------------------------------------------
  //
  // await.go:1 documents register-and-check as ATOMIC: it registers and checks the
  // event journal in one Lua script and returns immediately with satisfied=true/false
  // -- there is deliberately no separate check endpoint and no long-poll, so none of
  // the legs below can hang. The two that are skipped are the DriverRun suspend and
  // resume legs: those need a run already claimed with a node id, lease id and
  // fencing token, which belongs to a driver-run ownership pack, not this one.
  //
  // RULE 1: the pattern must be a rendered "event_type:subject" key, not a bare event
  // type. RULE 3: the instance key must be exactly runID#await-{n}, n >= 1. RULE 5:
  // the deadline is mandatory and must be in the future (models/await.go:41-52).
  const awaitKey = `${RUN}#await-1`;
  const pattern = `pr.merged:${RUN}`;
  const registered = await w.fleet("POST", `/api/v1/${ws}/awaits/register-and-check`, {
    instance_key: awaitKey,
    run_id: RUN,
    pattern,
    deadline: new Date(Date.now() + 3_600_000).toISOString(),
  });
  const awaitRow = unwrap(registered.body, "await") as { instance_key?: string } | null;
  if (awaitRow?.instance_key) w.bind("awaits.instance_key", awaitRow.instance_key);
  await w.checkpoint("await-registered");

  // The dispatch fan-out read: EVERY pending await on this pattern -- one event
  // resolves all of them (the multi-waiter decision, await.go:213).
  await w.fleet("GET", `/api/v1/${ws}/awaits?pattern=${encodeURIComponent(pattern)}`);
  // The deadline sweeper's work queue. `before` in the future proves the row is
  // indexed by deadline; `before` = now answers empty for a fresh await either way.
  await w.fleet(
    "GET",
    `/api/v1/${ws}/awaits/due?before=${encodeURIComponent(new Date(Date.now() + 7_200_000).toISOString())}&limit=50`,
  );
  await w.fleet("GET", `/api/v1/${ws}/awaits/${encodeURIComponent(awaitKey)}`);

  // The public resolve lane accepts only "satisfied"; timed_out and cancelled require
  // the privileged /resolve-system route, so a plain PermAwaitUpdate holder can never
  // forge a system resolution (await.go:164).
  await w.fleet("POST", `/api/v1/${ws}/awaits/${encodeURIComponent(awaitKey)}/resolve`, {
    event_id: "evt-apiaft-pr-merged-1",
    actor: "api-aft",
    payload: { merged: true, source: "api-aft" },
  });
  await w.checkpoint("await-satisfied");

  // The replay surface: the satisfied row with its size-capped resume payload inline.
  // A still-pending instance 404s here, which is why this call follows the resolve.
  await w.fleet("GET", `/api/v1/${ws}/awaits/${encodeURIComponent(awaitKey)}/satisfied`);

  // A second instance on the same run exercises the system lane's timeout leg -- the
  // path the deadline sweeper takes. Sequential awaits use an increasing ordinal.
  const timedOutKey = `${RUN}#await-2`;
  await w.fleet("POST", `/api/v1/${ws}/awaits/register-and-check`, {
    instance_key: timedOutKey,
    run_id: RUN,
    pattern: `deploy.finished:${RUN}`,
    deadline: new Date(Date.now() + 1_800_000).toISOString(),
  });
  await w.fleet("POST", `/api/v1/${ws}/awaits/${encodeURIComponent(timedOutKey)}/resolve-system`, {
    event_id: "evt-apiaft-timeout-1",
    status: "timed_out",
  });
  await w.checkpoint("await-timed-out");
}
