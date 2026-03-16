/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for slash command registry: parsing, formatting, and command handlers.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

import {
  parseSlashCommand,
  formatSystemMessage,
  COMMAND_REGISTRY,
} from "../slashCommands";

// Mock the API layer
vi.mock("@/api/issues", () => ({
  createIssue: vi.fn(),
  updateIssue: vi.fn(),
  getIssue: vi.fn(),
  getStats: vi.fn(),
}));

import { createIssue, updateIssue, getIssue, getStats } from "@/api/issues";

const mockedCreateIssue = vi.mocked(createIssue);
const mockedUpdateIssue = vi.mocked(updateIssue);
const mockedGetIssue = vi.mocked(getIssue);
const mockedGetStats = vi.mocked(getStats);

beforeEach(() => {
  vi.clearAllMocks();
});

// ============= parseSlashCommand =============

describe("parseSlashCommand", () => {
  it("parses /create-issue with args", () => {
    const result = parseSlashCommand("/create-issue Fix the login bug");
    expect(result).toEqual({
      name: "create-issue",
      args: "Fix the login bug",
    });
  });

  it("parses /assign with args", () => {
    const result = parseSlashCommand("/assign ISSUE-1 alice");
    expect(result).toEqual({ name: "assign", args: "ISSUE-1 alice" });
  });

  it("parses /status with args", () => {
    const result = parseSlashCommand("/status ISSUE-42");
    expect(result).toEqual({ name: "status", args: "ISSUE-42" });
  });

  it("parses /help with no args", () => {
    const result = parseSlashCommand("/help");
    expect(result).toEqual({ name: "help", args: "" });
  });

  it("parses /help with a subcommand arg", () => {
    const result = parseSlashCommand("/help create-issue");
    expect(result).toEqual({ name: "help", args: "create-issue" });
  });

  it("trims surrounding whitespace before parsing", () => {
    const result = parseSlashCommand("  /status ISSUE-1  ");
    expect(result).toEqual({ name: "status", args: "ISSUE-1" });
  });

  it("returns null for non-slash input", () => {
    expect(parseSlashCommand("hello world")).toBeNull();
  });

  it("returns null for empty input", () => {
    expect(parseSlashCommand("")).toBeNull();
  });

  it("returns null for whitespace-only input", () => {
    expect(parseSlashCommand("   ")).toBeNull();
  });

  it("parses command with no args (name only)", () => {
    const result = parseSlashCommand("/status");
    expect(result).toEqual({ name: "status", args: "" });
  });
});

// ============= formatSystemMessage =============

describe("formatSystemMessage", () => {
  const green = "\x1b[32m";
  const red = "\x1b[31m";
  const cyan = "\x1b[36m";
  const reset = "\x1b[0m";

  it("formats success messages with green ANSI color", () => {
    const result = formatSystemMessage("Created issue", "success");
    expect(result).toBe(`${green}[system]${reset} Created issue`);
  });

  it("formats error messages with red ANSI color", () => {
    const result = formatSystemMessage("Something failed", "error");
    expect(result).toBe(`${red}[system]${reset} Something failed`);
  });

  it("formats info messages with cyan ANSI color", () => {
    const result = formatSystemMessage("FYI", "info");
    expect(result).toBe(`${cyan}[system]${reset} FYI`);
  });

  it("handles multi-line text with \\r\\n between lines", () => {
    const result = formatSystemMessage("line one\nline two", "info");
    expect(result).toBe(
      `${cyan}[system]${reset} line one\r\n${cyan}[system]${reset} line two`,
    );
  });

  it("handles empty text", () => {
    const result = formatSystemMessage("", "success");
    expect(result).toBe(`${green}[system]${reset} `);
  });
});

// ============= Command Handlers =============

