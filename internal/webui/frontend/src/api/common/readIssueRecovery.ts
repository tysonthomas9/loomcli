import { getAuthToken, wsUrl } from "./client";
import {
  prepareIssueRecovery,
  selectedHistoryIdentity,
  type PreparedIssueRecovery,
} from "./issueRecovery";
import { decodeRecoveryHandle, type RecoveryHandle } from "./recoveryHandle";

const BODY_LIMIT = 16 * 1024 * 1024;
const HANDLE_HEADER = "X-Loom-Recovery-Handle";

/** Read one captured source. This prepares data; it never publishes or acknowledges it. */
export async function readIssueRecovery(
  input: RecoveryHandle,
  signal: AbortSignal,
  expectedIssueId?: string,
): Promise<PreparedIssueRecovery> {
  signal.throwIfAborted();
  if (expectedIssueId !== undefined) selectedHistoryIdentity(expectedIssueId);
  const offer = decodeRecoveryHandle(
    input,
    input?.workspace,
    input?.source_repos,
  );
  if (!offer) throw new Error("Invalid recovery offer");
  const path = wsUrl(offer.workspace, "/events/recovery/issues");
  const url =
    expectedIssueId === undefined
      ? path
      : `${path}?${new URLSearchParams({ issue_id: expectedIssueId })}`;
  const token = getAuthToken();
  if (!token?.trim()) throw new Error("Recovery authentication required");
  const controller = new AbortController();
  const cancel = () => controller.abort(signal.reason);
  signal.addEventListener("abort", cancel, { once: true });
  if (signal.aborted) cancel();
  const deadline = Math.min(Date.now() + 15_000, Date.parse(offer.expires_at));
  const timer = setTimeout(
    () =>
      controller.abort(
        new DOMException("Recovery read expired", "TimeoutError"),
      ),
    Math.max(0, deadline - Date.now()),
  );
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  let received: Response | undefined;
  let abortListener: (() => void) | undefined;
  const aborted = new Promise<never>((_, reject) => {
    abortListener = () => reject(controller.signal.reason);
    controller.signal.addEventListener("abort", abortListener, { once: true });
    if (controller.signal.aborted) abortListener();
  });
  // Handles cancellation during synchronous admission before the first race.
  void aborted.catch(() => {});
  const check = () => {
    controller.signal.throwIfAborted();
    if (Date.now() >= deadline) throw new Error("Recovery offer expired");
  };
  try {
    check();
    const response = await Promise.race([
      fetch(url, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          [HANDLE_HEADER]: offer.handle,
        },
        redirect: "error",
        cache: "no-store",
        signal: controller.signal,
      }).then((response) => {
        if (controller.signal.aborted) {
          void response.body?.cancel().catch(() => {});
          controller.signal.throwIfAborted();
        }
        return response;
      }),
      aborted,
    ]);
    received = response;
    check();
    if (
      response.status !== 200 ||
      response.headers.get(HANDLE_HEADER) !== offer.handle ||
      response.headers.get("X-Loom-Recovery-Source") !==
        offer.source_identity ||
      response.headers
        .get("Content-Type")
        ?.split(";", 1)[0]
        ?.trim()
        .toLowerCase() !== "application/json" ||
      !response.body
    ) {
      throw new Error("Invalid recovery response");
    }
    reader = response.body.getReader();
    const decoder = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });
    const parts: string[] = [];
    let bytes = 0;
    for (;;) {
      const part = await Promise.race([reader.read(), aborted]);
      check();
      if (part.done) break;
      bytes += part.value.byteLength;
      if (bytes > BODY_LIMIT)
        throw new Error("Recovery response exceeds size limit");
      const decoded = decoder.decode(part.value, { stream: true });
      if (decoded) parts.push(decoded);
    }
    parts.push(decoder.decode());
    check();
    const result = prepareIssueRecovery(
      parts.join(""),
      offer,
      offer.handle,
      Date.now(),
      expectedIssueId,
    );
    check();
    return result;
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", cancel);
    if (abortListener)
      controller.signal.removeEventListener("abort", abortListener);
    controller.abort();
    if (reader) {
      // Do not await an uncooperative stream's cancellation promise.
      void reader.cancel().catch(() => {});
      reader.releaseLock();
    } else {
      void received?.body?.cancel().catch(() => {});
    }
  }
}
