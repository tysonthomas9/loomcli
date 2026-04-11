/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ActiveAllToggle component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { ActiveAllToggle } from "../ActiveAllToggle";

describe("ActiveAllToggle", () => {
  it("renders both Active and All segments", () => {
    render(<ActiveAllToggle value="active" onChange={vi.fn()} />);
    expect(screen.getByText("Active")).toBeInTheDocument();
    expect(screen.getByText("All")).toBeInTheDocument();
  });

  it("marks Active as checked when value is active", () => {
    render(<ActiveAllToggle value="active" onChange={vi.fn()} />);
    expect(screen.getByText("Active")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByText("All")).toHaveAttribute("aria-checked", "false");
  });

  it("marks All as checked when value is all", () => {
    render(<ActiveAllToggle value="all" onChange={vi.fn()} />);
    expect(screen.getByText("Active")).toHaveAttribute("aria-checked", "false");
    expect(screen.getByText("All")).toHaveAttribute("aria-checked", "true");
  });

  it("calls onChange with 'all' when clicking All segment", () => {
    const onChange = vi.fn();
    render(<ActiveAllToggle value="active" onChange={onChange} />);
    fireEvent.click(screen.getByText("All"));
    expect(onChange).toHaveBeenCalledWith("all");
  });

  it("calls onChange with 'active' when clicking Active segment", () => {
    const onChange = vi.fn();
    render(<ActiveAllToggle value="all" onChange={onChange} />);
    fireEvent.click(screen.getByText("Active"));
    expect(onChange).toHaveBeenCalledWith("active");
  });

  it("has radiogroup role on container", () => {
    render(<ActiveAllToggle value="active" onChange={vi.fn()} />);
    expect(screen.getByRole("radiogroup")).toBeInTheDocument();
  });

  it("has radio role on each segment", () => {
    render(<ActiveAllToggle value="active" onChange={vi.fn()} />);
    const radios = screen.getAllByRole("radio");
    expect(radios).toHaveLength(2);
  });
});
