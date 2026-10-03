/**
 * @vitest-environment jsdom
 */
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

vi.mock("@/api/workspace/git", () => ({
  getGitSettings: vi.fn(),
  updateGitSettings: vi.fn(),
}));

import { getGitSettings, updateGitSettings } from "@/api/workspace/git";
import { GitSettingsPanel } from "../GitSettingsPanel";

const mockGet = vi.mocked(getGitSettings);
const mockUpdate = vi.mocked(updateGitSettings);

const defaults = {
  delivery_mode: "stack",
  lead_may_approve_publish: true,
  lead_may_merge: "off",
} as const;

describe("GitSettingsPanel", () => {
  beforeEach(() => {
    mockGet.mockReset();
    mockUpdate.mockReset();
  });

  it("shows all three values read from the server", async () => {
    mockGet.mockResolvedValue({
      delivery_mode: "trunk",
      lead_may_approve_publish: false,
      lead_may_merge: "when_green",
    });
    render(<GitSettingsPanel workspaceId="W" />);
    await waitFor(() =>
      expect(screen.getByTestId("git-delivery-mode")).toHaveValue("trunk"),
    );
    expect(mockGet).toHaveBeenCalledWith("W");
    expect(
      screen.getByRole("option", { name: "PR per task" }),
    ).toBeInTheDocument();
    expect(screen.getByTestId("git-lead-may-approve")).toHaveValue("off");
    expect(screen.getByTestId("git-lead-may-merge")).toHaveValue("when_green");
  });

  it("saves each change and shows the server's new values", async () => {
    mockGet.mockResolvedValue(defaults);
    mockUpdate
      .mockResolvedValueOnce({
        settings: { ...defaults, delivery_mode: "trunk" },
        warning: "",
      })
      .mockResolvedValueOnce({
        settings: {
          ...defaults,
          delivery_mode: "trunk",
          lead_may_merge: "when_green",
        },
        warning: "no required review",
      })
      .mockResolvedValueOnce({
        settings: {
          delivery_mode: "trunk",
          lead_may_approve_publish: false,
          lead_may_merge: "when_green",
        },
        warning: "",
      });
    render(<GitSettingsPanel workspaceId="W" />);
    await waitFor(() =>
      expect(screen.getByTestId("git-delivery-mode")).toHaveValue("stack"),
    );
    fireEvent.change(screen.getByTestId("git-delivery-mode"), {
      target: { value: "trunk" },
    });
    await waitFor(() =>
      expect(screen.getByTestId("git-delivery-mode")).toHaveValue("trunk"),
    );
    expect(mockUpdate).toHaveBeenLastCalledWith("W", {
      delivery_mode: "trunk",
    });
    fireEvent.change(screen.getByTestId("git-lead-may-merge"), {
      target: { value: "when_green" },
    });
    await waitFor(() =>
      expect(screen.getByTestId("git-merge-warning")).toHaveTextContent(
        "no required review",
      ),
    );
    expect(mockUpdate).toHaveBeenLastCalledWith("W", {
      lead_may_merge: "when_green",
    });
    fireEvent.change(screen.getByTestId("git-lead-may-approve"), {
      target: { value: "off" },
    });
    await waitFor(() =>
      expect(screen.getByTestId("git-lead-may-approve")).toHaveValue("off"),
    );
    expect(mockUpdate).toHaveBeenLastCalledWith("W", {
      lead_may_approve_publish: false,
    });
  });

  it("keeps the server value and shows the refusal when a change fails", async () => {
    mockGet.mockResolvedValue(defaults);
    mockUpdate.mockRejectedValue(
      new Error("only a human can change workspace policy"),
    );
    render(<GitSettingsPanel workspaceId="W" />);
    await waitFor(() =>
      expect(screen.getByTestId("git-lead-may-merge")).toHaveValue("off"),
    );
    fireEvent.change(screen.getByTestId("git-lead-may-merge"), {
      target: { value: "when_green" },
    });
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("only a human"),
    );
    expect(screen.getByTestId("git-lead-may-merge")).toHaveValue("off");
  });

  it("shows a value changed elsewhere after reload", async () => {
    mockGet.mockResolvedValueOnce(defaults).mockResolvedValueOnce({
      ...defaults,
      delivery_mode: "trunk",
    });
    const first = render(<GitSettingsPanel workspaceId="W" />);
    await waitFor(() =>
      expect(screen.getByTestId("git-delivery-mode")).toHaveValue("stack"),
    );
    first.unmount();
    render(<GitSettingsPanel workspaceId="W" />);
    await waitFor(() =>
      expect(screen.getByTestId("git-delivery-mode")).toHaveValue("trunk"),
    );
  });
});
