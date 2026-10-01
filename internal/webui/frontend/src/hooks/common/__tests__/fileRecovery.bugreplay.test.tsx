/**
 * @vitest-environment jsdom
 */
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { FileEntry, SkillsCatalogResponse } from "@/api/workspace";

const mocks = vi.hoisted(() => ({
  listSkills: vi.fn(),
}));

vi.mock("@/api/workspace", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/workspace")>()),
  listSkills: mocks.listSkills,
}));

import { SkillsStore } from "@/stores/skillsStore";

import { useScopedFileTreeCore } from "../useScopedFileTree";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

function file(name: string): FileEntry {
  return { name, is_dir: false, size: 1, mod_time: "2026-01-01T00:00:00Z" };
}

function catalog(skill: string): SkillsCatalogResponse {
  return {
    groups: [
      {
        group: "workspace",
        skills: [{ name: skill, files: ["SKILL.md"] }],
      },
    ],
  } as unknown as SkillsCatalogResponse;
}

afterEach(() => {
  mocks.listSkills.mockReset();
});

it("#651 an invalidation is not satisfied by a catalog read that started before it", async () => {
  const preInvalidation = deferred<SkillsCatalogResponse>();
  mocks.listSkills.mockReturnValueOnce(preInvalidation.promise);
  const store = new SkillsStore();

  const staleLoad = store.loadCatalog("ws-replay");
  store.invalidate("ws-replay");
  preInvalidation.resolve(catalog("before-invalidation"));
  await staleLoad;

  const snapshot = store.catalog("ws-replay");
  expect(
    snapshot.status === "loaded" &&
      snapshot.groups.some((group) =>
        group.skills.some((skill) => skill.name === "before-invalidation"),
      ),
    "a read pending before invalidate() was committed as the fresh catalog",
  ).toBe(false);
});

it("#653 a late directory refresh cannot overwrite a newer one", async () => {
  const reads: Array<ReturnType<typeof deferred<FileEntry[]>>> = [];
  const loader = vi.fn((path: string) => {
    if (path === "") return Promise.resolve([{ ...file("src"), is_dir: true }]);
    const read = deferred<FileEntry[]>();
    reads.push(read);
    return read.promise;
  });
  const { result } = renderHook(() =>
    useScopedFileTreeCore(loader, true, true),
  );
  await waitFor(() => expect(result.current.treeData.has("")).toBe(true));

  let older!: Promise<void>;
  let newer!: Promise<void>;
  act(() => {
    older = result.current.loadDir("src");
    newer = result.current.loadDir("src");
  });
  await act(async () => {
    reads[1]!.resolve([file("new.ts")]);
    await newer;
  });
  await act(async () => {
    reads[0]!.resolve([file("old.ts")]);
    await older;
  });

  expect(
    result.current.treeData.get("src")?.map((entry) => entry.name),
    "an older directory response overwrote the newer refresh",
  ).toEqual(["new.ts"]);
});
