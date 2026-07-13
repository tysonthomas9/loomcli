/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SettingsView component.
 *
 * These tests verify rendering of loading, error, and data states,
 * the backend dropdown, save button behavior, and agent override table.
 */

import { render, screen, fireEvent, within, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type {
  UseBackendConfigReturn,
  UseBackendsReturn,
  UseLocalSettingsReturn,
} from "@/hooks/workspace";
import type { UseTerminalFontReturn } from "@/hooks/terminal";
import type { BackendConfigData } from "@/api/common";

import { SettingsView } from "../SettingsView";

// Mock the hooks used by SettingsView
vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useBackendConfig: vi.fn(),
    useBackends: vi.fn(),
    useLocalSettings: vi.fn(),
  };
});

vi.mock("@/hooks/terminal", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/terminal")>(
      "@/hooks/terminal",
    );
  return { ...actual, useTerminalFont: vi.fn() };
});

vi.mock("@/hooks/ui", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/ui")>("@/hooks/ui");
  return {
    ...actual,
    useToast: vi.fn(() => ({
      showToast: vi.fn(),
      toasts: [],
      dismissToast: vi.fn(),
      dismissAll: vi.fn(),
    })),
  };
});

import {
  useBackendConfig,
  useBackends,
  useLocalSettings,
} from "@/hooks/workspace";
import { useTerminalFont } from "@/hooks/terminal";
import { useToast } from "@/hooks/ui";

const mockUseBackendConfig = vi.mocked(useBackendConfig);
const mockUseBackends = vi.mocked(useBackends);
const mockUseLocalSettings = vi.mocked(useLocalSettings);
const mockUseTerminalFont = vi.mocked(useTerminalFont);
const mockUseToast = vi.mocked(useToast);

/**
 * Helper to create a mock BackendConfigData.
 */
function createMockConfig(
  overrides?: Partial<BackendConfigData>,
): BackendConfigData {
  return {
    backend: "anthropic",
    source: "fleetdb",
    available: ["anthropic", "openai", "local"],
    agents: [],
    ...overrides,
  };
}

/**
 * Helper to create a mock UseBackendConfigReturn.
 */
function createMockHookReturn(
  overrides?: Partial<UseBackendConfigReturn>,
): UseBackendConfigReturn {
  return {
    config: createMockConfig(),
    isLoading: false,
    error: null,
    isSaving: false,
    isCached: false,
    updateBackend: vi.fn().mockResolvedValue(undefined),
    refetch: vi.fn(),
    ...overrides,
  };
}

function createMockBackendsReturn(
  overrides?: Partial<UseBackendsReturn>,
): UseBackendsReturn {
  return {
    backends: [
      {
        name: "anthropic",
        displayName: "Anthropic",
        provider: "Anthropic",
        brandColor: "#d4a574",
        available: true,
        installed: true,
        apiKeySet: true,
      },
      {
        name: "openai",
        displayName: "OpenAI",
        provider: "OpenAI",
        brandColor: "#10a37f",
        available: false,
        installed: false,
        apiKeySet: false,
      },
    ],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
    ...overrides,
  };
}

function createMockLocalSettingsReturn(
  overrides?: Partial<UseLocalSettingsReturn>,
): UseLocalSettingsReturn {
  return {
    settings: {
      version: 1,
      fleetdb_redis: {
        enabled: false,
        db: 0,
        tls: false,
        password_set: false,
      },
      agent_runtime: {
        default: "local",
      },
      local_task_runner: {},
      runtime_credentials: {
        daytona: { configured: false },
        github: { configured: false },
      },
    },
    isLoading: false,
    isSaving: false,
    error: null,
    updateRedis: vi.fn().mockResolvedValue(true),
    updateAgentRuntime: vi.fn().mockResolvedValue(true),
    updateLocalTaskRunner: vi.fn().mockResolvedValue(true),
    updateRuntimeCredentials: vi.fn().mockResolvedValue(true),
    refetch: vi.fn(),
    ...overrides,
  };
}