describe("COMMAND_REGISTRY", () => {
  it("contains all 4 commands", () => {
    expect(COMMAND_REGISTRY.size).toBe(4);
    expect(COMMAND_REGISTRY.has("create-issue")).toBe(true);
    expect(COMMAND_REGISTRY.has("assign")).toBe(true);
    expect(COMMAND_REGISTRY.has("status")).toBe(true);
    expect(COMMAND_REGISTRY.has("help")).toBe(true);
  });
});

describe("/create-issue handler", () => {
  const execute = COMMAND_REGISTRY.get("create-issue")!.execute;

  it("calls createIssue with correct arguments (default priority and type)", async () => {
    mockedCreateIssue.mockResolvedValue({
      id: "ISSUE-1",
      title: "Fix login",
      priority: 3,
      issue_type: "task",
      created_at: "",
      updated_at: "",
    });

    const result = await execute("Fix login");

    expect(mockedCreateIssue).toHaveBeenCalledWith({
      title: "Fix login",
      priority: 3,
      issue_type: "task",
    });
    expect(result).toContain("Created");
    expect(result).toContain("ISSUE-1");
    expect(result).toContain("Fix login");
  });

  it("parses --priority flag correctly", async () => {
    mockedCreateIssue.mockResolvedValue({
      id: "ISSUE-2",
      title: "Critical bug",
      priority: 0,
      issue_type: "task",
      created_at: "",
      updated_at: "",
    });

    await execute("Critical bug --priority 0");

    expect(mockedCreateIssue).toHaveBeenCalledWith({
      title: "Critical bug",
      priority: 0,
      issue_type: "task",
    });
  });

  it("parses --type flag correctly", async () => {
    mockedCreateIssue.mockResolvedValue({
      id: "ISSUE-3",
      title: "Login broken",
      priority: 3,
      issue_type: "bug",
      created_at: "",
      updated_at: "",
    });

    await execute("Login broken --type bug");

    expect(mockedCreateIssue).toHaveBeenCalledWith({
      title: "Login broken",
      priority: 3,
      issue_type: "bug",
    });
  });

  it("parses both --priority and --type flags together", async () => {
    mockedCreateIssue.mockResolvedValue({
      id: "ISSUE-4",
      title: "Plan release",
      priority: 1,
      issue_type: "epic",
      created_at: "",
      updated_at: "",
    });

    await execute("Plan release --priority 1 --type epic");

    expect(mockedCreateIssue).toHaveBeenCalledWith({
      title: "Plan release",
      priority: 1,
      issue_type: "epic",
    });
  });

  it("returns usage info when called with no args", async () => {
    const result = await execute("");
    expect(result).toContain("Usage");
    expect(result).toContain("/create-issue");
    expect(mockedCreateIssue).not.toHaveBeenCalled();
  });

  it("returns error for invalid priority", async () => {
    const result = await execute("Title --priority 9");
    expect(result).toContain("Invalid priority");
    expect(result).toContain("\x1b[31m"); // red
    expect(mockedCreateIssue).not.toHaveBeenCalled();
  });

  it("returns error for invalid type", async () => {
    const result = await execute("Title --type story");
    expect(result).toContain("Invalid type");
    expect(result).toContain("\x1b[31m"); // red
    expect(mockedCreateIssue).not.toHaveBeenCalled();
  });

  it("propagates API errors as error-styled messages", async () => {
    mockedCreateIssue.mockRejectedValue(new Error("Network error"));

    // The handler itself does not catch; the interceptor catches.
    // The handler will throw.
    await expect(execute("Some title")).rejects.toThrow("Network error");
  });
});

