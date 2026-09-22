/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it } from "vitest";

import {
  agentFileBrowserTabsStorageKey,
  createFileBrowserStore,
  fileBrowserTabsStorageKey,
} from "../fileBrowserStore";
import { wsKey } from "@/utils/scopedStorage";
import { tabIdentityKey as legacyTabIdentityKey } from "@/utils/fileExplorerRefs";
import {
  checkoutExplorerRef,
  skillsExplorerRef,
  tabIdentityKey,
} from "@/utils/explorerRefs";

describe("fileBrowserStore", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("ignores older persisted tab schemas", () => {
    localStorage.setItem(
      wsKey("ws-1", fileBrowserTabsStorageKey()),
      JSON.stringify({
        v: 2,
        groups: [
          { tabs: [{ path: "workspace.txt" }], active: "workspace.txt" },
        ],
        mru: ["workspace.txt"],
      }),
    );

    const store = createFileBrowserStore({ workspaceId: "ws-1" });

    expect(store.getState().groups).toEqual([{ tabs: [], active: null }]);
    expect(store.getState().mru).toEqual([]);
  });

  it("falls back to empty state on malformed persisted state", () => {
    localStorage.setItem(wsKey("ws-1", fileBrowserTabsStorageKey()), "{bad");

    const store = createFileBrowserStore({ workspaceId: "ws-1" });

    expect(store.getState().groups).toEqual([{ tabs: [], active: null }]);
    expect(store.getState().mru).toEqual([]);
  });

  it("migrates v3 checkout tabs into v4 explorer refs", () => {
    const legacy = { ref: { scope: "workspace" as const }, path: "main.ts" };
    localStorage.setItem(
      wsKey("ws-1", fileBrowserTabsStorageKey()),
      JSON.stringify({
        v: 3,
        groups: [
          {
            tabs: [legacy],
            active: legacyTabIdentityKey(legacy),
          },
        ],
        mru: [legacy],
      }),
    );

    const store = createFileBrowserStore({ workspaceId: "ws-1" });
    expect(store.getState().groups[0]?.tabs).toEqual([
      {
        ref: checkoutExplorerRef({ scope: "workspace" }),
        path: "main.ts",
      },
    ]);
    expect(store.getState().v).toBe(4);
  });

  it("rejects malformed skill refs and paths from persisted v4 state", () => {
    const valid = {
      ref: skillsExplorerRef({ kind: "role", role: "reviewer" }),
      path: "audit/SKILL.md",
    };
    localStorage.setItem(
      wsKey("ws-1", fileBrowserTabsStorageKey()),
      JSON.stringify({
        v: 4,
        groups: [
          {
            tabs: [
              valid,
              {
                ref: {
                  kind: "skills",
                  group: { kind: "role", role: "bad/role" },
                },
                path: "audit/SKILL.md",
              },
              {
                ref: skillsExplorerRef({ kind: "role", role: "reviewer" }),
                path: "audit",
              },
            ],
            active: tabIdentityKey(valid),
          },
        ],
        mru: [valid],
      }),
    );

    const store = createFileBrowserStore({ workspaceId: "ws-1" });
    expect(store.getState().groups[0]?.tabs).toEqual([valid]);
  });

  it("drops a non-string persisted role without discarding valid tabs", () => {
    const valid = {
      ref: checkoutExplorerRef({ scope: "workspace" }),
      path: "main.ts",
    };
    localStorage.setItem(
      wsKey("ws-1", fileBrowserTabsStorageKey()),
      JSON.stringify({
        v: 4,
        groups: [
          {
            tabs: [
              {
                ref: {
                  kind: "skills",
                  group: { kind: "role", role: 1 },
                },
                path: "audit/SKILL.md",
              },
              valid,
            ],
            active: tabIdentityKey(valid),
          },
        ],
        mru: [valid],
      }),
    );

    const store = createFileBrowserStore({ workspaceId: "ws-1" });

    expect(store.getState().groups[0]?.tabs).toEqual([valid]);
    expect(store.getState().groups[0]?.active).toBe(tabIdentityKey(valid));
  });

  it("persists tabs from multiple checkouts in the same workspace store", () => {
    const store = createFileBrowserStore({ workspaceId: "ws-1" });

    store.getState().openTab({
      ref: checkoutExplorerRef({ scope: "workspace" }),
      path: "workspace.txt",
    });
    store.getState().openTab({
      ref: checkoutExplorerRef({ scope: "repo", target: "loomcli" }),
      path: "repo.txt",
    });

    const reloaded = createFileBrowserStore({ workspaceId: "ws-1" });

    expect(reloaded.getState().groups[0]?.tabs).toEqual([
      {
        ref: checkoutExplorerRef({ scope: "workspace" }),
        path: "workspace.txt",
      },
      {
        ref: checkoutExplorerRef({ scope: "repo", target: "loomcli" }),
        path: "repo.txt",
      },
    ]);
  });

  it("keeps agent-scoped stores on distinct v3 storage keys", () => {
    const atlasKey = agentFileBrowserTabsStorageKey("atlas");
    const novaKey = agentFileBrowserTabsStorageKey("nova");
    const atlas = createFileBrowserStore({
      workspaceId: "ws-1",
      storageKey: atlasKey,
    });

    atlas.getState().openTab({
      ref: checkoutExplorerRef({
        scope: "agent",
        target: "atlas",
        repo: "loomcli",
      }),
      path: "README.md",
    });
    const nova = createFileBrowserStore({
      workspaceId: "ws-1",
      storageKey: novaKey,
    });

    expect(nova.getState().groups[0]?.tabs).toEqual([]);
    expect(localStorage.getItem(wsKey("ws-1", atlasKey))).toContain(
      "README.md",
    );
    expect(
      localStorage.getItem(wsKey("ws-1", fileBrowserTabsStorageKey())),
    ).toBeNull();
  });

  it("retargets and closes tabs for one checkout only", () => {
    const store = createFileBrowserStore({ workspaceId: "ws-1" });
    const atlas = checkoutExplorerRef({
      scope: "agent",
      target: "atlas",
      repo: "app",
    });
    const repo = checkoutExplorerRef({ scope: "repo", target: "app" });

    store.getState().openTab({ ref: atlas, path: "src/old/a.ts" });
    store.getState().openTab({ ref: repo, path: "src/old/a.ts" });
    const atlasKey = tabIdentityKey({ ref: atlas, path: "src/old/a.ts" });
    store.getState().setDirty(atlasKey, true);

    store.getState().retargetPathPrefix(atlas, "src/old", "src/new");

    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: atlas, path: "src/new/a.ts" },
      { ref: repo, path: "src/old/a.ts" },
    ]);
    expect(
      store.getState().dirty[
        tabIdentityKey({ ref: atlas, path: "src/new/a.ts" })
      ],
    ).toBe(true);

    store.getState().closePathPrefix(atlas, "src/new");

    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: repo, path: "src/old/a.ts" },
    ]);
  });

  it("keeps skills tabs when pruning to the available skills roots", () => {
    const store = createFileBrowserStore({ workspaceId: "ws-1" });
    const skills = skillsExplorerRef({ kind: "role", role: "reviewer" });
    const checkout = checkoutExplorerRef({ scope: "workspace" });
    store.getState().openTab({ ref: checkout, path: "main.ts" });
    store.getState().openTab({ ref: skills, path: "audit/SKILL.md" });

    store.getState().pruneUnavailableRefs([skills], "ws-1");

    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: skills, path: "audit/SKILL.md" },
    ]);
  });

  it("keeps a tab with unsaved edits when its ref goes missing", () => {
    const store = createFileBrowserStore({ workspaceId: "ws-1" });
    const workspace = checkoutExplorerRef({ scope: "workspace" });
    const gone = checkoutExplorerRef({ scope: "repo", target: "app" });
    store.getState().openTab({ ref: workspace, path: "main.ts" });
    store.getState().openTab({ ref: gone, path: "clean.ts" });
    store.getState().openTab({ ref: gone, path: "draft.ts" });
    const draftKey = tabIdentityKey({ ref: gone, path: "draft.ts" });
    store.getState().setDirty(draftKey, true);

    store.getState().pruneUnavailableRefs([workspace], "ws-1");

    // The clean tab on the same missing ref still closes — retaining the dirty
    // one must not amount to disabling pruning.
    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: workspace, path: "main.ts" },
      { ref: gone, path: "draft.ts" },
    ]);
    expect(store.getState().dirty[draftKey]).toBe(true);
    expect(store.getState().mru).toContainEqual({
      ref: gone,
      path: "draft.ts",
    });

    // Pruning persists, so the retained draft has to survive a reload too.
    const reloaded = createFileBrowserStore({ workspaceId: "ws-1" });
    expect(reloaded.getState().groups[0]?.tabs).toContainEqual({
      ref: gone,
      path: "draft.ts",
    });

    // Once the edits are saved the tab is ordinary again and the next prune
    // closes it.
    store.getState().setDirty(draftKey, false);
    store.getState().pruneUnavailableRefs([workspace], "ws-1");
    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: workspace, path: "main.ts" },
    ]);
  });

  it("re-homes a flattened agent tab only when one checkout can claim it", () => {
    const flattened = checkoutExplorerRef({ scope: "agent", target: "atlas" });
    const api = checkoutExplorerRef({
      scope: "agent",
      target: "atlas",
      repo: "api",
    });
    const web = checkoutExplorerRef({
      scope: "agent",
      target: "atlas",
      repo: "web",
    });

    const ambiguous = createFileBrowserStore({ workspaceId: "ws-1" });
    ambiguous.getState().openTab({ ref: flattened, path: "README.md" });
    ambiguous.getState().pruneUnavailableRefs([api, web], "ws-1");

    // "api" and "web" both have a README.md and only one of them is the file
    // this tab was opened on, so the tab closes rather than silently showing
    // whichever checkout happens to be listed first.
    expect(ambiguous.getState().groups[0]?.tabs).toEqual([]);

    localStorage.clear();
    const unambiguous = createFileBrowserStore({ workspaceId: "ws-2" });
    unambiguous.getState().openTab({ ref: flattened, path: "README.md" });
    unambiguous.getState().pruneUnavailableRefs([api], "ws-2");

    expect(unambiguous.getState().groups[0]?.tabs).toEqual([
      { ref: api, path: "README.md" },
    ]);
  });

  it("ignores a valid-ref universe belonging to another workspace", () => {
    const store = createFileBrowserStore({ workspaceId: "ws-b" });
    const nova = checkoutExplorerRef({
      scope: "agent",
      target: "nova",
      repo: "svc",
    });
    const atlas = checkoutExplorerRef({
      scope: "agent",
      target: "atlas",
      repo: "loomcli",
    });
    store.getState().openTab({ ref: nova, path: "main.ts" });

    // Mid-switch: the agent list still describes ws-a, which has no nova.
    store.getState().pruneUnavailableRefs([atlas], "ws-a");
    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: nova, path: "main.ts" },
    ]);

    // A caller that cannot say which workspace the refs came from is treated
    // the same way: not known yet, so nothing is closed.
    store.getState().pruneUnavailableRefs([atlas], undefined);
    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: nova, path: "main.ts" },
    ]);

    // ws-b's own universe arrives and pruning resumes normally.
    store.getState().pruneUnavailableRefs([atlas], "ws-b");
    expect(store.getState().groups[0]?.tabs).toEqual([]);
  });
});
