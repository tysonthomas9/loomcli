/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { PRSection } from "../PRSection";

describe("PRSection", () => {
  it("shows the no-PR placeholder on a plan-review task", () => {
    render(<PRSection issue={{ status: "review", labels: [] }} />);
    expect(screen.getByText(/No pull request yet/i)).toBeTruthy();
  });

  it("hides the placeholder on a task whose code awaits review in Loom", () => {
    const { container } = render(
      <PRSection issue={{ status: "review", labels: ["code-review"] }} />,
    );
    expect(screen.queryByText(/No pull request yet/i)).toBeNull();
    expect(container.innerHTML).toBe("");
  });
});
