import { describe, it, expect } from "vitest";

import type { Issue } from "@/types";

import {
  epicMetadataMatchesSearch,
  filterIssuesBySearch,
  issueMatchesSearch,
} from "../issueSearch";

function createIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: overrides.id ?? "issue-1",
    title: overrides.title ?? "Test Issue",
    priority: overrides.priority ?? 2,
    created_at: overrides.created_at ?? "2024-01-01T00:00:00Z",
    updated_at: overrides.updated_at ?? "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("issueMatchesSearch", () => {
  it("matches task id", () => {
    const issue = createIssue({ id: "HELLO-WORLD-1", title: "Implement API" });
    expect(issueMatchesSearch(issue, "HELLO-WORLD-1")).toBe(true);
    expect(issueMatchesSearch(issue, "hello-world")).toBe(true);
  });

  it("matches epic id", () => {
    const epic = createIssue({
      id: "HELLO-WORLD-2",
      title: "Build the Hello World web app",
      issue_type: "epic",
    });
    expect(issueMatchesSearch(epic, "HELLO-WORLD-2")).toBe(true);
  });

  it("matches parent id and parent title on child tasks", () => {
    const task = createIssue({
      id: "task-1",
      title: "Child Task",
      parent: "HELLO-WORLD-2",
      parent_title: "Build the Hello World web app",
    });
    expect(issueMatchesSearch(task, "HELLO-WORLD-2")).toBe(true);
    expect(issueMatchesSearch(task, "Hello World web")).toBe(true);
  });

  it("matches title, description, notes, assignee, status, and labels", () => {
    const issue = createIssue({
      title: "Fix login",
      description: "Auth bug",
      notes: "Check OAuth",
      assignee: "alice",
      status: "in_progress",
      labels: ["frontend"],
    });
    expect(issueMatchesSearch(issue, "login")).toBe(true);
    expect(issueMatchesSearch(issue, "auth")).toBe(true);
    expect(issueMatchesSearch(issue, "oauth")).toBe(true);
    expect(issueMatchesSearch(issue, "alice")).toBe(true);
    expect(issueMatchesSearch(issue, "progress")).toBe(true);
    expect(issueMatchesSearch(issue, "frontend")).toBe(true);
  });
});

describe("filterIssuesBySearch", () => {
  const epic = createIssue({
    id: "HELLO-WORLD-2",
    title: "Build the Hello World web app",
    issue_type: "epic",
    status: "open",
  });
  const taskA = createIssue({
    id: "HELLO-WORLD-1",
    title: "Implement API",
    issue_type: "task",
    parent: "HELLO-WORLD-2",
    parent_title: "Build the Hello World web app",
    status: "open",
  });
  const taskB = createIssue({
    id: "task-2",
    title: "Write docs",
    issue_type: "task",
    parent: "HELLO-WORLD-2",
    parent_title: "Build the Hello World web app",
    status: "open",
  });
  const unrelated = createIssue({
    id: "OTHER-1",
    title: "Unrelated work",
    issue_type: "task",
    status: "open",
  });
  const issues = [epic, taskA, taskB, unrelated];

  it("finds a task by id", () => {
    const result = filterIssuesBySearch(issues, "HELLO-WORLD-1");
    expect(result.map((issue) => issue.id)).toEqual(["HELLO-WORLD-1"]);
  });

  it("finds an epic by id and keeps all child tasks visible", () => {
    const result = filterIssuesBySearch(issues, "HELLO-WORLD-2");
    expect(result.map((issue) => issue.id).sort()).toEqual(
      ["HELLO-WORLD-1", "HELLO-WORLD-2", "task-2"].sort(),
    );
  });

  it("finds an epic by title and keeps all child tasks visible", () => {
    const result = filterIssuesBySearch(issues, "Hello World web");
    expect(result.map((issue) => issue.id).sort()).toEqual(
      ["HELLO-WORLD-1", "HELLO-WORLD-2", "task-2"].sort(),
    );
  });

  it("returns all issues when search term is empty", () => {
    expect(filterIssuesBySearch(issues, "")).toEqual(issues);
    expect(filterIssuesBySearch(issues, "   ")).toEqual(issues);
  });
});

describe("epicMetadataMatchesSearch", () => {
  it("matches parent id and parent title independently", () => {
    const byParent = createIssue({ parent: "EPIC-42" });
    const byTitle = createIssue({ parent_title: "Platform rollout" });
    expect(epicMetadataMatchesSearch(byParent, "epic-42")).toBe(true);
    expect(epicMetadataMatchesSearch(byTitle, "platform")).toBe(true);
  });
});
