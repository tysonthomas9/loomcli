// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { AgentChatPage } from "../AgentChatPage";

vi.mock("@/components/AgentChat", () => ({
  AgentChat: ({ agentId }: { agentId: string }) => (
    <div data-testid="chat">chat {agentId}</div>
  ),
}));

vi.mock("@/components/AgentDetailPanel", () => ({
  GitTab: (p: {
    agent: { name: string; branch: string };
    readOnly?: boolean;
  }) => (
    <div data-testid="git" data-readonly={String(!!p.readOnly)}>
      git {p.agent.name} {p.agent.branch}
    </div>
  ),
  DiffTab: (p: { agent: { name: string } }) => (
    <div data-testid="diff">diff {p.agent.name}</div>
  ),
}));

vi.mock("@/components/FileExplorer", async () => {
  const { useContext } = await import("react");
  const { ExtraBrowserAgent } =
    await import("@/components/FileExplorer/browserAgents");
  return {
    WorkspaceFileBrowser: (p: { agentName: string }) => {
      const agent = useContext(ExtraBrowserAgent);
      return (
        <div data-testid="files">
          files {p.agentName} {agent?.name} {agent?.repos.join(",")}
        </div>
      );
    },
  };
});

vi.mock("@/hooks/agents", () => ({
  useRosterAgent: (id: string) => ({
    agent_id: id,
    name: "Child",
    harness: "opencode",
    state: "idle",
    parent_agent_id: "agt_lead",
    repo: "/work/slack-clone",
    branch: `loom/agent/${id}`,
    worktree_path: `/loom/worktrees/slack-clone/${id}`,
  }),
}));

vi.mock("@/api/agentsv1", () => ({ getAgent: vi.fn() }));

function Where(): JSX.Element {
  return <div data-testid="where">{useLocation().search}</div>;
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route
          path="/ws/:workspaceId/chat/:agentId"
          element={
            <>
              <AgentChatPage />
              <Where />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

const active = () =>
  screen
    .getAllByRole("button")
    .filter((b) => b.getAttribute("aria-current") === "page")
    .map((b) => b.textContent);

describe("AgentChatPage", () => {
  it("shows Chat | Info | Git | Diff | Files with Chat open and no Terminal", () => {
    renderAt("/ws/ws1/chat/agt_1");

    const tabs = ["Chat", "Info", "Git", "Diff", "Files"];
    for (const name of tabs) {
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    }
    expect(
      screen.queryByRole("button", { name: "Terminal" }),
    ).not.toBeInTheDocument();
    expect(active()).toEqual(["Chat"]);
    expect(screen.getByTestId("chat")).toHaveTextContent("chat agt_1");
  });

  it("reads the agent's own worktree in Git, Diff and Files, Git read-only", async () => {
    renderAt("/ws/ws1/chat/agt_1");

    expect(screen.getByTestId("git")).toHaveTextContent(
      "git agt_1 loom/agent/agt_1",
    );
    expect(screen.getByTestId("git")).toHaveAttribute("data-readonly", "true");
    fireEvent.click(screen.getByRole("button", { name: "Diff" }));
    expect(await screen.findByTestId("diff")).toHaveTextContent("diff agt_1");
    fireEvent.click(screen.getByRole("button", { name: "Files" }));
    expect(await screen.findByTestId("files")).toHaveTextContent(
      "files agt_1 agt_1 slack-clone",
    );
  });

  it("keeps the open tab in the URL so a reload reopens it", () => {
    const first = renderAt("/ws/ws1/chat/agt_1");
    fireEvent.click(screen.getByRole("button", { name: "Diff" }));
    expect(screen.getByTestId("where")).toHaveTextContent("?tab=diff");
    fireEvent.click(screen.getByRole("button", { name: "Chat" }));
    expect(screen.getByTestId("where")).toHaveTextContent(/^$/);
    first.unmount();

    renderAt("/ws/ws1/chat/agt_1?tab=diff");
    expect(active()).toEqual(["Diff"]);
  });

  it("ignores an unknown tab, including terminal", () => {
    renderAt("/ws/ws1/chat/agt_1?tab=terminal");
    expect(active()).toEqual(["Chat"]);
  });

  it("splits the Chat tab into its own column", () => {
    renderAt("/ws/ws1/chat/agt_1");
    fireEvent.click(screen.getByTestId("agent-editor-split"));
    expect(screen.getByTestId("agent-editor-groups")).toHaveAttribute(
      "data-split",
      "true",
    );
    expect(active()).toEqual(["Info", "Chat"]);
  });

  it("shows harness, state and a link to the parent in Info", () => {
    renderAt("/ws/ws1/chat/agt_1?tab=info");
    expect(screen.getByText("opencode")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "agt_lead" })).toHaveAttribute(
      "href",
      "/ws/ws1/chat/agt_lead",
    );
  });
});
