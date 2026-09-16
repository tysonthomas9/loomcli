// Scenario: fleet-db's messaging + trigger plane, driven end to end.
//
// Why these operations matter: everything loom does asynchronously rides this
// surface. The pub/sub bus (`topics`) is the transport, the durable-consume
// subset (subscriptions + drain lease + ack + dead-letter) is the ONLY
// at-least-once path in either repo, the outbox is the store-and-forward queue
// for lead notifications, and trigger bindings/events/deliveries are the
// router that turns an inbound event into a driver run. A silent regression
// anywhere here does not surface as a 500 -- it surfaces as work that never
// happens, which is precisely the failure a status-code test cannot see and a
// seam-differential + replay-verify oracle can.
//
// Two things dominated the authoring effort and are worth reading before
// editing:
//
//  1. LONG-POLL. `GET .../topics/{topic}/messages` and the durable pull both
//     accept a `timeout` (ms) query parameter that blocks the request until a
//     message arrives (internal/api/pubsub.go:174 and pubsub_durable.go:196).
//     The harness has a 10s hard request ceiling, so every read below asks for
//     the NON-blocking form by simply omitting `timeout` -- the handler's fast
//     path returns immediately when `timeout == 0` (pubsub.go:186).
//
//  2. REFERENTIAL PREREQUISITES. A trigger binding is rejected unless its
//     driver_version_id resolves AND that version's driver_id matches
//     (internal/storage/platform.go:343-350), and a trigger delivery is
//     rejected unless both its event and its binding already exist
//     (platform.go:643-648). So the pack registers a real driver and a real
//     driver version first. Nothing below is fabricated: every id bound for
//     the read sweep is an id this file actually created.

import type { World } from "../src/world.ts";
import { call, type Exchange } from "../src/wire.ts";

export const name = "fleet-db messaging: topics, durable consume, outbox, triggers";

const TOPIC = "apiaft.msg";
const SCRATCH_TOPIC = "apiaft.scratch";
const SUB = "apiaft-drainer";
const OWNER = "apiaft-owner-1";

const DRIVER = "apiaft-msg-driver";
const VERSION = "apiaft-msg-driver-v1";
const BINDING = "apiaft-msg-binding";
const SCRATCH_BINDING = "apiaft-scratch-binding";

/**
 * The drain lease token travels in the X-Lease-Token header, deliberately kept
 * out of the query string and body so it cannot leak into logs or proxies
 * (internal/api/pubsub_durable.go:15). World.fleet() has no header channel, so
 * the leased calls go through the wire layer directly -- still recorded, still
 * attributed to the current checkpoint.
 */
function leased(w: World, method: string, path: string, token: string, body?: unknown): Promise<Exchange> {
  return call(w.rec, {
    service: "fleetdb",
    baseUrl: w.stack.fleetUrl,
    method,
    path,
    body,
    headers: { "x-lease-token": token },
  });
}

function field(ex: Exchange, key: string): string {
  const o = ex.body as Record<string, unknown> | null;
  const v = o?.[key];
  return typeof v === "string" ? v : "";
}

export async function run(w: World): Promise<void> {
  const ws = w.workspace;

  // Same workspace bootstrap the reference pack uses: the shape a real client
  // sends (internal/webui/service/workspace_types.go:12). Idempotent, so this
  // pack runs standalone under the `run <substring>` filter as well as in the
  // full suite.
  await w.loom("POST", "/api/workspaces", { name: ws, type: "empty" });

  await publishAndRead(w, ws);
  await durableConsume(w, ws);
  await triggerRouter(w, ws);
  await outbox(w, ws);
}