describe("/assign handler", () => {
  const execute = COMMAND_REGISTRY.get("assign")!.execute;

  it("calls updateIssue with issue id and assignee", async () => {
    mockedUpdateIssue.mockResolvedValue({
      id: "ISSUE-5",
      title: "Fix bug",
      priority: 2,
      created_at: "",
      updated_at: "",
    });

    const result = await execute("ISSUE-5 alice");

    expect(mockedUpdateIssue).toHaveBeenCalledWith("ISSUE-5", {
      assignee: "alice",
    });
    expect(result).toContain("Assigned");
    expect(result).toContain("ISSUE-5");
    expect(result).toContain("alice");
  });

  it("returns usage info when called with insufficient args", async () => {
    const result = await execute("ISSUE-5");
    expect(result).toContain("Usage");
    expect(result).toContain("/assign");
    expect(mockedUpdateIssue).not.toHaveBeenCalled();
  });

  it("returns usage info when called with no args", async () => {
    const result = await execute("");
    expect(result).toContain("Usage");
    expect(mockedUpdateIssue).not.toHaveBeenCalled();
  });

  it("propagates API errors", async () => {
    mockedUpdateIssue.mockRejectedValue(new Error("Not found"));
    await expect(execute("BAD-ID alice")).rejects.toThrow("Not found");
  });
});

describe("/status handler", () => {
  const execute = COMMAND_REGISTRY.get("status")!.execute;

  it("calls getStats() when invoked with no args", async () => {
    mockedGetStats.mockResolvedValue({
      total_issues: 100,
      open_issues: 40,
      in_progress_issues: 20,
      closed_issues: 30,
      blocked_issues: 5,
      deferred_issues: 2,
      ready_issues: 3,
      tombstone_issues: 0,
      pinned_issues: 0,
      epics_eligible_for_closure: 0,
      average_lead_time_hours: 0,
    });

    const result = await execute("");

    expect(mockedGetStats).toHaveBeenCalled();
    expect(mockedGetIssue).not.toHaveBeenCalled();
    expect(result).toContain("Total: 100");
    expect(result).toContain("Open: 40");
    expect(result).toContain("In Progress: 20");
  });

  it("calls getIssue(id) when invoked with an issue id", async () => {
    mockedGetIssue.mockResolvedValue({
      id: "ISSUE-7",
      title: "Implement login",
      status: "in-progress",
      priority: 2,
      issue_type: "task",
      assignee: "bob",
      description: "Login page needs rework",
      created_at: "",
      updated_at: "",
    } as any);

    const result = await execute("ISSUE-7");

    expect(mockedGetIssue).toHaveBeenCalledWith("ISSUE-7");
    expect(mockedGetStats).not.toHaveBeenCalled();
    expect(result).toContain("ISSUE-7");
    expect(result).toContain("Implement login");
    expect(result).toContain("in-progress");
    expect(result).toContain("bob");
  });

  it("propagates API errors from getStats", async () => {
    mockedGetStats.mockRejectedValue(new Error("Server error"));
    await expect(execute("")).rejects.toThrow("Server error");
  });

  it("propagates API errors from getIssue", async () => {
    mockedGetIssue.mockRejectedValue(new Error("Issue not found"));
    await expect(execute("BAD-ID")).rejects.toThrow("Issue not found");
  });
});

describe("/help handler", () => {
  const execute = COMMAND_REGISTRY.get("help")!.execute;

  it("lists all available commands when called with no args", async () => {
    const result = await execute("");
    expect(result).toContain("Available commands:");
    expect(result).toContain("/create-issue");
    expect(result).toContain("/assign");
    expect(result).toContain("/status");
    expect(result).toContain("/help");
    expect(result).toContain("Type /help <command> for detailed usage.");
  });

  it("shows specific usage for /help create-issue", async () => {
    const result = await execute("create-issue");
    expect(result).toContain("/create-issue");
    expect(result).toContain("Create a new issue");
    expect(result).toContain("Usage:");
  });

  it("shows specific usage for /help assign", async () => {
    const result = await execute("assign");
    expect(result).toContain("/assign");
    expect(result).toContain("Assign an issue to someone");
  });

  it("shows error for unknown command name", async () => {
    const result = await execute("nonexistent");
    expect(result).toContain("Unknown command");
    expect(result).toContain("\x1b[31m"); // red
  });
});