describe("SettingsView", () => {
  const mockShowToast = vi.fn();

  const mockSetFontFamily = vi.fn();
  const mockSetFontSize = vi.fn();

  function createMockFontReturn(
    overrides?: Partial<UseTerminalFontReturn>,
  ): UseTerminalFontReturn {
    return {
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      fontSize: 14,
      setFontFamily: mockSetFontFamily,
      setFontSize: mockSetFontSize,
      ...overrides,
    };
  }

  beforeEach(() => {
    vi.clearAllMocks();
    mockUseToast.mockReturnValue({
      showToast: mockShowToast,
      toasts: [],
      dismissToast: vi.fn(),
      dismissAll: vi.fn(),
    });
    mockUseBackends.mockReturnValue(createMockBackendsReturn());
    mockUseTerminalFont.mockReturnValue(createMockFontReturn());
    mockUseLocalSettings.mockReturnValue(createMockLocalSettingsReturn());
  });

  describe("loading state", () => {
    it("renders loading skeleton while fetching", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({ config: null, isLoading: true }),
      );

      render(<SettingsView />);

      expect(screen.getByTestId("settings-view")).toBeInTheDocument();
      expect(screen.getByText("Settings")).toBeInTheDocument();
      expect(screen.getByText("Project Default Backend")).toBeInTheDocument();
      // LoadingSkeleton renders with aria-hidden
      const settingsView = screen.getByTestId("settings-view");
      const skeleton = settingsView.querySelector('[aria-hidden="true"]');
      expect(skeleton).toBeInTheDocument();
    });
  });

  describe("error state", () => {
    it("renders error display when fetch fails and no config", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: null,
          isLoading: false,
          error: "Server error",
        }),
      );

      render(<SettingsView />);

      expect(screen.getByTestId("settings-view")).toBeInTheDocument();
      expect(
        screen.getByText("Backend configuration unavailable"),
      ).toBeInTheDocument();
    });
  });

  describe("backend dropdown", () => {
    it("renders backend dropdown with current value selected", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ backend: "openai" }),
        }),
      );

      render(<SettingsView />);

      const select = screen.getByTestId("backend-select") as HTMLSelectElement;
      expect(select).toBeInTheDocument();
      expect(select.value).toBe("openai");
    });

    it("renders available backend options in dropdown", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({
            available: ["anthropic", "openai", "local", "azure"],
          }),
        }),
      );

      render(<SettingsView />);

      const select = screen.getByTestId("backend-select") as HTMLSelectElement;
      const options = within(select).getAllByRole("option");

      expect(options).toHaveLength(4);
      expect(options[0]).toHaveTextContent("anthropic");
      expect(options[1]).toHaveTextContent("openai");
      expect(options[2]).toHaveTextContent("local");
      expect(options[3]).toHaveTextContent("azure");
    });

    it("renders FleetDB source tag for store-backed config", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ source: "fleetdb" }),
        }),
      );

      render(<SettingsView />);

      expect(screen.getByText("From FleetDB")).toBeInTheDocument();
    });

    it('renders "Default" source tag when source is not project', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ source: "default" }),
        }),
      );

      render(<SettingsView />);

      expect(screen.getByText("Default")).toBeInTheDocument();
    });
  });

  describe("runtime credentials", () => {
    it("describes the GitHub token as shared by remote runtimes and PR review", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());

      render(<SettingsView />);

      expect(
        screen.getByLabelText("GitHub Token for Runtimes and PR Review"),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          "Used by remote runtimes and GitHub PR review in this workspace.",
        ),
      ).toBeInTheDocument();
    });
  });

  describe("save button", () => {
    it("save button disabled when no changes", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());

      render(<SettingsView />);

      const saveButton = screen.getByTestId("save-button");
      expect(saveButton).toBeDisabled();
    });

    it("save button enabled after dropdown change", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ backend: "anthropic" }),
        }),
      );

      render(<SettingsView />);

      const select = screen.getByTestId("backend-select");
      fireEvent.change(select, { target: { value: "openai" } });

      const saveButton = screen.getByTestId("save-button");
      expect(saveButton).not.toBeDisabled();
    });

    it("calls updateBackend on save button click", async () => {
      const mockUpdateBackend = vi.fn().mockResolvedValue(undefined);
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ backend: "anthropic" }),
          updateBackend: mockUpdateBackend,
        }),
      );

      render(<SettingsView />);

      // Change dropdown value
      const select = screen.getByTestId("backend-select");
      fireEvent.change(select, { target: { value: "openai" } });

      // Click save
      const saveButton = screen.getByTestId("save-button");
      await act(async () => {
        fireEvent.click(saveButton);
      });

      expect(mockUpdateBackend).toHaveBeenCalledWith("openai");
    });

    it('shows "Saving..." text when isSaving is true', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({ isSaving: true }),
      );

      render(<SettingsView />);

      const saveButton = screen.getByTestId("save-button");
      expect(saveButton).toHaveTextContent("Saving...");
    });

    it("save button disabled when isSaving is true", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({ isSaving: true }),
      );

      render(<SettingsView />);

      const saveButton = screen.getByTestId("save-button");
      expect(saveButton).toBeDisabled();
    });
  });

  describe("agent override table", () => {
    it("renders agent override table when agents have overrides", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({
            agents: [
              { worktree: "feature-a", role: "coder", backend: "openai" },
              { worktree: "feature-b", role: "reviewer", backend: "local" },
            ],
          }),
        }),
      );

      render(<SettingsView />);

      const table = screen.getByTestId("agent-overrides-table");
      expect(table).toBeInTheDocument();

      // Check table headers
      expect(screen.getByText("Worktree")).toBeInTheDocument();
      expect(screen.getByText("Role")).toBeInTheDocument();
      expect(screen.getByText("Backend")).toBeInTheDocument();

      // Check table data
      expect(screen.getByText("feature-a")).toBeInTheDocument();
      expect(screen.getByText("coder")).toBeInTheDocument();
      expect(screen.getByText("feature-b")).toBeInTheDocument();
      expect(screen.getByText("reviewer")).toBeInTheDocument();
    });

    it("hides agent table when no overrides", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({
            agents: [
              { worktree: "feature-a", role: "coder", backend: "" },
              { worktree: "feature-b", role: "reviewer", backend: "" },
            ],
          }),
        }),
      );

      render(<SettingsView />);

      expect(
        screen.queryByTestId("agent-overrides-table"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("no-overrides-message")).toBeInTheDocument();
    });

    it("shows empty message when agents list empty", () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ agents: [] }),
        }),
      );

      render(<SettingsView />);

      expect(
        screen.queryByTestId("agent-overrides-table"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("no-overrides-message")).toBeInTheDocument();
      expect(
        screen.getByText("No per-agent overrides configured."),
      ).toBeInTheDocument();
    });
  });

  describe("local task runner settings", () => {
    it("saves the opencode model setting", async () => {
      const mockUpdateLocalTaskRunner = vi.fn().mockResolvedValue(true);
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());
      mockUseLocalSettings.mockReturnValue(
        createMockLocalSettingsReturn({
          updateLocalTaskRunner: mockUpdateLocalTaskRunner,
        }),
      );

      render(<SettingsView />);

      fireEvent.change(screen.getByTestId("opencode-model-input"), {
        target: { value: "anthropic/claude-sonnet-4-20250514" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("local-task-runner-save-button"));
      });

      expect(mockUpdateLocalTaskRunner).toHaveBeenCalledWith({
        opencode_model: "anthropic/claude-sonnet-4-20250514",
      });
    });
  });

  describe("FleetDB Redis panel", () => {
    it("renders disabled Redis settings by default", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());

      render(<SettingsView />);

      expect(screen.getByTestId("fleetdb-redis-panel")).toBeInTheDocument();
      expect(screen.getByText("FleetDB Redis")).toBeInTheDocument();
      expect(screen.getByTestId("redis-enabled-checkbox")).not.toBeChecked();
      expect(screen.queryByTestId("redis-url-input")).not.toBeInTheDocument();
    });

    it("shows saved Redis connection metadata without the password", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());
      mockUseLocalSettings.mockReturnValue(
        createMockLocalSettingsReturn({
          settings: {
            version: 1,
            fleetdb_redis: {
              enabled: true,
              addr: "example.redis:6379",
              db: 2,
              tls: true,
              password_set: true,
            },
          },
        }),
      );

      render(<SettingsView />);

      expect(screen.getByTestId("redis-enabled-checkbox")).toBeChecked();
      expect(screen.getByTestId("redis-current")).toHaveTextContent(
        "example.redis:6379",
      );
      expect(screen.getByTestId("redis-current")).toHaveTextContent(
        "database 2",
      );
      expect(screen.getByTestId("redis-current")).toHaveTextContent("TLS");
      expect(screen.getByTestId("redis-current")).toHaveTextContent(
        "password saved",
      );
    });

    it("saves a pasted redis-cli TLS command", async () => {
      const mockUpdateRedis = vi.fn().mockResolvedValue(true);
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());
      mockUseLocalSettings.mockReturnValue(
        createMockLocalSettingsReturn({ updateRedis: mockUpdateRedis }),
      );

      render(<SettingsView />);

      fireEvent.click(screen.getByTestId("redis-enabled-checkbox"));
      expect(screen.getByLabelText("Database index")).toBeInTheDocument();
      fireEvent.change(screen.getByTestId("redis-url-input"), {
        target: {
          value: "redis-cli --tls -u redis://default:secret@example.redis:6379",
        },
      });
      fireEvent.change(screen.getByTestId("redis-db-input"), {
        target: { value: "1" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("redis-save-button"));
      });

      expect(mockUpdateRedis).toHaveBeenCalledWith({
        enabled: true,
        db: 1,
        tls: true,
        redis_url:
          "redis-cli --tls -u redis://default:secret@example.redis:6379",
      });
    });
  });

  describe("terminal font panel", () => {
    it("renders the Terminal Font panel", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());

      render(<SettingsView />);

      expect(screen.getByTestId("terminal-font-panel")).toBeInTheDocument();
      expect(screen.getByText("Terminal Font")).toBeInTheDocument();
    });

    it("renders font family select with current preset value", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());
      mockUseTerminalFont.mockReturnValue(
        createMockFontReturn({ fontFamily: "Menlo, monospace" }),
      );

      render(<SettingsView />);

      const select = screen.getByTestId(
        "font-family-select",
      ) as HTMLSelectElement;
      expect(select.value).toBe("Menlo, monospace");
    });

    it("renders font size select with current value", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());
      mockUseTerminalFont.mockReturnValue(
        createMockFontReturn({ fontSize: 18 }),
      );

      render(<SettingsView />);

      const select = screen.getByTestId(
        "font-size-select",
      ) as HTMLSelectElement;
      expect(select.value).toBe("18");
    });

    it("calls setFontFamily when font family select changes", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());

      render(<SettingsView />);

      const select = screen.getByTestId("font-family-select");
      fireEvent.change(select, { target: { value: "Monaco, monospace" } });

      expect(mockSetFontFamily).toHaveBeenCalledWith("Monaco, monospace");
    });

    it("calls setFontSize when font size select changes", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());

      render(<SettingsView />);

      const select = screen.getByTestId("font-size-select");
      fireEvent.change(select, { target: { value: "20" } });

      expect(mockSetFontSize).toHaveBeenCalledWith(20);
    });

    it("shows custom input when Custom is selected", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());
      // A non-preset font shows the custom input
      mockUseTerminalFont.mockReturnValue(
        createMockFontReturn({ fontFamily: "MyCustomFont, serif" }),
      );

      render(<SettingsView />);

      expect(
        screen.getByTestId("font-family-custom-input"),
      ).toBeInTheDocument();
    });

    it("does not show custom input for preset fonts", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());
      mockUseTerminalFont.mockReturnValue(
        createMockFontReturn({ fontFamily: "Menlo, monospace" }),
      );

      render(<SettingsView />);

      expect(
        screen.queryByTestId("font-family-custom-input"),
      ).not.toBeInTheDocument();
    });

    it("typing in custom input calls setFontFamily", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());
      mockUseTerminalFont.mockReturnValue(
        createMockFontReturn({ fontFamily: "MyFont" }),
      );

      render(<SettingsView />);

      const input = screen.getByTestId(
        "font-family-custom-input",
      ) as HTMLInputElement;
      fireEvent.change(input, { target: { value: "AnotherFont, monospace" } });

      expect(mockSetFontFamily).toHaveBeenCalledWith("AnotherFont, monospace");
    });
  });

  describe("className prop", () => {
    it("applies custom className", () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());

      render(<SettingsView className="custom-settings" />);

      const settingsView = screen.getByTestId("settings-view");
      expect(settingsView).toHaveClass("custom-settings");
    });
  });
});
