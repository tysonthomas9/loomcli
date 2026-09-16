// Scenario: an operator creates an issue, reads it back, and closes it.
//
// There is not a single assertion in this file, and there must never be
// (scripts/check-no-asserts.mjs enforces it). A scenario's whole job is to MOVE THE
// SYSTEM through states a real client would. Every check comes from the invariant
// registry, auto-dispatched at each checkpoint -- so this 40-line file picks up the
// full L1/L2/L3 oracle stack without naming any of it, and every invariant added
// later strengthens this scenario retroactively.

import type { World } from "../src/world.ts";
import { unwrap } from "../src/world.ts";

export const name = "create -> read back -> close, through the loom API";

export async function run(w: World): Promise<void> {
  // The spec declares POST /api/workspaces requires {name, path}. Send exactly that
  // first: if it is right the scenario proceeds, and if it is wrong the conformance
  // lane records the deviation from the real exchange rather than from a guess.
  await w.loom("POST", "/api/workspaces", { name: w.workspace, path: `/tmp/${w.workspace}` });

  // The shape the real client sends (internal/webui/service/workspace_types.go:12).
  await w.loom("POST", "/api/workspaces", { name: w.workspace, type: "empty" });

  await w.checkpoint("workspace-ready");

  const created = await w.loom("POST", `/api/workspaces/${w.workspace}/issues`, {
    title: "api-aft: create-readback-close",
    issue_type: "task",
    priority: 2,
    description: "Created by the api-aft vertical slice.",
  });
  const issue = unwrap(created.body, "issue") as { id?: string } | null;
  if (issue?.id) w.track(issue.id);

  await w.checkpoint("issue-created");

  // Read back through BOTH observers happens inside checkpoint(); here we only drive
  // the surfaces a client would touch.
  await w.loom("GET", `/api/workspaces/${w.workspace}/issues`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/ready`);

  if (issue?.id) {
    await w.loom("PATCH", `/api/workspaces/${w.workspace}/issues/${issue.id}`, {
      status: "in_progress",
      description: "Moved to in_progress by the slice.",
    });
    await w.checkpoint("issue-in-progress");

    await w.loom("POST", `/api/workspaces/${w.workspace}/issues/${issue.id}/close`, {});
    await w.checkpoint("issue-closed");
  }
}
