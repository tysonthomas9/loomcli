// @vitest-environment jsdom

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { PRDiscussionPanel } from "../PRDiscussionPanel";

const mocks = vi.hoisted(() => ({
  ensureReviewer: vi.fn(),
  getReviewerConversation: vi.fn(),
  sendReviewerMessage: vi.fn(),
}));

vi.mock("@/api/workspace/prReview", () => ({
  ensureReviewer: mocks.ensureReviewer,
  getReviewerConversation: mocks.getReviewerConversation,
  sendReviewerMessage: mocks.sendReviewerMessage,
}));

vi.mock("@/components/TerminalView/TerminalView", () => ({
  TerminalView: () => <div data-testid="terminal-stub" />,
}));

describe("PRDiscussionPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.ensureReviewer.mockResolvedValue({
      agent_name: "review-hello-pr-7",
      checked_out_sha: "sha-old",
      seeded: true,
    });
    mocks.getReviewerConversation.mockResolvedValue({
      state: "idle",
      messages: [
        {
          turn_id: "t1",
          item_id: "i1",
          role: "user",
          text: "hello",
        },
        {
          turn_id: "t1",
          item_id: "i2",
          role: "assistant",
          text: "hi",
        },
      ],
    });
    mocks.sendReviewerMessage.mockResolvedValue({
      state: "delivered",
      reason: "",
    });
  });

  it("renders assistant chat text as formatted Markdown (bold + inline code)", async () => {
    mocks.getReviewerConversation.mockResolvedValue({
      state: "idle",
      messages: [
        {
          turn_id: "t1",
          item_id: "i1",
          role: "assistant",
          text: "Risk at **foo.go** via `checkout`.",
        },
      ],
    });
    const { container } = render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );

    const messages = await screen.findByTestId("pr-chat-messages");
    expect(await within(messages).findByText("foo.go")).toBeInTheDocument();
    expect(container.querySelector("strong")).toHaveTextContent("foo.go");
    expect(container.querySelector("code")).toHaveTextContent("checkout");
    expect(container.textContent).not.toContain("**foo.go**");
    expect(container.textContent).not.toContain("`checkout`");
  });

  it("lays out multi-paragraph markdown without pre-wrap gaps", async () => {
    mocks.getReviewerConversation.mockResolvedValue({
      state: "idle",
      messages: [
        {
          turn_id: "t1",
          item_id: "i1",
          role: "assistant",
          text: "First paragraph.\n\nSecond paragraph.",
        },
      ],
    });
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );

    const markdown = await screen.findByTestId("markdown-content");
    expect(markdown.querySelectorAll("p")).toHaveLength(2);
    expect(markdown).toHaveTextContent("First paragraph.");
    expect(markdown).toHaveTextContent("Second paragraph.");
  });

  it("ensures, renders messages, sends chat text, and keeps the terminal mounted", async () => {
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(mocks.ensureReviewer).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
      );
    });

    const messages = await screen.findByTestId("pr-chat-messages");
    expect(await within(messages).findByText("hello")).toBeInTheDocument();
    expect(within(messages).getByText("hi")).toBeInTheDocument();

    expect(screen.getByTestId("terminal-stub")).toBeInTheDocument();

    fireEvent.change(screen.getByTestId("pr-chat-composer"), {
      target: { value: "please review this" },
    });
    fireEvent.click(screen.getByTestId("pr-chat-send"));

    await waitFor(() => {
      expect(mocks.sendReviewerMessage).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
        "please review this",
      );
    });

    fireEvent.click(screen.getByTestId("pr-discussion-tab-terminal"));
    expect(screen.getByTestId("terminal-stub")).toBeInTheDocument();
  });

  it("keeps the typed message when a send fails", async () => {
    mocks.sendReviewerMessage.mockRejectedValueOnce(new Error("boom"));
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );
    await screen.findByTestId("pr-chat-messages");

    const composer = screen.getByTestId("pr-chat-composer");
    fireEvent.change(composer, { target: { value: "keep me" } });
    fireEvent.click(screen.getByTestId("pr-chat-send"));

    await waitFor(() => {
      expect(mocks.sendReviewerMessage).toHaveBeenCalled();
    });
    // The failed send must NOT clear the composer.
    expect((composer as HTMLTextAreaElement).value).toBe("keep me");
  });

  it("retries standing up the reviewer after an ensure failure", async () => {
    mocks.ensureReviewer
      .mockRejectedValueOnce(new Error("cold backend"))
      .mockResolvedValueOnce({
        agent_name: "review-hello-pr-7",
        checked_out_sha: "sha-old",
        seeded: true,
      });
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );

    const retry = await screen.findByTestId("pr-discussion-retry");
    expect(mocks.ensureReviewer).toHaveBeenCalledTimes(1);

    fireEvent.click(retry);

    await waitFor(() => {
      expect(mocks.ensureReviewer).toHaveBeenCalledTimes(2);
    });
    // After a successful retry the conversation loads (messages render).
    await waitFor(() => {
      expect(screen.getByText("hello")).toBeInTheDocument();
    });
  });

  it("shows the unsupported state, disables the composer, and offers the terminal", async () => {
    mocks.getReviewerConversation.mockResolvedValue({
      state: "unsupported",
      detail: "The chat view is not available for the cursor backend.",
      messages: [],
    });
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );

    const notice = await screen.findByTestId("pr-chat-unavailable");
    expect(notice).toHaveTextContent("cursor backend");
    expect(screen.getByTestId("pr-chat-composer")).toBeDisabled();
    expect(screen.getByTestId("pr-chat-send")).toBeDisabled();

    // The shortcut lands the user on the terminal tab.
    fireEvent.click(screen.getByTestId("pr-chat-open-terminal"));
    expect(screen.getByTestId("pr-discussion-tab-terminal")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("shows the failed state with a disabled composer", async () => {
    mocks.getReviewerConversation.mockResolvedValue({
      state: "failed",
      messages: [],
    });
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );

    const notice = await screen.findByTestId("pr-chat-unavailable");
    expect(notice).toHaveTextContent("stopped unexpectedly");
    expect(screen.getByTestId("pr-chat-composer")).toBeDisabled();
    expect(screen.queryByTestId("pr-chat-open-terminal")).toBeNull();
  });

  it("renders tool calls as collapsed pills that expand on click", async () => {
    mocks.getReviewerConversation.mockResolvedValue({
      state: "idle",
      messages: [
        {
          turn_id: "t1",
          item_id: "i-tool",
          role: "assistant",
          kind: "tool_use",
          text: "Bash",
          tool_name: "Bash",
          tool_input: '{"command":"ls src"}',
          tool_result: "a.ts\nb.ts",
        },
        {
          turn_id: "t1",
          item_id: "i-text",
          role: "assistant",
          text: "two files",
        },
      ],
    });
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );

    const messages = await screen.findByTestId("pr-chat-messages");
    const pill = await within(messages).findByTestId("tool-pill");
    expect(pill).toHaveTextContent("Bash");
    expect(pill).toHaveTextContent("ls src");
    expect(within(messages).queryByText("a.ts")).toBeNull();

    fireEvent.click(pill);
    expect(await within(messages).findByText(/a\.ts/)).toBeInTheDocument();
    expect(within(messages).getByText("two files")).toBeInTheDocument();
  });

  it("keeps the last good messages through a reconnecting snapshot", async () => {
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );
    const messages = await screen.findByTestId("pr-chat-messages");
    await waitFor(() => {
      expect(within(messages).getByText("hi")).toBeInTheDocument();
    });

    // The next poll fails transiently (torn transcript append): reconnecting
    // with no messages. Trigger it via a send's optimistic refetch.
    mocks.getReviewerConversation.mockResolvedValue({
      state: "reconnecting",
      messages: [],
    });
    fireEvent.change(screen.getByTestId("pr-chat-composer"), {
      target: { value: "still there?" },
    });
    fireEvent.click(screen.getByTestId("pr-chat-send"));
    await waitFor(() => {
      expect(mocks.sendReviewerMessage).toHaveBeenCalled();
    });

    // The chat must not blank out.
    expect(within(messages).getByText("hi")).toBeInTheDocument();
  });
});
