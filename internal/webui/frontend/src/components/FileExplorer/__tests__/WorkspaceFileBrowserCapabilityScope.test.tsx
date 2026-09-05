/**
 * @vitest-environment jsdom
 */

/**
 * Capability scoping, tested against the real providers.
 *
 * The sibling WorkspaceFileBrowser.test.tsx replaces @/hooks wholesale: its
 * FileCapabilitiesProvider is a pass-through and its skills hooks are literals,
 * so nothing there can observe a request being issued — which is exactly why
 * the Skills section fetching /files/capabilities, and the Files section
 * fetching the skills catalog, stayed invisible. This file keeps the real
 * capability provider and the real skills store, stubs only the workspace
 * context, and watches fetch.
 */

import "@testing-library/jest-dom";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { CapabilityNotices } from "../overlays";
import { WorkspaceFileBrowser } from "../index";

const mocks = vi.hoisted(() => ({ workspaceId: "ws-scope-0" }));

vi.mock("@/components/CodeMirrorEditor", () => ({
  CodeMirrorEditor: () => null,
}));

vi.mock("@/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks")>();
  return {
    ...actual,
    useWorkspaceContext: () => ({
      workspaceId: mocks.workspaceId,
      workspace: { id: mocks.workspaceId },
      repos: [
        {
          name: "loomcli",
          path: "/tmp/loomcli",
          default_branch: "main",
          remote: "origin",
          groups: [],
        },
      ],
      agents: [
        {
          name: "atlas",
          repos: ["loomcli"],
          repo_groups: [],
          cross_repo: false,
        },
      ],
    }),
  };
});

const requested: string[] = [];
let workspaceCounter = 0;

function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    statusText: "OK",
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response;
}

function requestsMatching(fragment: string): string[] {
  return requested.filter((url) => url.includes(fragment));
}

describe("WorkspaceFileBrowser capability scoping", () => {
  beforeEach(() => {
    localStorage.clear();
    requested.length = 0;
    // The skills store is a module-level singleton keyed by workspace, so each
    // test needs its own workspace or the second one reads the first's cache.
    workspaceCounter += 1;
    mocks.workspaceId = `ws-scope-${workspaceCounter}`;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url =
          typeof input === "string"
            ? input
            : input instanceof URL
              ? input.href
              : input.url;
        requested.push(url);
        if (url.includes("/files/checkouts")) {
          return Promise.resolve(jsonResponse({ checkouts: [] }));
        }
        if (url.includes("/files/capabilities")) {
          return Promise.resolve(
            jsonResponse({ read: true, write: true, sensitive: false }),
          );
        }
        if (url.includes("/files/git-status")) {
          return Promise.resolve(
            jsonResponse({
              status: {},
              partial: false,
              limit_hit: false,
              errors: [],
            }),
          );
        }
        if (url.includes("/skill-capabilities")) {
          return Promise.resolve(
            jsonResponse({
              can_edit_role_scope: true,
              workspace_scope: "read_only",
            }),
          );
        }
        if (url.includes("/skills")) {
          return Promise.resolve(jsonResponse({ groups: [] }));
        }
        return Promise.resolve(jsonResponse({}));
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("asks for skills data and no file capabilities in the Skills section", async () => {
    render(<WorkspaceFileBrowser mode="skills" />);

    await waitFor(() =>
      expect(requestsMatching("/skill-capabilities")).not.toHaveLength(0),
    );
    await waitFor(() =>
      expect(requestsMatching("/skills")).not.toHaveLength(0),
    );

    // Editing here is gated by skill capabilities alone. Fetching file
    // capabilities would only buy a notice claiming editing is disabled.
    expect(requestsMatching("/files/capabilities")).toEqual([]);
    expect(screen.queryByText(/file permissions/i)).toBeNull();
  });

  it("asks for file capabilities and no skills data in the Files section", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);

    await waitFor(() =>
      expect(requestsMatching("/files/capabilities")).not.toHaveLength(0),
    );
    await waitFor(() =>
      expect(requestsMatching("/files/checkouts")).not.toHaveLength(0),
    );

    expect(requestsMatching("/skills")).toEqual([]);
    expect(requestsMatching("/skill-capabilities")).toEqual([]);
    expect(screen.queryByText(/skill permissions/i)).toBeNull();
  });

  it("asks for no skills data in an agent's files", async () => {
    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    await waitFor(() =>
      expect(requestsMatching("/files/capabilities")).not.toHaveLength(0),
    );

    expect(requestsMatching("/skills")).toEqual([]);
    expect(requestsMatching("/skill-capabilities")).toEqual([]);
    expect(screen.queryByText(/skill permissions/i)).toBeNull();
  });

  it("renders only the notices a section is actually gated by", async () => {
    const { unmount } = render(
      <CapabilityNotices
        workspaceId={mocks.workspaceId}
        capabilities={{ checkouts: false, skills: true }}
        filesLoading={false}
        filesError="capabilities request failed"
        retryFiles={() => {}}
      />,
    );

    // A failing /files/capabilities must not tell a skills-only section that
    // editing is disabled — there, editing never depended on it.
    expect(screen.queryByText(/File permissions unavailable/)).toBeNull();
    // It did load the skill capabilities it is gated by; the checkout-shaped
    // section below must load neither those nor a notice about them.
    await waitFor(() =>
      expect(requestsMatching("/skill-capabilities")).not.toHaveLength(0),
    );
    unmount();
    requested.length = 0;

    render(
      <CapabilityNotices
        workspaceId={mocks.workspaceId}
        capabilities={{ checkouts: true, skills: false }}
        filesLoading={false}
        filesError="capabilities request failed"
        retryFiles={() => {}}
      />,
    );

    expect(
      screen.getByText(/File permissions unavailable/),
    ).toBeInTheDocument();
    expect(requestsMatching("/skill-capabilities")).toEqual([]);
  });
});
