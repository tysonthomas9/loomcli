/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it } from "vitest";

import {
  createFileBrowserStore,
  fileBrowserTabsStorageKey,
} from "../fileBrowserStore";
import { wsKey } from "@/utils/scopedStorage";

describe("fileBrowserStore", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("migrates legacy v1 workspace tabs into v2 schema", () => {
    localStorage.setItem(
      wsKey("ws-1", "file-browser-tabs"),
      JSON.stringify({ openTabs: ["a.ts", "b.ts"], activePath: "b.ts" }),
    );

    const store = createFileBrowserStore({
      workspaceId: "ws-1",
      scopeRef: { scope: "workspace" },
    });

    expect(store.getState().groups).toEqual([
      {
        tabs: [{ path: "a.ts" }, { path: "b.ts" }],
        active: "b.ts",
      },
    ]);
    expect(store.getState().mru[0]).toBe("b.ts");

    const migrated = localStorage.getItem(
      wsKey("ws-1", fileBrowserTabsStorageKey({ scope: "workspace" })),
    );
    expect(JSON.parse(migrated ?? "{}")).toMatchObject({ v: 2 });
  });

  it("persists separate tab sets per scope and target", () => {
    const workspaceStore = createFileBrowserStore({
      workspaceId: "ws-1",
      scopeRef: { scope: "workspace" },
    });
    workspaceStore.getState().openTab("workspace.txt");

    const repoStore = createFileBrowserStore({
      workspaceId: "ws-1",
      scopeRef: { scope: "repo", target: "loomcli" },
    });
    repoStore.getState().openTab("repo.txt");

    const reloadedWorkspace = createFileBrowserStore({
      workspaceId: "ws-1",
      scopeRef: { scope: "workspace" },
    });
    const reloadedRepo = createFileBrowserStore({
      workspaceId: "ws-1",
      scopeRef: { scope: "repo", target: "loomcli" },
    });

    expect(reloadedWorkspace.getState().groups[0]?.active).toBe(
      "workspace.txt",
    );
    expect(reloadedRepo.getState().groups[0]?.active).toBe("repo.txt");
  });

  it("opens split right and collapses when group 2 closes its last tab", () => {
    const store = createFileBrowserStore({
      workspaceId: "ws-1",
      scopeRef: { scope: "workspace" },
    });

    store.getState().openTab("a.ts");
    store.getState().splitRight();

    expect(store.getState().groups).toHaveLength(2);
    expect(store.getState().groups[1]?.active).toBe("a.ts");
    expect(store.getState().activeGroup).toBe(1);

    store.getState().closeTab(1, "a.ts");

    expect(store.getState().groups).toHaveLength(1);
    expect(store.getState().activeGroup).toBe(0);
  });

  it("retargets and closes tabs for CRUD path operations", () => {
    const store = createFileBrowserStore({
      workspaceId: "ws-1",
      scopeRef: { scope: "agent", target: "atlas" },
    });

    store.getState().openTab("src/old/a.ts");
    store.getState().openTab("src/old/b.ts");
    store.getState().setDirty("src/old/a.ts", true);

    store.getState().retargetPathPrefix("src/old", "src/new");

    expect(store.getState().groups[0]?.tabs.map((tab) => tab.path)).toEqual([
      "src/new/a.ts",
      "src/new/b.ts",
    ]);
    expect(store.getState().dirty["src/new/a.ts"]).toBe(true);
    expect(store.getState().mru).toContain("src/new/b.ts");

    store.getState().closePathPrefix("src/new");

    expect(store.getState().groups[0]?.tabs).toEqual([]);
    expect(store.getState().dirty).toEqual({});
  });
});
