/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { OnboardingActionsProvider } from "@/contexts/OnboardingActionsContext";
import { fetchOnboardingStatus } from "@/api/onboarding";
import type { OnboardingStatusWire } from "@/types/onboarding";

import { OnboardingFlow } from "../OnboardingFlow";

vi.mock("@/api/onboarding", () => ({
  fetchOnboardingStatus: vi.fn(),
}));

const mockFetch = vi.mocked(fetchOnboardingStatus);

interface RenderOpts {
  context?: "no-workspace" | "empty-kanban";
  workspaceId?: string;
  onUnregistered?: (action: string) => void;
}

function renderFlow(
  wire: OnboardingStatusWire,
  opts: RenderOpts = {},
): ReturnType<typeof render> {
  mockFetch.mockResolvedValueOnce(wire);
  const { context = "empty-kanban", workspaceId, onUnregistered } = opts;
  // Resolve "default" workspaceId only when the key is absent so callers
  // can explicitly pass undefined for the no-workspace case.
  const ws = "workspaceId" in opts ? workspaceId : "ws-1";
  return render(
    <OnboardingActionsProvider
      onUnregistered={onUnregistered as (action: never) => void}
    >
      <OnboardingFlow context={context} workspaceId={ws} />
    </OnboardingActionsProvider>,
  );
}

function partialWire(
  overrides: Partial<OnboardingStatusWire> = {},
): OnboardingStatusWire {
  return {
    workspace_id: "ws-1",
    all_complete: false,
    steps: [
      {
        id: "workspace-repo",
        status: "complete",
        action: "open_workspace_repo_wizard",
      },
      { id: "verify-repo", status: "complete", action: "open_repo_checks" },
      {
        id: "setup-backend",
        status: "actionable",
        action: "open_backend_setup",
        message: "Codex installed but not authenticated.",
      },
      { id: "create-agent", status: "blocked", action: "open_create_agent" },
      { id: "create-issue", status: "blocked", action: "open_create_issue" },
      { id: "run-agent", status: "blocked", action: "start_first_agent" },
    ],
    ...overrides,
  };
}

describe("OnboardingFlow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("renders all six steps with the right indicators", async () => {
    renderFlow(partialWire());

    expect(await screen.findByTestId("onboarding-flow")).toBeInTheDocument();
    // Six rows.
    const rows = await screen.findAllByRole("listitem");
    expect(rows).toHaveLength(6);
    // Complete steps should mark themselves so via aria-current absence.
    const setupRow = screen.getByTestId("onboarding-cta-setup-backend").closest("li")!;
    expect(setupRow.getAttribute("aria-current")).toBe("step");
    expect(setupRow.getAttribute("data-status")).toBe("actionable");
  });

  it("dispatches the step's action on CTA click", async () => {
    const onUnregistered = vi.fn();
    renderFlow(partialWire(), { onUnregistered });

    const cta = await screen.findByTestId("onboarding-cta-setup-backend");
    fireEvent.click(cta);

    expect(onUnregistered).toHaveBeenCalledWith("open_backend_setup");
  });

  it("renders 'Done' for complete steps and no CTA for blocked steps", async () => {
    renderFlow(partialWire());

    // Complete steps show a "Done" indicator instead of a disabled
    // button — completed work shouldn't show another action.
    const completeIndicator = await screen.findByTestId(
      "onboarding-cta-workspace-repo",
    );
    expect(completeIndicator).toHaveTextContent(/done/i);
    expect(completeIndicator.tagName).not.toBe("BUTTON");

    // Blocked steps emit no CTA at all (so users can't click into a
    // dead-end action). The row stays for hierarchy/visibility.
    expect(
      screen.queryByTestId("onboarding-cta-create-agent"),
    ).not.toBeInTheDocument();
  });

  it("dismiss button writes the per-workspace flag and removes the flow", async () => {
    renderFlow(partialWire());
    const dismiss = await screen.findByTestId("onboarding-dismiss");
    fireEvent.click(dismiss);

    expect(localStorage.getItem("loom:ws-1:onboarding-dismissed")).toBe("1");
    expect(screen.queryByTestId("onboarding-flow")).not.toBeInTheDocument();
  });

  it("hides the dismiss button in the no-workspace context", async () => {
    renderFlow(partialWire({ workspace_id: undefined }), {
      context: "no-workspace",
      workspaceId: undefined,
    });
    await screen.findByTestId("onboarding-flow");
    expect(screen.queryByTestId("onboarding-dismiss")).not.toBeInTheDocument();
  });

  it("renders nothing when all steps are complete", async () => {
    renderFlow(
      partialWire({
        all_complete: true,
        steps: [
          {
            id: "workspace-repo",
            status: "complete",
            action: "open_workspace_repo_wizard",
          },
          { id: "verify-repo", status: "complete", action: "open_repo_checks" },
          {
            id: "setup-backend",
            status: "complete",
            action: "open_backend_setup",
          },
          {
            id: "create-agent",
            status: "complete",
            action: "open_create_agent",
          },
          {
            id: "create-issue",
            status: "complete",
            action: "open_create_issue",
          },
          { id: "run-agent", status: "complete", action: "start_first_agent" },
        ],
      }),
    );
    // Wait one microtask for the fetch promise to resolve.
    await Promise.resolve();
    expect(screen.queryByTestId("onboarding-flow")).not.toBeInTheDocument();
  });

  it("uses the step message in place of the description when present", async () => {
    renderFlow(partialWire());
    expect(
      await screen.findByText(/codex installed but not authenticated/i),
    ).toBeInTheDocument();
  });
});
