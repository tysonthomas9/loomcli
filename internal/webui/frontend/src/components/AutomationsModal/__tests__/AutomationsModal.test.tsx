/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { AutomationsModal } from "../AutomationsModal";

const mockCreateBinding = vi.fn();
const mockSetEnabled = vi.fn();
const mockRunWorkflow = vi.fn();

// eslint-disable-next-line @typescript-eslint/no-explicit-any
let mockState: any;

vi.mock("@/hooks/workspace", () => ({
  useAutomations: () => mockState,
}));

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function setup(overrides: Record<string, any> = {}) {
  mockState = {
    workflows: [
      { name: "epic-runner", builtin: true },
      { name: "github-review-agent", builtin: true },
    ],
    bindings: [],
    loading: false,
    error: null,
    refresh: vi.fn(),
    createBinding: mockCreateBinding,
    setEnabled: mockSetEnabled,
    runWorkflow: mockRunWorkflow,
    ...overrides,
  };
  return render(
    <AutomationsModal isOpen workspaceId="ws-1" onClose={vi.fn()} />,
  );
}

beforeEach(() => {
  mockCreateBinding.mockReset().mockResolvedValue({
    binding_id: "binding-github-pull_request-opened",
    name: "code-review",
    route_key: "github.pull_request.opened",
    enabled: true,
  });
  mockSetEnabled.mockReset().mockResolvedValue(undefined);
  mockRunWorkflow
    .mockReset()
    .mockResolvedValue({ run_id: "run-1", status: "queued" });
});

describe("AutomationsModal: code-review binding", () => {
  it("creates a github-review binding with the entered secret", async () => {
    setup();
    fireEvent.change(screen.getByTestId("automation-binding-secret"), {
      target: { value: "s3cret" },
    });
    fireEvent.click(screen.getByTestId("automation-create-binding"));

    await waitFor(() => expect(mockCreateBinding).toHaveBeenCalledTimes(1));
    expect(mockCreateBinding.mock.calls[0][0]).toMatchObject({
      workflow: "github-review-agent",
      source_kind: "github",
      route_key: "github.pull_request.opened",
      secret: "s3cret",
      enabled: true,
    });
  });

  it("refuses to create a binding without a secret", () => {
    setup();
    fireEvent.click(screen.getByTestId("automation-create-binding"));
    expect(mockCreateBinding).not.toHaveBeenCalled();
    expect(screen.getByTestId("automation-binding-error")).toBeInTheDocument();
  });
});

describe("AutomationsModal: existing bindings", () => {
  it("toggles a binding's enabled state", async () => {
    setup({
      bindings: [
        {
          workspace_key: "ws-1",
          binding_id: "b1",
          name: "code-review",
          source_kind: "github",
          route_key: "github.pull_request.opened",
          driver_id: "drv-1",
          driver_version_id: "v1",
          enabled: true,
        },
      ],
    });
    fireEvent.click(screen.getByTestId("automation-binding-toggle-b1"));
    await waitFor(() =>
      expect(mockSetEnabled).toHaveBeenCalledWith("b1", false),
    );
  });
});

describe("AutomationsModal: generic workflow runner", () => {
  it("starts the selected workflow with a parsed JSON payload", async () => {
    setup();
    fireEvent.change(screen.getByTestId("automation-workflow-select"), {
      target: { value: "epic-runner" },
    });
    fireEvent.change(screen.getByTestId("automation-payload"), {
      target: { value: '{"epicId":"E-1"}' },
    });
    fireEvent.click(screen.getByTestId("automation-run"));

    await waitFor(() => expect(mockRunWorkflow).toHaveBeenCalledTimes(1));
    expect(mockRunWorkflow).toHaveBeenCalledWith("epic-runner", {
      epicId: "E-1",
    });
    expect(
      await screen.findByTestId("automation-run-result"),
    ).toHaveTextContent(/run-1/);
  });

  it("rejects an invalid JSON payload before calling the API", () => {
    setup();
    fireEvent.change(screen.getByTestId("automation-payload"), {
      target: { value: "{not json" },
    });
    fireEvent.click(screen.getByTestId("automation-run"));
    expect(mockRunWorkflow).not.toHaveBeenCalled();
    expect(screen.getByTestId("automation-run-error")).toBeInTheDocument();
  });
});
