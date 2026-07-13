/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { DiffFilePatch } from "@/api/issues";

import { GitDiffViewer } from "../GitDiffViewer";

function makePatch(overrides: Partial<DiffFilePatch> = {}): DiffFilePatch {
  return {
    patch: "",
    is_binary: false,
    is_too_large: false,
    additions: 0,
    deletions: 0,
    ...overrides,
  };
}

describe("GitDiffViewer", () => {
  it("shows loading state", () => {
    render(<GitDiffViewer patch={null} isLoading={true} />);
    expect(screen.getByText(/Loading diff/)).toBeInTheDocument();
  });

  it("shows error message", () => {
    render(
      <GitDiffViewer patch={null} isLoading={false} error="Failed to load" />,
    );
    expect(screen.getByText("Failed to load")).toBeInTheDocument();
  });

  it("shows 'No changes' when patch is null", () => {
    render(<GitDiffViewer patch={null} isLoading={false} />);
    expect(screen.getByText("No changes")).toBeInTheDocument();
  });

  it("shows binary file message for binary patches", () => {
    render(
      <GitDiffViewer
        patch={makePatch({ is_binary: true })}
        isLoading={false}
      />,
    );
    expect(
      screen.getByText(/Binary file.*cannot display diff/),
    ).toBeInTheDocument();
  });

  it("shows too large message for oversized patches", () => {
    render(
      <GitDiffViewer
        patch={makePatch({ is_too_large: true })}
        isLoading={false}
      />,
    );
    expect(screen.getByText("File too large to display")).toBeInTheDocument();
  });

  it("shows 'No changes' when patch string is empty", () => {
    render(<GitDiffViewer patch={makePatch()} isLoading={false} />);
    expect(screen.getByText("No changes")).toBeInTheDocument();
  });

  it("prioritizes loading over error", () => {
    render(<GitDiffViewer patch={null} isLoading={true} error="Some error" />);
    expect(screen.getByText(/Loading diff/)).toBeInTheDocument();
    expect(screen.queryByText("Some error")).not.toBeInTheDocument();
  });

  it("renders added and deleted lines from a hunk-only patch", async () => {
    const { container } = render(
      <GitDiffViewer
        filePath="src/main.go"
        patch={makePatch({
          patch: "@@ -1,2 +1,2 @@\n-removed line\n+added line",
          additions: 1,
          deletions: 1,
        })}
        isLoading={false}
      />,
    );

    await waitFor(() => {
      expect(container).toHaveTextContent("removed line");
      expect(container).toHaveTextContent("added line");
    });
  });
});
