/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { AppConfigError } from "@/api/appConfig";
import { BootError } from "../BootError";

describe("BootError", () => {
  it("renders error message from AppConfigError", () => {
    const error = new AppConfigError("Failed to load configuration");
    render(<BootError error={error} onRetry={vi.fn()} />);

    expect(
      screen.getByText("Failed to load configuration"),
    ).toBeInTheDocument();
  });

  it("renders error message from generic Error", () => {
    const error = new Error("Something went wrong");
    render(<BootError error={error} onRetry={vi.fn()} />);

    expect(screen.getByText("Something went wrong")).toBeInTheDocument();
  });

  it("renders fallback message for non-Error values", () => {
    render(<BootError error="string error" onRetry={vi.fn()} />);

    expect(
      screen.getByText("An unexpected error occurred"),
    ).toBeInTheDocument();
  });

  it("renders retry button", () => {
    render(<BootError error={new Error("fail")} onRetry={vi.fn()} />);

    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });

  it("calls onRetry when retry button is clicked", () => {
    const onRetry = vi.fn();
    render(<BootError error={new Error("fail")} onRetry={onRetry} />);

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('has role="alert" on overlay', () => {
    render(<BootError error={new Error("fail")} onRetry={vi.fn()} />);

    const alert = screen.getByRole("alert");
    expect(alert).toBeInTheDocument();
  });

  it("renders the heading", () => {
    render(<BootError error={new Error("fail")} onRetry={vi.fn()} />);

    expect(screen.getByText("Unable to start application")).toBeInTheDocument();
  });
});
