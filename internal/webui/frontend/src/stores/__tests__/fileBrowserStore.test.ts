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
import { tabIdentityKey } from "@/utils/fileExplorerRefs";

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
});
