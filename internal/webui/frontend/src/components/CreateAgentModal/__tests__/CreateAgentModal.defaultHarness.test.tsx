/**
 * @vitest-environment jsdom
 */

/**
 * DEF1: the AI Backend picker never defaults to a harness this server
 * reports as unavailable; it takes the first available one and says why.
 * The rule is the same for every harness.
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { CreateAgentModal } from "../CreateAgentModal";
import type { RepoInfo } from "@/api/workspace";
import { toBackendInfo, type BackendInfo } from "@/utils/workspace";

const mockCreateAgent = vi.fn();
const mockCreateLead = vi.fn();
let leadHarnesses: string[] = [];
let backends: BackendInfo[] = [];

vi.mock("@/hooks/agents", () => ({
  useCreateWorkspaceAgent: () => mockCreateAgent,
  useCreateLead: () => mockCreateLead,
  useLeadHarnesses: () => leadHarnesses,
  useInteractivePrompts: () => ({
    prompts: [
      { id: "lead", label: "Lead" },
      { id: "pr-review", label: "PR Review" },
    ],
    isLoading: false,
    error: null,
  }),
}));

vi.mock("@/hooks/workspace", () => ({
  useBackends: () => ({
    backends,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
}));

const repos: RepoInfo[] = [
  { name: "alpha", default_branch: "main", path: "/a" },
];

const HARNESSES = ["claude", "codex", "opencode"];

function setBackends(unavailable: string[]): void {
  backends = HARNESSES.map((name) =>
    toBackendInfo(name, { available: !unavailable.includes(name) }),
  );
}

function renderModal(defaultBackend: string) {
  return render(
    <CreateAgentModal
      isOpen
      workspaceId="ws-1"
      repos={repos}
      defaultBackend={defaultBackend}
      onClose={vi.fn()}
      onSuccess={vi.fn()}
      onLeadCreated={vi.fn()}
    />,
  );
}

const picker = () => screen.getByTestId("create-agent-backend");

beforeEach(() => {
  mockCreateAgent.mockReset();
  mockCreateAgent.mockResolvedValue({
    name: "x",
    repos: [],
    repo_groups: [],
    cross_repo: false,
  });
  mockCreateLead.mockReset();
  mockCreateLead.mockResolvedValue({ agent_id: "ag_1", name: "x" });
  leadHarnesses = [];
  backends = [];
});

describe("CreateAgentModal: default AI Backend skips unavailable harnesses", () => {
  it.each(HARNESSES)(
    "defaults past an unavailable %s to the first available harness",
    (disabled) => {
      setBackends([disabled]);
      renderModal(disabled);
      const expected = HARNESSES.find((h) => h !== disabled)!;
      expect(picker()).toHaveValue(expected);
      expect(
        screen.getByText(
          `${toBackendInfo(disabled).displayName} is unavailable, so ${toBackendInfo(expected).displayName} is selected.`,
        ),
      ).toBeInTheDocument();
    },
  );

  it("submits the fallback harness", async () => {
    setBackends(["codex"]);
    renderModal("codex");
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "runner" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      backend: "claude",
    });
  });

  it("keeps an available default and shows no note", () => {
    setBackends(["claude"]);
    renderModal("codex");
    expect(picker()).toHaveValue("codex");
    expect(screen.queryByText(/is unavailable, so/)).not.toBeInTheDocument();
  });

  it("keeps the default while the harness list is not loaded", () => {
    renderModal("codex");
    expect(picker()).toHaveValue("codex");
  });

  it("keeps the default when no harness is available", () => {
    setBackends(HARNESSES);
    renderModal("codex");
    expect(picker()).toHaveValue("codex");
  });

  it("keeps a harness the user picks, even an unavailable one", () => {
    setBackends(["codex"]);
    renderModal("codex");
    fireEvent.change(picker(), { target: { value: "codex" } });
    expect(picker()).toHaveValue("codex");
    expect(screen.queryByText(/is unavailable, so/)).not.toBeInTheDocument();
  });

  it("gives a lead the first available lead harness", () => {
    leadHarnesses = ["codex", "opencode"];
    setBackends(["codex"]);
    renderModal("codex");
    fireEvent.click(screen.getByTestId("create-agent-template-lead"));
    expect(picker()).toHaveValue("opencode");
  });

  it("takes a lead's fallback in the lead harness order", async () => {
    leadHarnesses = ["codex", "opencode", "claude"];
    setBackends(["codex"]);
    renderModal("codex");
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "lead-a" },
    });
    fireEvent.click(screen.getByTestId("create-agent-template-lead"));
    expect(picker()).toHaveValue("opencode");
    expect(
      screen.getByText("Codex is unavailable, so OpenCode is selected."),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    await waitFor(() => expect(mockCreateLead).toHaveBeenCalled());
    expect(mockCreateLead.mock.calls[0][0]).toMatchObject({
      overrides: { harness: "opencode" },
    });
  });
});
