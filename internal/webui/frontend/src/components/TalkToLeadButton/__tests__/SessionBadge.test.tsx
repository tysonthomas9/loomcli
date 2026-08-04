/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { SessionBadge } from "../SessionBadge";

describe("SessionBadge", () => {
  it("renders nothing when count is 0", () => {
    const { container } = render(<SessionBadge count={0} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders nothing when count is negative", () => {
    const { container } = render(<SessionBadge count={-1} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders badge with correct number when count > 0", () => {
    render(<SessionBadge count={3} />);
    expect(screen.getByText("3")).toBeInTheDocument();
  });

  it("includes aria-label with session count", () => {
    render(<SessionBadge count={5} />);
    expect(screen.getByLabelText("5 active sessions")).toBeInTheDocument();
  });

  it("renders count of 1", () => {
    render(<SessionBadge count={1} />);
    expect(screen.getByText("1")).toBeInTheDocument();
  });

  it("renders max count of 8", () => {
    render(<SessionBadge count={8} />);
    expect(screen.getByText("8")).toBeInTheDocument();
  });
});
