import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  getSkillCapabilities,
  getSkillFile,
  listSkills,
  putSkillFile,
  type SkillCatalogGroup,
  type SkillsCatalogResponse,
} from "@/api/workspace";
import { ApiError } from "@/types/common";
import { skillsExplorerRef } from "@/utils/explorerRefs";
import { SkillsStore, synthesizeSkillDirectory } from "../skillsStore";

vi.mock("@/api/workspace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/workspace")>();
  return {
    ...actual,
    getSkillCapabilities: vi.fn(),
    getSkillFile: vi.fn(),
    listSkills: vi.fn(),
    putSkillFile: vi.fn(),
  };
});

const mockCapabilities = vi.mocked(getSkillCapabilities);
const mockGetFile = vi.mocked(getSkillFile);
const mockList = vi.mocked(listSkills);
const mockPutFile = vi.mocked(putSkillFile);

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => {
    resolve = onResolve;
    reject = onReject;
  });
  return { promise, resolve, reject };
}

function groups(): SkillCatalogGroup[] {
  const base = {
    description: "Audit changes",
    created_at: "2026-08-14T00:00:00Z",
    updated_at: "2026-08-14T00:00:00Z",
    files: [
      { path: "z.txt", revision: "z1", executable: false },
      { path: "scripts/run.sh", revision: "s1", executable: true },
      { path: "scripts/lib/helper.sh", revision: "h1", executable: true },
    ],
  };
  return [
    {
      scope: "workspace",
      skills: [
        {
          ...base,
          name: "audit",
          scope: "workspace",
          content_revision: "w1",
        },
      ],
    },
    {
      scope: "role",
      role: "reviewer",
      skills: [
        {
          ...base,
          name: "audit",
          scope: "role",
          role: "reviewer",
          content_revision: "r1",
        },
      ],
    },
  ];
}

