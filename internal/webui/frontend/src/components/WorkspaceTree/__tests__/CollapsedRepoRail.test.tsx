/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { CollapsedRepoRail } from "../CollapsedRepoRail";

describe("CollapsedRepoRail", () => {
  it("renders repo pills and add button", () => {
    const onAddRepo = vi.fn();
    render(
      <CollapsedRepoRail
        repos={[
          {
            name: "hello-world",
            path: "/tmp/hello-world",
            default_branch: "main",
            remote: "origin",
          },
        ]}
        onAddRepo={onAddRepo}
      />,
    );

    expect(screen.getByTestId("collapsed-repo-rail")).toBeInTheDocument();
    expect(screen.getByLabelText("hello-world")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add repo" }),
    ).toBeInTheDocument();
  });

  it("returns null when there are no repos and no add handler", () => {
    const { container } = render(<CollapsedRepoRail repos={[]} />);
    expect(container.firstChild).toBeNull();
  });
});
