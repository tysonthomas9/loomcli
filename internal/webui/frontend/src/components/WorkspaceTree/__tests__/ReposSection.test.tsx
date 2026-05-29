/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import "@testing-library/jest-dom";
import type { RepoInfo } from "@/api/workspace";

import { ReposSection } from "../ReposSection";

function repo(overrides: Partial<RepoInfo> = {}): RepoInfo {
  return {
    name: "alpha",
    path: "/repos/alpha",
    default_branch: "main",
    remote: "origin",
    groups: [],
    ...overrides,
  };
}

describe("ReposSection", () => {
  it("renders repo name and branch", () => {
    render(<ReposSection repos={[repo()]} />);

    expect(screen.getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("main")).toBeInTheDocument();
    expect(screen.queryByText("Not cloned locally")).not.toBeInTheDocument();
  });

  it("marks repos without a local checkout path", () => {
    render(<ReposSection repos={[repo({ name: "fleet-db", path: "" })]} />);

    expect(screen.getByText("fleet-db")).toBeInTheDocument();
    expect(screen.getByText("Not cloned locally")).toBeInTheDocument();
    expect(
      screen.getByTitle("No local checkout path registered for fleet-db"),
    ).toBeInTheDocument();
  });
});