describe("skillsStore", () => {
  beforeEach(() => vi.resetAllMocks());

  it("synthesizes sorted directory levels without duplicating SKILL.md", () => {
    const catalog = groups();
    catalog[0]!.skills[0]!.files.push({
      path: "skill.md",
      revision: "legacy",
      executable: false,
    });
    expect(
      synthesizeSkillDirectory(catalog, { kind: "workspace" }, "").map(
        (entry) => [entry.name, entry.is_dir],
      ),
    ).toEqual([["audit", true]]);
    expect(
      synthesizeSkillDirectory(catalog, { kind: "workspace" }, "audit").map(
        (entry) => [entry.name, entry.is_dir],
      ),
    ).toEqual([
      ["scripts", true],
      ["SKILL.md", false],
      ["z.txt", false],
    ]);
    expect(
      synthesizeSkillDirectory(
        catalog,
        { kind: "workspace" },
        "audit/scripts",
      ).map((entry) => [entry.name, entry.is_dir]),
    ).toEqual([
      ["lib", true],
      ["run.sh", false],
    ]);
  });

  it("computes shadowing once across workspace and role groups", async () => {
    mockList.mockResolvedValue({ groups: groups() });
    const store = new SkillsStore();
    await store.loadCatalog("ws-1");

    const snapshot = store.catalog("ws-1");
    expect(snapshot.shadowedByRef["skills:workspace:"]).toEqual(
      new Set(["audit"]),
    );
    expect(snapshot.shadowsByRef["skills:role:reviewer:"]).toEqual(
      new Set(["audit"]),
    );
  });

  it("preserves an authoritative 403 that lands during a catalog load", async () => {
    const catalog = deferred<SkillsCatalogResponse>();
    mockList.mockReturnValue(catalog.promise);
    mockPutFile.mockRejectedValue(
      new ApiError(403, "Forbidden", {
        code: "workspace_scope_readonly",
        error: "scope denied",
      }),
    );
    const store = new SkillsStore();
    const group = { kind: "role", role: "reviewer" } as const;
    const loading = store.loadCatalog("ws-race");

    await expect(
      store.createFile("ws-race", group, "audit", "new.txt"),
    ).rejects.toThrow("scope denied");
    catalog.resolve({ groups: groups() });
    await loading;

    expect(store.catalog("ws-race").readOnlyRefs).toContain(
      "skills:role:reviewer:",
    );
  });

  it("ignores an older catalog response that resolves after a newer load", async () => {
    const older = deferred<SkillsCatalogResponse>();
    const newer = deferred<SkillsCatalogResponse>();
    mockList
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);
    const store = new SkillsStore();
    const olderLoad = store.loadCatalog("ws-generation");
    store.invalidate("ws-generation");
    const newerLoad = store.loadCatalog("ws-generation");
    const olderGroups = groups();
    olderGroups[0]!.skills[0]!.name = "older";
    const newerGroups = groups();
    newerGroups[0]!.skills[0]!.name = "newer";

    newer.resolve({ groups: newerGroups });
    await newerLoad;
    older.resolve({ groups: olderGroups });
    await olderLoad;

    expect(
      store
        .skills({ kind: "workspace" }, "ws-generation")
        .map((skill) => skill.name),
    ).toEqual(["newer"]);
  });

  it("round-trips the executable bit and the supplied If-Match revision", async () => {
    mockGetFile.mockResolvedValue({
      path: "scripts/run.sh",
      content: "echo old",
      executable: true,
      revision: "file-v1",
      skill_ref: "role:reviewer:audit",
    });
    mockPutFile.mockResolvedValue({
      path: "scripts/run.sh",
      content: "echo new",
      executable: true,
      revision: "file-v2",
      skill_ref: "role:reviewer:audit",
    });
    const store = new SkillsStore();
    const transport = store.documentTransport();
    const ref = {
      workspaceId: "ws-1",
      ref: skillsExplorerRef({ kind: "role", role: "reviewer" }),
      path: "audit/scripts/run.sh",
    };
    await transport.read(ref, new AbortController().signal);
    await transport.write(
      ref,
      "echo new",
      new AbortController().signal,
      "file-v1",
    );

    expect(mockPutFile).toHaveBeenCalledWith(
      "ws-1",
      { kind: "role", role: "reviewer" },
      "audit",
      "scripts/run.sh",
      { content: "echo new", executable: true },
      { ifMatch: "file-v1" },
      { signal: expect.any(AbortSignal) },
    );
  });

  it("serializes writes to sibling documents in one skill", async () => {
    let releaseFirst!: () => void;
    mockPutFile
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            releaseFirst = () =>
              resolve({
                path: "SKILL.md",
                content: "first",
                executable: false,
                revision: "v2",
                skill_ref: "role:reviewer:audit",
              });
          }),
      )
      .mockResolvedValueOnce({
        path: "notes.md",
        content: "second",
        executable: false,
        revision: "v3",
        skill_ref: "role:reviewer:audit",
      });
    const store = new SkillsStore();
    const transport = store.documentTransport();
    const ref = skillsExplorerRef({ kind: "role", role: "reviewer" });
    const first = transport.write(
      { workspaceId: "ws-1", ref, path: "audit/SKILL.md" },
      "first",
      new AbortController().signal,
      "v1",
    );
    const second = transport.write(
      { workspaceId: "ws-1", ref, path: "audit/notes.md" },
      "second",
      new AbortController().signal,
      "n1",
    );

    await vi.waitFor(() => expect(mockPutFile).toHaveBeenCalledTimes(1));
    releaseFirst();
    await first;
    await second;
    expect(mockPutFile).toHaveBeenCalledTimes(2);
    expect(mockPutFile.mock.calls[0]?.[4]).toEqual({
      content: "first",
      executable: false,
    });
    expect(mockPutFile.mock.calls[1]?.[4]).toEqual({
      content: "second",
      executable: false,
    });
  });

  it("marks a role scope read-only after an authoritative 403", async () => {
    mockCapabilities.mockResolvedValue({
      can_edit_role_scope: true,
      workspace_scope: "read_only",
    });
    mockList.mockResolvedValue({ groups: groups() });
    mockPutFile.mockRejectedValue(
      new ApiError(403, "Forbidden", {
        code: "workspace_scope_readonly",
        error: "scope denied",
      }),
    );
    const store = new SkillsStore();
    const group = { kind: "role", role: "reviewer" } as const;
    await Promise.all([
      store.loadCatalog("ws-1"),
      store.loadCapabilities("ws-1"),
    ]);
    expect(store.canEdit("ws-1", group)).toBe(true);

    await expect(
      store.createFile("ws-1", group, "audit", "new.txt"),
    ).rejects.toThrow("scope denied");
    expect(store.canEdit("ws-1", group)).toBe(false);
  });

  it("surfaces provenance owner metadata as a non-retryable write error", async () => {
    mockGetFile.mockResolvedValue({
      path: "SKILL.md",
      content: "old",
      executable: false,
      revision: "v1",
      skill_ref: "role:reviewer:audit",
    });
    mockPutFile.mockRejectedValue(
      new ApiError(409, "Conflict", {
        code: "skill_provenance_conflict",
        error: "the skill is owned by another actor",
        owner: "pack-sync",
        source: "pack:standards",
      }),
    );
    const store = new SkillsStore();
    const transport = store.documentTransport();
    const ref = {
      workspaceId: "ws-1",
      ref: skillsExplorerRef({ kind: "role", role: "reviewer" }),
      path: "audit/SKILL.md",
    };
    await transport.read(ref, new AbortController().signal);

    await expect(
      transport.write(ref, "new", new AbortController().signal, "v1"),
    ).rejects.toThrow(
      "the skill is owned by another actor (owner: pack-sync, source: pack:standards)",
    );
  });
});
