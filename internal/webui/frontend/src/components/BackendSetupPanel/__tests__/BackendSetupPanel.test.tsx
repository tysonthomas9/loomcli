/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { BackendHealthData } from "@/api/workspace";
import { useBackendsSetup } from "@/hooks/workspace/useBackendsSetup";

import { BackendSetupPanel } from "../BackendSetupPanel";

vi.mock("@/hooks/workspace/useBackendsSetup", () => ({
  useBackendsSetup: vi.fn(),
}));

const mockHook = vi.mocked(useBackendsSetup);

function makeBackend(
  overrides?: Partial<BackendHealthData>,
): BackendHealthData {
  return {
    name: "claude",
    display_name: "Claude",
    available: true,
    installed: true,
    api_key_set: true,
    authenticated: true,
    ready: true,
    description: "Anthropic Claude — code reasoning, refactoring, and pair-programming.",
    install_actions: [
      {
        id: "npm-global",
        label: "Install Claude CLI with npm",
        command: "npm install -g @anthropic-ai/claude-code",
        interactive: false,
      },
    ],
    login_actions: [
      {
        id: "claude-login",
        label: "Run claude login",
        command: "claude login",
        interactive: true,
      },
    ],
    env_vars: [{ name: "ANTHROPIC_API_KEY", restart_required: true }],
    ...overrides,
  };
}

describe("BackendSetupPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockHook.mockReturnValue({
      backends: [makeBackend()],
      isLoading: false,
      error: null,
      refresh: vi.fn(async () => []),
    });
  });

  it("renders the panel with backend name and badges", () => {
    render(<BackendSetupPanel />);
    expect(screen.getByTestId("backend-setup-panel")).toBeInTheDocument();
    expect(screen.getByTestId("backend-row-claude")).toBeInTheDocument();
    expect(screen.getByText("Claude")).toBeInTheDocument();
    // Three status badges visible.
    expect(screen.getByText(/installed/i)).toBeInTheDocument();
    expect(screen.getByText(/authenticated/i)).toBeInTheDocument();
    expect(screen.getByText(/ready/i)).toBeInTheDocument();
  });

  it("hides setup detail by default and shows it on toggle", () => {
    render(<BackendSetupPanel />);
    expect(
      screen.queryByTestId("backend-install-claude-npm-global"),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("backend-toggle-claude"));
    expect(
      screen.getByTestId("backend-install-claude-npm-global"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("backend-login-claude-claude-login"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("backend-envvar-claude-ANTHROPIC_API_KEY"),
    ).toBeInTheDocument();
  });

  it("shows the install command and supports copy-to-clipboard", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });

    render(<BackendSetupPanel />);
    fireEvent.click(screen.getByTestId("backend-toggle-claude"));

    const cmdRow = screen.getByTestId("backend-install-claude-npm-global");
    expect(cmdRow).toHaveTextContent("npm install -g @anthropic-ai/claude-code");

    const copyButton = cmdRow.querySelector("button")!;
    fireEvent.click(copyButton);
    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith(
        "npm install -g @anthropic-ai/claude-code",
      ),
    );
  });

  it("calls the refresh function when Refresh status is clicked", async () => {
    const refresh = vi.fn(async () => []);
    mockHook.mockReturnValue({
      backends: [makeBackend()],
      isLoading: false,
      error: null,
      refresh,
    });
    render(<BackendSetupPanel />);
    fireEvent.click(screen.getByTestId("backend-refresh-claude"));
    await waitFor(() => expect(refresh).toHaveBeenCalled());
  });

  it("renders unauthenticated backend with off badges", () => {
    mockHook.mockReturnValue({
      backends: [
        makeBackend({
          installed: true,
          authenticated: false,
          ready: false,
          api_key_set: false,
        }),
      ],
      isLoading: false,
      error: null,
      refresh: vi.fn(async () => []),
    });
    render(<BackendSetupPanel />);
    const row = screen.getByTestId("backend-row-claude");
    expect(row.getAttribute("data-ready")).toBe("false");
    // Installed badge should be on, authenticated/ready off.
    const installedBadge = screen.getByText(/installed/i).closest("span");
    expect(installedBadge?.getAttribute("data-on")).toBe("true");
    const authBadge = screen.getByText(/authenticated/i).closest("span");
    expect(authBadge?.getAttribute("data-on")).toBe("false");
  });

  it("falls back to api_key_set when authenticated is undefined", () => {
    mockHook.mockReturnValue({
      backends: [
        // Older response shape with no `authenticated` field.
        {
          name: "mystery",
          display_name: "Mystery",
          available: true,
          installed: true,
          api_key_set: true,
        },
      ],
      isLoading: false,
      error: null,
      refresh: vi.fn(async () => []),
    });
    render(<BackendSetupPanel />);
    const row = screen.getByTestId("backend-row-mystery");
    // ready falls back to installed && api_key_set.
    expect(row.getAttribute("data-ready")).toBe("true");
  });

  it("shows the loading subtitle on first paint", () => {
    mockHook.mockReturnValue({
      backends: [],
      isLoading: true,
      error: null,
      refresh: vi.fn(async () => []),
    });
    render(<BackendSetupPanel />);
    expect(screen.getByText(/loading backend status/i)).toBeInTheDocument();
  });

  it("surfaces store errors", () => {
    mockHook.mockReturnValue({
      backends: [makeBackend()],
      isLoading: false,
      error: "network down",
      refresh: vi.fn(async () => []),
    });
    render(<BackendSetupPanel />);
    expect(screen.getByText("network down")).toBeInTheDocument();
  });
});
