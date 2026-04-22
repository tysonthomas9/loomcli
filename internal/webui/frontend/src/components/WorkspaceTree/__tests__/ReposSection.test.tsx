/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { ReposSection } from "../ReposSection";
import type { RepoInfo } from "@/api/workspace";

const makeRepo = (
  name: string,
  overrides: Partial<RepoInfo> = {},
): RepoInfo => ({
  name,
  path: `/tmp/${name}`,
  default_branch: "main",
  remote: "origin",
  groups: [],
  ...overrides,
});

describe("ReposSection — default-branch inline edit", () => {
  it("returns null when there are no repos", () => {
    const { container } = render(<ReposSection repos={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it("shows current_branch (fallback to default_branch) in read-only mode", () => {
    render(
      <ReposSection
        repos={[
          makeRepo("backend", {
            current_branch: "feature-x",
            default_branch: "main",
          }),
          makeRepo("frontend", { default_branch: "develop" }),
        ]}
      />,
    );
    expect(screen.getByText("feature-x")).toBeInTheDocument();
    expect(screen.getByText("develop")).toBeInTheDocument();
    // No edit button in read-only mode
    expect(screen.queryByRole("button", { name: /click to edit/i })).toBeNull();
  });

  it("does not show edit button when workspaceId is absent", () => {
    render(
      <ReposSection
        repos={[makeRepo("backend")]}
        onDefaultBranchChange={vi.fn()}
      />,
    );
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("does not show edit button when onDefaultBranchChange is absent", () => {
    render(<ReposSection repos={[makeRepo("backend")]} workspaceId="ws-1" />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("opens inline input pre-filled with default_branch on click", () => {
    render(
      <ReposSection
        repos={[makeRepo("backend", { default_branch: "main" })]}
        workspaceId="ws-1"
        onDefaultBranchChange={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /main/ }));
    const input = screen.getByLabelText(
      "Default branch for backend",
    ) as HTMLInputElement;
    expect(input).toBeInTheDocument();
    expect(input.value).toBe("main");
  });

  it("commits new branch on Enter", () => {
    const onChange = vi.fn();
    render(
      <ReposSection
        repos={[makeRepo("backend", { default_branch: "main" })]}
        workspaceId="ws-1"
        onDefaultBranchChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /main/ }));
    const input = screen.getByLabelText("Default branch for backend");
    fireEvent.change(input, { target: { value: "develop" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("backend", "develop");
  });

  it("cancels edit on Escape without calling callback", () => {
    const onChange = vi.fn();
    render(
      <ReposSection
        repos={[makeRepo("backend", { default_branch: "main" })]}
        workspaceId="ws-1"
        onDefaultBranchChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /main/ }));
    const input = screen.getByLabelText("Default branch for backend");
    fireEvent.change(input, { target: { value: "develop" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(onChange).not.toHaveBeenCalled();
    // Edit closed → button back
    expect(screen.getByRole("button", { name: /main/ })).toBeInTheDocument();
  });

  it("skips callback when value is unchanged", () => {
    const onChange = vi.fn();
    render(
      <ReposSection
        repos={[makeRepo("backend", { default_branch: "main" })]}
        workspaceId="ws-1"
        onDefaultBranchChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /main/ }));
    const input = screen.getByLabelText("Default branch for backend");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("skips callback when value is empty", () => {
    const onChange = vi.fn();
    render(
      <ReposSection
        repos={[makeRepo("backend", { default_branch: "main" })]}
        workspaceId="ws-1"
        onDefaultBranchChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /main/ }));
    const input = screen.getByLabelText("Default branch for backend");
    fireEvent.change(input, { target: { value: "   " } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).not.toHaveBeenCalled();
  });
});
