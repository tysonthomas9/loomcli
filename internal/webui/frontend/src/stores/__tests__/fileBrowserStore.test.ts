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
import {
  legacyScopeStorageKey,
  tabIdentityKey,
} from "@/utils/fileExplorerRefs";

describe("fileBrowserStore", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("migrates legacy v1 workspace tabs into v3 schema", () => {
    localStorage.setItem(
      wsKey("ws-1", "file-browser-tabs"),
      JSON.stringify({ openTabs: ["a.ts", "b.ts"], activePath: "b.ts" }),
    );

    const store = createFileBrowserStore({ workspaceId: "ws-1" });
    const workspaceB = {
      ref: { scope: "workspace" as const },
      path: "b.ts",
    };

    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: { scope: "workspace" }, path: "a.ts" },
      { ref: { scope: "workspace" }, path: "b.ts" },
    ]);
    expect(store.getState().groups[0]?.active).toBe(tabIdentityKey(workspaceB));

    const migrated = localStorage.getItem(
      wsKey("ws-1", fileBrowserTabsStorageKey()),
    );
    expect(JSON.parse(migrated ?? "{}")).toMatchObject({ v: 3 });
  });

  it("folds v2 scoped stores into one workspace-keyed v3 store", () => {
    localStorage.setItem(
      wsKey("ws-1", legacyScopeStorageKey({ scope: "workspace" })),
      JSON.stringify({
        v: 2,
        groups: [
          { tabs: [{ path: "workspace.txt" }], active: "workspace.txt" },
        ],
        mru: ["workspace.txt"],
      }),
    );
    localStorage.setItem(
      wsKey(
        "ws-1",
        legacyScopeStorageKey({ scope: "repo", target: "loomcli" }),
      ),
      JSON.stringify({
        v: 2,
        groups: [{ tabs: [{ path: "repo.txt" }], active: "repo.txt" }],
        mru: ["repo.txt"],
      }),
    );

    const store = createFileBrowserStore({ workspaceId: "ws-1" });

    expect(store.getState().groups[0]?.tabs).toEqual([
      { ref: { scope: "workspace" }, path: "workspace.txt" },
      { ref: { scope: "repo", target: "loomcli" }, path: "repo.txt" },
    ]);
    expect(
      localStorage.getItem(wsKey("ws-1", fileBrowserTabsStorageKey())),
    ).toContain('"v":3');
  });

  it("falls back to empty state on malformed persisted state", () => {
    localStorage.setItem(wsKey("ws-1", fileBrowserTabsStorageKey()), "{bad");

    const store = createFileBrowserStore({ workspaceId: "ws-1" });

    expect(store.getState().groups).toEqual([{ tabs: [], active: null }]);
    expect(store.getState().mru).toEqual([]);
  });

  it("persists tabs from multiple checkouts in the same workspace store", () => {
    const store = createFileBrowserStore({ workspaceId: "ws-1" });

    store
      .getState()
      .openTab({ ref: { scope: "workspace" }, path: "workspace.txt" });
    store
      .getState()
      .openTab({ ref: { scope: "repo", target: "loomcli" }, path: "repo.txt" });

    const reloaded = createFileBrowserStore({ workspaceId: "ws-1" });

    expect(reloaded.getState().groups[0]?.tabs).toEqual([
      { ref: { scope: "workspace" }, path: "workspace.txt" },
      { ref: { scope: "repo", target: "loomcli" }, path: "repo.txt" },
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
      ref: { scope: "agent", target: "atlas", repo: "loomcli" },
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
    const atlas = { scope: "agent" as const, target: "atlas", repo: "app" };
    const repo = { scope: "repo" as const, target: "app" };

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

  it("coerces legacy agent tabs to an existing multi-repo checkout before pruning", () => {
    localStorage.setItem(
      wsKey("ws-1", legacyScopeStorageKey({ scope: "agent", target: "atlas" })),
      JSON.stringify({
        v: 2,
        groups: [{ tabs: [{ path: "README.md" }], active: "README.md" }],
        mru: ["README.md"],
      }),
    );
    const store = createFileBrowserStore({ workspaceId: "ws-1" });

    store
      .getState()
      .pruneUnavailableRefs([
        { scope: "workspace" },
        { scope: "agent", target: "atlas", repo: "app" },
      ]);

    expect(store.getState().groups[0]?.tabs).toEqual([
      {
        ref: { scope: "agent", target: "atlas", repo: "app" },
        path: "README.md",
      },
    ]);
  });
});
