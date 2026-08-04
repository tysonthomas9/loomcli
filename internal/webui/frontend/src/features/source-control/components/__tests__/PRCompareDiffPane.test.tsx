/**
 * @vitest-environment jsdom
 */

import "@testing-library/jest-dom";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { getPullRequestDiff, type PullRequestDiff } from "../../api/prReview";

import { PRCompareDiffPane } from "../PRCompareDiffPane";

vi.mock("../../api/prReview", () => ({
  getPullRequestDiff: vi.fn(),
}));

const mockGetPullRequestDiff = vi.mocked(getPullRequestDiff);

function makeDiff(overrides: Partial<PullRequestDiff> = {}): PullRequestDiff {
  return {
    files: [
      {
        path: "src/first.ts",
        status: "modified",
        additions: 1,
        deletions: 1,
        patch: "@@ -1,1 +1,1 @@\n-first old\n+first new",
      },
      {
        path: "src/second.ts",
        status: "added",
        additions: 2,
        deletions: 0,
        patch: "@@ -0,0 +1,2 @@\n+second new\n+second next",
      },
    ],
    diff: "diff --git a/src/first.ts b/src/first.ts",
    ...overrides,
  };
}

function renderPane(): ReturnType<typeof render> {
  return render(
    <PRCompareDiffPane
      workspaceId="WS"
      owner="octocat"
      repo="hello"
      number={7}
    />,
  );
}

function lineText(container: HTMLElement, type: string): string {
  return Array.from(container.querySelectorAll(`[data-type="${type}"]`))
    .map((line) => line.textContent ?? "")
    .join("\n");
}

describe("PRCompareDiffPane", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("loads the connector diff and renders file rows with the first patch selected", async () => {
    mockGetPullRequestDiff.mockResolvedValueOnce(makeDiff());

    const { container } = renderPane();

    const rows = await screen.findAllByTestId("pr-compare-file-row");
    expect(mockGetPullRequestDiff).toHaveBeenCalledWith(
      "WS",
      "octocat",
      "hello",
      7,
    );
    expect(screen.getByTestId("pr-compare-diff-pane")).toBeInTheDocument();
    expect(screen.getByText("Files changed")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(screen.getByText("+3")).toBeInTheDocument();
    expect(screen.getAllByText("−1").length).toBeGreaterThan(0);
    expect(rows).toHaveLength(2);
    expect(rows[0] as HTMLElement).toHaveTextContent("src/first.ts");
    expect(rows[1] as HTMLElement).toHaveTextContent("src/second.ts");
    expect(lineText(container, "add")).toContain("+first new");
    expect(lineText(container, "del")).toContain("-first old");
  });

  it("selects a file row and shows that file's patch", async () => {
    mockGetPullRequestDiff.mockResolvedValueOnce(makeDiff());

    const { container } = renderPane();
    const rows = await screen.findAllByTestId("pr-compare-file-row");

    fireEvent.click(rows[1] as HTMLElement);

    expect(
      screen.getByRole("heading", { name: /src\/second\.ts/ }),
    ).toBeInTheDocument();
    expect(lineText(container, "add")).toContain("+second new");
    expect(lineText(container, "add")).not.toContain("+first new");
  });

  it("shows a loading state while the diff request is pending", async () => {
    let resolveDiff: (diff: PullRequestDiff) => void = () => {};
    mockGetPullRequestDiff.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveDiff = resolve;
      }),
    );

    renderPane();

    expect(screen.getByText("Loading diff…")).toBeInTheDocument();

    await act(async () => {
      resolveDiff(makeDiff());
    });

    expect(await screen.findAllByTestId("pr-compare-file-row")).toHaveLength(2);
  });

  it("shows an empty state when the connector diff has no files", async () => {
    mockGetPullRequestDiff.mockResolvedValueOnce(
      makeDiff({ files: [], diff: "" }),
    );

    renderPane();

    expect(
      await screen.findByText("No files changed in this pull request."),
    ).toBeInTheDocument();
    expect(screen.queryAllByTestId("pr-compare-file-row")).toHaveLength(0);
  });

  it("shows an error state when the connector diff fails to load", async () => {
    mockGetPullRequestDiff.mockRejectedValueOnce(
      new Error("Connector unavailable"),
    );

    renderPane();

    expect(
      await screen.findByText("Connector unavailable"),
    ).toBeInTheDocument();
  });
});
