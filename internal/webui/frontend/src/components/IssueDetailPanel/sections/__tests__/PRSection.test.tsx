/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { describe, expect, it, vi } from "vitest";

import { PRSection } from "../PRSection";

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "WS" }),
}));

vi.mock("@/components/StackContextStrip", () => ({
  StackContextStrip: ({ prKey }: { prKey: string }) => (
    <div data-testid="stack-context-strip" data-pr-key={prKey} />
  ),
}));

describe("PRSection", () => {
  it("shows the stack strip for a validated PR URL and keeps View PR", () => {
    render(
      <PRSection
        issue={{
          status: "review",
          external_ref: "https://github.com/octocat/hello/pull/7",
        }}
      />,
    );

    expect(screen.getByTestId("pr-section")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /View PR/i })).toHaveAttribute(
      "href",
      "https://github.com/octocat/hello/pull/7",
    );
    expect(screen.getByTestId("pr-section-stack")).toBeInTheDocument();
    expect(screen.getByTestId("stack-context-strip")).toHaveAttribute(
      "data-pr-key",
      "github:octocat/hello#7",
    );
  });

  it("does not render a stack strip when there is no PR URL", () => {
    render(<PRSection issue={{ status: "review" }} />);
    expect(screen.getByTestId("pr-section-empty")).toBeInTheDocument();
    expect(screen.queryByTestId("stack-context-strip")).not.toBeInTheDocument();
    expect(screen.queryByTestId("pr-section-stack")).not.toBeInTheDocument();
  });
});
