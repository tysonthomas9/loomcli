/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { OnboardingFlow, type OnboardingStep } from "../OnboardingFlow";

const steps: OnboardingStep[] = [
  {
    id: "workspace-repo",
    title: "Create workspace with repo",
    description: "Create the first workspace.",
    status: "complete",
  },
  {
    id: "verify-repo",
    title: "Verify repository",
    description: "Check repository readiness.",
    status: "current",
    actionLabel: "Verify Repo",
    onAction: vi.fn(),
  },
  {
    id: "setup-backend",
    title: "Set up AI CLI",
    description: "Install or login to the CLI.",
    status: "actionable",
    actionLabel: "Open Terminal",
    onAction: vi.fn(),
  },
  {
    id: "create-agent",
    title: "Create agent",
    description: "Create a planner agent.",
    status: "blocked",
    actionLabel: "Create Agent",
    onAction: vi.fn(),
  },
  {
    id: "create-issue",
    title: "Create first issue",
    description: "Create the first issue.",
    status: "blocked",
  },
];

describe("OnboardingFlow", () => {
  it("renders the five setup steps with progress and the sample repo", () => {
    render(
      <OnboardingFlow
        repoUrl="https://github.com/octocat/Hello-World"
        steps={steps}
      />,
    );

    expect(screen.getByTestId("onboarding-flow")).toBeInTheDocument();
    expect(screen.getByText("1/5")).toBeInTheDocument();
    expect(
      screen.getByText("https://github.com/octocat/Hello-World"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Create workspace with repo" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Verify repository" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Set up AI CLI" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Create agent" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Create first issue" }),
    ).toBeInTheDocument();
  });

  it("fires available actions and keeps blocked actions disabled", () => {
    const onCurrent = vi.fn();
    const onBlocked = vi.fn();
    render(
      <OnboardingFlow
        repoUrl="https://github.com/octocat/Hello-World"
        steps={[
          {
            id: "current",
            title: "Current",
            description: "Current step",
            status: "current",
            actionLabel: "Run Current",
            onAction: onCurrent,
          },
          {
            id: "blocked",
            title: "Blocked",
            description: "Blocked step",
            status: "blocked",
            actionLabel: "Run Blocked",
            onAction: onBlocked,
          },
        ]}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Run Current" }));
    expect(onCurrent).toHaveBeenCalledOnce();

    const blockedButton = screen.getByRole("button", { name: "Run Blocked" });
    expect(blockedButton).toBeDisabled();
    fireEvent.click(blockedButton);
    expect(onBlocked).not.toHaveBeenCalled();
  });
});
