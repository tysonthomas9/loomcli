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

    store.getState().pruneUnavailableRefs([skills]);

    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: skills, path: "audit/SKILL.md" },
    ]);
  });
});