/** The broadcast half of the bus: publish, one-shot read, list, delete. */
async function publishAndRead(w: World, ws: string): Promise<void> {
  const base = `/api/v1/${ws}/topics/${TOPIC}`;

  // `payload` is the only required field (models.Message.Validate,
  // internal/models/message.go:169) and is stored as raw JSON, so any JSON
  // value is legal. `kind` is an opaque discriminator, `id` makes the publish
  // idempotent.
  await w.fleet("POST", `${base}/messages`, { kind: "apiaft.note", payload: { seq: 1 } });
  await w.fleet("POST", `${base}/messages`, { kind: "apiaft.note", payload: { seq: 2 } });
  await w.fleet("POST", `${base}/messages`, {
    id: "apiaft-msg-3",
    kind: "apiaft.note",
    trace: { traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" },
    payload: { seq: 3 },
  });
  // Republish under the SAME id. The service documents this as idempotent
  // (internal/service/pubsub_service.go:118), so the bus must not grow a
  // fourth message -- a duplicate here would double-deliver every consumer.
  await w.fleet("POST", `${base}/messages`, { id: "apiaft-msg-3", kind: "apiaft.note", payload: { seq: 3 } });

  // NON-BLOCKING read: no `timeout`, so the handler's fast path answers at
  // once. `from` is exclusive and "$" is rejected outright (pubsub.go:158).
  await w.fleet("GET", `${base}/messages?from=0&limit=50`);
  await w.fleet("GET", `/api/v1/${ws}/topics`);

  w.bind("topics.topic", TOPIC);
  await w.checkpoint("topic-published");

  // A scratch topic exercises DELETE without destroying the topic the sweep
  // will read back. Deleting the bound topic instead would turn every later
  // sweep GET into an honest-looking 404.
  await w.fleet("POST", `/api/v1/${ws}/topics/${SCRATCH_TOPIC}/messages`, { payload: { scratch: true } });
  await w.fleet("DELETE", `/api/v1/${ws}/topics/${SCRATCH_TOPIC}`);
  await w.checkpoint("scratch-topic-deleted");
}

/**
 * The at-least-once half: a durable subscription, the single-drainer lease,
 * a CAS ack, and a dead-letter. This is the only place in either repo where a
 * message can be lost or doubled, so it is the part worth driving properly
 * rather than smoke-testing.
 */
async function durableConsume(w: World, ws: string): Promise<void> {
  const base = `/api/v1/${ws}/topics/${TOPIC}/subscriptions/${SUB}`;

  // start_cursor "0" = from the beginning; the body is optional and the
  // handler only decodes it when ContentLength is non-zero
  // (internal/api/pubsub_durable.go:84).
  await w.fleet("PUT", base, { start_cursor: "0" });
  w.bind("topics.sub", SUB);
  await w.fleet("GET", `/api/v1/${ws}/topics/${TOPIC}/subscriptions`);

  // Acquire. `owner` is required (models.ValidateOwnerName) and ttl_seconds is
  // clamped to [5s, 5m] at the service edge (pubsub_service.go:92). Note the
  // spec declares NO request body for this operation even though the handler
  // requires one -- see the report.
  const lease = await w.fleet("POST", `${base}/lease`, { owner: OWNER, ttl_seconds: 60 });
  const token = field(lease, "token");
  await w.checkpoint("drain-lease-acquired");

  if (token) {
    // Durable pull, lease-guarded and NON-blocking (no `timeout`). It returns
    // the backlog after the cursor WITHOUT advancing it and bumps the head's
    // attempt counter -- that attempt count is what later gates dead-lettering.
    const pull = await leased(w, "GET", `${base}/messages?limit=50`, token);
    const head = field(pull, "head");

    // Ack is a compare-and-swap on the cursor: `from` must equal the cursor as
    // it stands, or the store answers ErrCursorConflict -> 409
    // (internal/storage/redis_pubsub_durable.go:243). The subscription starts
    // at "0", so that is the honest `from` for the first ack.
    if (head) await leased(w, "POST", `${base}/ack`, token, { from: "0", position: head });
    await w.checkpoint("subscription-acked");

    // Re-pull to learn the NEW head, then dead-letter exactly that cursor.
    // expected_cursor is a head-mismatch guard (topic_deadletter.lua:20), and
    // max_attempts 0 disables the "attempts must have reached N" readiness
    // gate -- `if maxA > 0 and attempts < maxA` in the same script.
    const pull2 = await leased(w, "GET", `${base}/messages?limit=50`, token);
    const head2 = field(pull2, "head");
    if (head2) {
      await leased(w, "POST", `${base}/dead-letter-next`, token, {
        expected_cursor: head2,
        max_attempts: 0,
        reason: "api-aft: exercising the dead-letter path",
      });
    }
    await w.checkpoint("message-dead-lettered");

    // A POST to the same lease route RENEWS instead of acquiring when the
    // token header is present (pubsub_durable.go:120) -- one route, two verbs
    // in effect, discriminated by a header the spec never declares.
    await leased(w, "POST", `${base}/lease`, token, { owner: OWNER, ttl_seconds: 120 });

    // Release takes the owner as a QUERY parameter, not in a body: DELETE
    // carries no body here (pubsub_durable.go:155).
    await leased(w, "DELETE", `${base}/lease?owner=${OWNER}`, token);
    await w.checkpoint("drain-lease-released");
  }
}

/**
 * The router: a driver + version to point at, a binding, events (including a
 * workflow child whose hop_depth the server derives), and deliveries moving
 * through the retry due-index.
 */
async function triggerRouter(w: World, ws: string): Promise<void> {
  // Prerequisites. driver_version_id must resolve AND its driver_id must match
  // the binding's driver_id (internal/storage/platform.go:343). source_digest,
  // bundle_digest and a positive version are all required by
  // models.DriverVersion.Validate (internal/models/platform.go:147).
  await w.fleet("POST", `/api/v1/${ws}/drivers`, {
    driver_id: DRIVER,
    name: "api-aft messaging driver",
    owner_type: "system",
    status: "active",
    trust_level: "trusted",
  });
  await w.fleet("POST", `/api/v1/${ws}/drivers/${DRIVER}/versions`, {
    version_id: VERSION,
    version: 1,
    source_ref: "api-aft://messaging",
    source_digest: "sha256:apiaft-source",
    bundle_ref: "api-aft://messaging/bundle",
    bundle_digest: "sha256:apiaft-bundle",
    runtime: "node",
    validation_status: "passed",
  });
  // Real state this pack created, so it is honest to hand it to the sweep even
  // though drivers are not this pack's subject.
  w.bind("drivers.driver_id", DRIVER);
  w.bind("driver-versions.version_id", VERSION);

  // source_kind "internal" = fed by workflow-emitted events
  // (models.TriggerSourceKindInternal). The subject key template's tokens are
  // restricted to subject_ref / event_type / attrs.<name>
  // (models.ValidateSubjectKeyTemplate); anything else is a 400.
  await w.fleet("POST", `/api/v1/${ws}/trigger-bindings`, {
    binding_id: BINDING,
    name: "api-aft messaging binding",
    source_kind: "internal",
    source_ref: "api-aft",
    route_key: "apiaft-msg-route",
    topic: TOPIC,
    event_type_patterns: ["issue.*"],
    driver_id: DRIVER,
    driver_version_id: VERSION,
    concurrency_policy: "one_active_per_epic",
    subject_key_template: "{{subject_ref}}|{{event_type}}",
    actor_filter: { exclude_actor_kinds: ["driver"] },
    retry_max_attempts: 3,
    retry_backoff_seconds: 15,
    // Set so the dedicated secret endpoint has something real to return, and
    // so the redaction on the ordinary read/list surfaces
    // (redactedTriggerBinding, internal/api/platform.go:755) is observable.
    webhook_secret: "apiaft-webhook-secret",
    enabled: true,
  });
  w.bind("trigger-bindings.binding_id", BINDING);

  await w.fleet("GET", `/api/v1/${ws}/trigger-bindings?enabled=true&limit=20`);
  await w.fleet("GET", `/api/v1/${ws}/trigger-bindings/${BINDING}`);
  await w.fleet("GET", `/api/v1/${ws}/trigger-bindings/${BINDING}/webhook-secret`);
  await w.fleet("PATCH", `/api/v1/${ws}/trigger-bindings/${BINDING}`, {
    retry_backoff_seconds: 45,
    source_config_ref: "api-aft://config/messaging",
  });
  await w.checkpoint("trigger-binding-registered");

  // A throwaway binding so DELETE is exercised without unbinding the one the
  // sweep reads. Its route_key differs because route keys are reserved.
  await w.fleet("POST", `/api/v1/${ws}/trigger-bindings`, {
    binding_id: SCRATCH_BINDING,
    name: "api-aft scratch binding",
    source_kind: "internal",
    route_key: "apiaft-scratch-route",
    driver_id: DRIVER,
    driver_version_id: VERSION,
  });
  await w.fleet("DELETE", `/api/v1/${ws}/trigger-bindings/${SCRATCH_BINDING}`);
  await w.checkpoint("trigger-binding-deleted");

  await triggerEventsAndDeliveries(w, ws);
}

async function triggerEventsAndDeliveries(w: World, ws: string): Promise<void> {
  // origin is server-stamped, never client-trusted: "external" is refused here
  // because only the webhook ingest path may claim it, and "system" pins
  // hop_depth to 0 (stampTriggerEventProvenance, internal/api/platform.go:889).
  const seed = {
    source_kind: "internal",
    event_type: "issue.created",
    subject_ref: "apiaft-subject-1",
    trigger_binding_id: BINDING,
    idempotency_key: "apiaft-trigger-event-1",
    origin: "system",
  };
  const e1 = await w.fleet("POST", `/api/v1/${ws}/trigger-events`, seed);
  const eventId = field(e1, "event_id");
  if (eventId) w.bind("trigger-events.event_id", eventId);

  // Replay on the same idempotency_key: the store returns the EXISTING event
  // rather than creating a second one (internal/storage/platform.go:559).
  await w.fleet("POST", `/api/v1/${ws}/trigger-events`, seed);

  // A workflow-originated child. hop_depth is derived from the parent's
  // persisted depth, never from anything the client sends -- that is what
  // bounds re-trigger chains at the cap (models.NextTriggerHopDepth).
  if (eventId) {
    await w.fleet("POST", `/api/v1/${ws}/trigger-events`, {
      source_kind: "internal",
      event_type: "issue.updated",
      subject_ref: "apiaft-subject-1",
      trigger_binding_id: BINDING,
      origin: "workflow",
      parent_event_id: eventId,
    });
  }
  await w.fleet("GET", `/api/v1/${ws}/trigger-events?source_kind=internal&limit=20`);
  if (eventId) await w.fleet("GET", `/api/v1/${ws}/trigger-events/${eventId}`);
  await w.checkpoint("trigger-events-recorded");

  if (!eventId) return;

  // A delivery only lands on the retry due-index when it is `held` or a
  // NON-exhausted `failed` (triggerDeliveryInDueIndex, platform.go:826), and
  // its due score is next_retry_at. Backdating that makes the row due now, so
  // GET /trigger-deliveries/due observes real sweeper work rather than [].
  const overdue = new Date(Date.now() - 60_000).toISOString();
  await w.fleet("POST", `/api/v1/${ws}/trigger-deliveries`, {
    delivery_id: "apiaft-delivery-1",
    trigger_event_id: eventId,
    trigger_binding_id: BINDING,
    status: "failed",
    attempt: 1,
    error_class: "transient",
    next_retry_at: overdue,
  });
  w.bind("trigger-deliveries.delivery_id", "apiaft-delivery-1");
  // A second retryable row is left untouched so the due-list is still
  // non-empty when the read sweep replays it after every pack has finished.
  await w.fleet("POST", `/api/v1/${ws}/trigger-deliveries`, {
    delivery_id: "apiaft-delivery-2",
    trigger_event_id: eventId,
    trigger_binding_id: BINDING,
    status: "held",
    attempt: 1,
    next_retry_at: overdue,
  });

  await w.fleet("GET", `/api/v1/${ws}/trigger-deliveries/due?limit=20`);
  await w.fleet("GET", `/api/v1/${ws}/trigger-deliveries?trigger_event_id=${eventId}&limit=20`);
  await w.fleet("GET", `/api/v1/${ws}/trigger-deliveries/apiaft-delivery-1`);
  await w.checkpoint("trigger-delivery-due");

  // Recording a terminal outcome must take the row OUT of the due index --
  // the sweeper would otherwise retry a dispatched delivery forever.
  await w.fleet("POST", `/api/v1/${ws}/trigger-deliveries/apiaft-delivery-1/result`, {
    status: "dispatched",
    attempt: 2,
  });
  await w.checkpoint("trigger-delivery-dispatched");
}

/** Store-and-forward queue for lead notifications: create, due, resolve. */
async function outbox(w: World, ws: string): Promise<void> {
  // kind is a closed enum of two values (models.OutboxKind) and dedupe_key is
  // required; a create with a dedupe_key already in use returns the EXISTING
  // row (internal/storage/outbox.go:165) rather than a conflict.
  await w.fleet("POST", `/api/v1/${ws}/outbox`, {
    outbox_id: "apiaft-outbox-1",
    kind: "lead_assignment",
    target_agent: "apiaft-lead",
    body: '{"note":"api-aft outbox row"}',
    dedupe_key: "apiaft-outbox-dedupe-1",
  });
  w.bind("outbox.outbox_id", "apiaft-outbox-1");

  // Left pending on purpose: keeps /outbox/due non-empty for the read sweep.
  await w.fleet("POST", `/api/v1/${ws}/outbox`, {
    outbox_id: "apiaft-outbox-2",
    kind: "lead_task_message",
    target_agent: "apiaft-lead",
    body: '{"note":"api-aft pending row"}',
    dedupe_key: "apiaft-outbox-dedupe-2",
  });

  // Same dedupe_key, DIFFERENT outbox_id: the caller's id is discarded and the
  // first row comes back. Worth driving because a client that trusts the id it
  // sent would then poll a row that does not exist.
  await w.fleet("POST", `/api/v1/${ws}/outbox`, {
    outbox_id: "apiaft-outbox-1-replay",
    kind: "lead_assignment",
    dedupe_key: "apiaft-outbox-dedupe-1",
  });

  await w.fleet("GET", `/api/v1/${ws}/outbox/due?limit=20`);
  await w.fleet("GET", `/api/v1/${ws}/outbox/apiaft-outbox-1`);
  await w.checkpoint("outbox-queued");

  await w.fleet("POST", `/api/v1/${ws}/outbox/apiaft-outbox-1/result`, {
    status: "delivered",
    attempt: 1,
    inbox_message_id: "apiaft-inbox-1",
  });
  await w.checkpoint("outbox-delivered");
}
