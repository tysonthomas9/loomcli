// Watch-driven epic runner: claim ready tasks as the epic evolves, reacting
// to server-pushed SSE events instead of polling. This mirrors the epic-runner
// shape used by the platform's own driver bundles.
//
// epics.watch yields three event types:
//   snapshot — full epic state (first event, and after reconnects)
//   taskRun  — a task run changed
//   closed   — the epic reached a terminal state (generator ends)
// Reconnection and Last-Event-ID resume are handled inside the SDK.
import { createLoomClient } from "@loom/sdk/flue";

export default async function run() {
  const loom = createLoomClient();
  const epicId = loom.input.epicId;
  if (!epicId) return loom.failed({ summary: "input.epicId is required", errorClass: "bad_input" });

  let dispatched = 0;

  // Claim everything that is ready right now, then again on every change.
  const claimReady = async () => {
    for (;;) {
      const claimed = await loom.tasks.claimReady({ epicId });
      if (!claimed) return;
      dispatched++;
      await loom.taskRuns.request({ taskId: claimed.taskId });
    }
  };

  await claimReady();
  for await (const ev of loom.epics.watch({ epicId })) {
    if (ev.type === "closed") break;
    // snapshot or taskRun: the epic moved — new tasks may be ready.
    await claimReady();
  }

  return loom.completed({ summary: `epic settled; dispatched ${dispatched} task runs` });
}
