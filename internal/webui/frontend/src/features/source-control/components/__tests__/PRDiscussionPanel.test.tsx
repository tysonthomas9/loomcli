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
  archiveReviewer: vi.fn(),
  getReviewerConversation: vi.fn(),
  sendReviewerMessage: vi.fn(),
}));

vi.mock("../../api/prReview", () => ({
  ensureReviewer: mocks.ensureReviewer,
  archiveReviewer: mocks.archiveReviewer,
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
    mocks.archiveReviewer.mockResolvedValue({
      agent_name: "review-hello-pr-7",
      archived: true,
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

  it("archives the checkout reviewer before closing the discussion", async () => {
    const onClose = vi.fn();
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={onClose}
      />,
    );
    await screen.findByText("hello");

    fireEvent.click(screen.getByRole("button", { name: "Close discussion" }));

    await waitFor(() => {
      expect(mocks.archiveReviewer).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
      );
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });

  it("does not allow close to race an in-flight reviewer creation", async () => {
    let resolveEnsure:
      | ((value: {
          agent_name: string;
          checked_out_sha: string;
          seeded: boolean;
        }) => void)
      | undefined;
    mocks.ensureReviewer.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveEnsure = resolve;
        }),
    );
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={vi.fn()}
      />,
    );

    const close = screen.getByRole("button", { name: "Close discussion" });
    expect(close).toBeDisabled();
    expect(mocks.archiveReviewer).not.toHaveBeenCalled();

    resolveEnsure?.({
      agent_name: "review-hello-pr-7",
      checked_out_sha: "sha-old",
      seeded: true,
    });
    await waitFor(() => expect(close).not.toBeDisabled());
  });

  it("keeps the discussion open when reviewer archival fails", async () => {
    mocks.archiveReviewer.mockRejectedValueOnce(new Error("archive conflict"));
    const onClose = vi.fn();
    render(
      <PRDiscussionPanel
        workspaceId="WS"
        owner="octocat"
        repo="hello"
        number={7}
        onClose={onClose}
      />,
    );
    await screen.findByText("hello");

    fireEvent.click(screen.getByRole("button", { name: "Close discussion" }));

    expect(await screen.findByText("archive conflict")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
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
