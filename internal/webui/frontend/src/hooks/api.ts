/**
 * API surface for the components layer.
 *
 * Per the Phase 7 frontend layer DAG (enforced by eslint-plugin-boundaries),
 * components cannot import from @/api directly. They reach api functions
 * through the hooks layer, which owns data-fetching responsibility. This
 * file re-exports the @/api barrel so components can call api functions
 * via `import { createIssue, ... } from "@/hooks/api"`.
 *
 * Why re-export vs. thin `useXxx` wrappers: 38 component→api call sites
 * were fixed as part of loomcli-rir82.12. Writing a `useCreateIssue`-style
 * wrapper for each would have added ceremony (one hook per api function)
 * without any runtime benefit, since none of the call sites need shared
 * loading/error state. Re-exporting through the hooks layer satisfies the
 * architectural intent (hooks own the api surface) while keeping the
 * diff minimal. Hooks that legitimately wrap loading state (`useIssueDetail`,
 * `useBackends`, etc.) continue to live as real `useXxx` files.
 */

export * from "@/api";

// gitPushAll is not in the @/api barrel (by design — the api barrel only
// exports the canonical git verbs). Re-export it explicitly so components
// can still reach it through the hooks layer.
export { gitPushAll } from "@/api/workspace";

// Session history is a thin sub-module that isn't in the @/api barrel.
// Re-export what components need.
export { listSessionHistory, getSessionScrollback } from "@/api/terminal";
export type { SessionRecord } from "@/api/terminal";

// Terminal sub-module functions not in the @/api barrel.
export {
  spawnTerminalSession,
  patchTerminalState,
  getExportUrl,
  restartTerminalSession,
  closeAllSessions,
  fetchScrollback,
  seedTerminalSession,
  deleteTabMetadata,
  scheduleSessionKill,
} from "@/api/terminal";

// Workspace mutation helpers not in the @/api barrel.
export {
  renameWorkspace,
  deleteWorkspace,
  reorderWorkspaces,
  createWorkspace,
  createWorkspaceEpic,
  createWorkspaceTask,
} from "@/api/workspace";
