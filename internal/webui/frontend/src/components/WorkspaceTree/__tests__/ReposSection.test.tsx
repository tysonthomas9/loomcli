/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { RepoInfo } from "@/api/workspace";
import { ReposSection } from "../ReposSection";

const { mockDeleteWorkspaceRepo, mockShowToast } = vi.hoisted(() => ({
  mockDeleteWorkspaceRepo: vi.fn(),
  mockShowToast: vi.fn(),
}));

vi.mock("@/hooks/api", () => ({
  deleteWorkspaceRepo: (...args: unknown[]) => mockDeleteWorkspaceRepo(...args),
}));

vi.mock("@/hooks", () => ({
  useToast: () => ({ showToast: mockShowToast }),
  useRegisterEscapeLayer: vi.fn(),
  LAYER_CONFIRM_DIALOG: 60,
}));

function repo(name: string): RepoInfo {
  return {
    name,
    path: `/repos/${name}`,
    default_branch: "main",
    remote: "origin",
    groups: [],
  };
}

describe("ReposSection", () => {
  beforeEach(() => {
    mockDeleteWorkspaceRepo.mockReset();
    mockDeleteWorkspaceRepo.mockResolvedValue({});
    mockShowToast.mockReset();
  });

  it("confirms repo removal and calls the delete API", async () => {
    const onRepoRemoved = vi.fn();

    render(
      <ReposSection
        repos={[repo("api")]}
        workspaceId="ws-alpha"
        onRepoRemoved={onRepoRemoved}
      />,
    );

    fireEvent.click(screen.getByLabelText("More actions for repo api"));

    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(
      screen.getByText(/will not delete anything on disk/i),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Remove" }));

    await waitFor(() => {
      expect(mockDeleteWorkspaceRepo).toHaveBeenCalledWith("ws-alpha", "api");
    });
    await waitFor(() => {
      expect(onRepoRemoved).toHaveBeenCalledOnce();
    });
  });
});
