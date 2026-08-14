/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/common";
import {
  ensureAgentTerminalSession,
  getTabMetadata,
  listSessionsByIssue,
  patchTabMetadata,
  putTabMetadata,
  startTerminalSetup,
} from "../terminal";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    api: {
      GET: vi.fn(),
      POST: vi.fn(),
      PATCH: vi.fn(),
      PUT: vi.fn(),
      DELETE: vi.fn(),
      use: vi.fn(),
    },
  };
});

const mockApiGet = vi.mocked(api.GET);
const mockApiPost = vi.mocked(api.POST);
const mockApiPatch = vi.mocked(api.PATCH);
const mockApiPut = vi.mocked(api.PUT);

describe("terminal tab metadata API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("throws ApiError instead of TypeError when get metadata returns no envelope", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    } as never);

    await expect(getTabMetadata("default", "lead-1")).rejects.toMatchObject({
      name: "ApiError",
      status: 200,
      statusText: "OK",
      body: "missing response envelope",
    });
  });

  it("throws ApiError instead of TypeError when patch metadata returns no envelope", async () => {
    mockApiPatch.mockResolvedValueOnce({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    } as never);

    await expect(
      patchTabMetadata("default", "lead-1", { label: "Lead" }),
    ).rejects.toMatchObject({
      status: 200,
      statusText: "OK",
      body: "missing response envelope",
    });
  });

  it("sends exactly the generated tab creation contract", async () => {
    mockApiPut.mockResolvedValueOnce({
      data: { success: true },
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    } as never);

    await putTabMetadata("default", "lead-shell-1", {
      backend: "shell",
      label: "lead-shell-1",
      notes: "",
      pinned: false,
      sort_order: 0,
    });

    expect(mockApiPut).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/terminal/tabs/{session}",
      {
        params: {
          path: { ws: "default", session: "lead-shell-1" },
        },
        body: {
          backend: "shell",
          label: "lead-shell-1",
          notes: "",
          pinned: false,
          sort_order: 0,
        },
      },
    );
  });

  it("resolves an agent terminal through the generated session operation", async () => {
    mockApiPost.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          session_name: "term_reviewer",
          label: "Reviewer",
          notes: "",
          sort_order: 0,
          pinned: false,
          created_at: "2026-08-13T00:00:00Z",
          updated_at: "2026-08-13T00:00:00Z",
          pty_alive: false,
          attached_clients: 0,
        },
      },
      error: undefined,
      response: new Response(),
    } as never);

    await expect(
      ensureAgentTerminalSession("TEST", "reviewer.v2"),
    ).resolves.toMatchObject({ session_name: "term_reviewer" });
    expect(mockApiPost).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/agents/{name}/terminal/session",
      { params: { path: { ws: "TEST", name: "reviewer.v2" } } },
    );
  });

  it("starts setup through the generated allowlisted operation", async () => {
    mockApiPost.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          session_name: "TEST--lead-shell-setup-codex",
          label: "Codex Setup",
          backend: "codex",
          action: "test",
          command: "codex --version",
          title: "Test Codex",
          message: "Run the Codex version check.",
          manual: false,
          created: true,
        },
      },
      error: undefined,
      response: new Response(),
    } as never);

    await expect(
      startTerminalSetup("TEST", "codex", "test"),
    ).resolves.toMatchObject({
      backend: "codex",
      action: "test",
    });
    expect(mockApiPost).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/terminal/setup",
      {
        params: { path: { ws: "TEST" } },
        body: { backend: "codex", action: "test" },
      },
    );
  });

  it("lists issue sessions through the generated response envelope", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: { "TEST-1": ["term_reviewer"] } },
      error: undefined,
      response: new Response(),
    } as never);

    await expect(listSessionsByIssue("TEST")).resolves.toEqual({
      "TEST-1": ["term_reviewer"],
    });
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/terminal/sessions/by-issue",
      { params: { path: { ws: "TEST" }, query: {} } },
    );
  });
});
