/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for PendingInputBanner: the operator's half of the human answer
 * path. The banner must render the prompt with its typed options, deliver the
 * chosen option WITH the request id (so a stale click cannot land on a
 * replaced prompt), and disappear once nothing is pending.
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { PendingInputBanner } from "./PendingInputBanner";

const fetchAgentPendingInput = vi.fn();
const answerAgentInput = vi.fn();

vi.mock("@/api/agents/pendingInputs", () => ({
  fetchAgentPendingInput: (...args: unknown[]) =>
    fetchAgentPendingInput(...args),
  answerAgentInput: (...args: unknown[]) => answerAgentInput(...args),
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "TESTMIRROR" }),
}));

const pending = {
  request_id: "req-1",
  agent: "critic",
  kind: "trust_prompt",
  prompt: "Do you trust the files in this folder?",
  options: [
    { id: "1", label: "Yes, proceed" },
    { id: "2", label: "No, exit" },
  ],
  asked_at: new Date().toISOString(),
};

describe("PendingInputBanner", () => {
  beforeEach(() => {
    fetchAgentPendingInput.mockReset();
    answerAgentInput.mockReset();
  });

  it("renders nothing when the agent is not waiting", async () => {
    fetchAgentPendingInput.mockResolvedValue([]);
    render(<PendingInputBanner agentName="critic" />);
    await waitFor(() => expect(fetchAgentPendingInput).toHaveBeenCalled());
    expect(screen.queryByTestId("pending-input-banner")).not.toBeInTheDocument();
  });

  it("shows the prompt with its typed options and delivers the click with the request id", async () => {
    fetchAgentPendingInput.mockResolvedValue([pending]);
    answerAgentInput.mockResolvedValue(undefined);

    render(<PendingInputBanner agentName="critic" />);

    const banner = await screen.findByTestId("pending-input-banner");
    expect(banner).toHaveTextContent("Do you trust the files in this folder?");
    expect(banner).toHaveTextContent("trust_prompt");

    fireEvent.click(screen.getByRole("button", { name: "Yes, proceed" }));

    await waitFor(() =>
      expect(answerAgentInput).toHaveBeenCalledWith("TESTMIRROR", "critic", {
        request_id: "req-1",
        option_id: "1",
      }),
    );
    // Delivered answers clear the banner without waiting for the next poll.
    await waitFor(() =>
      expect(
        screen.queryByTestId("pending-input-banner"),
      ).not.toBeInTheDocument(),
    );
  });

  it("delivers a decline", async () => {
    fetchAgentPendingInput.mockResolvedValue([pending]);
    answerAgentInput.mockResolvedValue(undefined);

    render(<PendingInputBanner agentName="critic" />);
    await screen.findByTestId("pending-input-banner");

    fireEvent.click(screen.getByRole("button", { name: "Decline" }));

    await waitFor(() =>
      expect(answerAgentInput).toHaveBeenCalledWith("TESTMIRROR", "critic", {
        request_id: "req-1",
        decline: true,
      }),
    );
  });

  it("delivers free text and surfaces a delivery failure", async () => {
    fetchAgentPendingInput.mockResolvedValue([pending]);
    answerAgentInput.mockRejectedValue(new Error("prompt changed under you"));

    render(<PendingInputBanner agentName="critic" />);
    await screen.findByTestId("pending-input-banner");

    fireEvent.change(screen.getByPlaceholderText("Answer with text…"), {
      target: { value: "use staging" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() =>
      expect(answerAgentInput).toHaveBeenCalledWith("TESTMIRROR", "critic", {
        request_id: "req-1",
        text: "use staging",
      }),
    );
    // The failure must be visible and the banner must stay for a retry.
    expect(
      await screen.findByText(/prompt changed under you/),
    ).toBeInTheDocument();
    expect(screen.getByTestId("pending-input-banner")).toBeInTheDocument();
  });
});
