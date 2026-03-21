/**
 * Slash command registry and execution for terminal view.
 * Client-side intercepted commands: /create-issue, /assign, /status, /help.
 * Calls existing REST API endpoints.
 */

import { createIssue, updateIssue, getIssue, getStats } from "@/api/issues";
import { KNOWN_ISSUE_TYPES } from "@/types";
import type { Priority, IssueType } from "@/types";

export interface SlashCommand {
  name: string;
  description: string;
  usage: string;
  execute: (args: string) => Promise<string>;
}

/**
 * Format a system message with ANSI styling.
 */
export function formatSystemMessage(
  text: string,
  type: "success" | "error" | "info",
): string {
  const colors: Record<string, string> = {
    success: "\x1b[32m", // green
    error: "\x1b[31m", // red
    info: "\x1b[36m", // cyan
  };
  const reset = "\x1b[0m";
  const color = colors[type];
  const lines = text.split("\n");
  return lines.map((line) => `${color}[system]${reset} ${line}`).join("\r\n");
}

/**
 * Parse a slash command from a line of input.
 * Returns null if the line doesn't start with '/'.
 */
export function parseSlashCommand(
  line: string,
): { name: string; args: string } | null {
  const trimmed = line.trim();
  if (!trimmed.startsWith("/")) return null;
  const spaceIdx = trimmed.indexOf(" ");
  if (spaceIdx === -1) {
    return { name: trimmed.slice(1), args: "" };
  }
  return {
    name: trimmed.slice(1, spaceIdx),
    args: trimmed.slice(spaceIdx + 1).trim(),
  };
}

const VALID_PRIORITIES = new Set(["0", "1", "2", "3", "4"]);
const VALID_TYPES = new Set<string>(KNOWN_ISSUE_TYPES);

/**
 * Parse --flag value pairs from an args string.
 * Returns the positional text (before any flags) and a map of flag values.
 */
function parseFlags(args: string): {
  positional: string;
  flags: Map<string, string>;
} {
  const flags = new Map<string, string>();
  const parts: string[] = [];
  const tokens = args.split(/\s+/);
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i]!;
    const next = tokens[i + 1];
    if (token.startsWith("--")) {
      if (next === undefined) {
        // Trailing flag with no value — treat as error by ignoring it
        // (the caller will validate required flags and show usage)
        continue;
      }
      flags.set(token.slice(2), next);
      i++; // skip the value token
    } else {
      parts.push(token);
    }
  }
  return { positional: parts.join(" "), flags };
}

async function handleCreateIssue(args: string): Promise<string> {
  if (!args.trim()) {
    return formatSystemMessage(
      `Usage: /create-issue <title> [--priority 0-4] [--type ${[...VALID_TYPES].join("|")}]`,
      "info",
    );
  }
  const { positional: title, flags } = parseFlags(args);
  if (!title) {
    return formatSystemMessage(
      `Usage: /create-issue <title> [--priority 0-4] [--type ${[...VALID_TYPES].join("|")}]`,
      "info",
    );
  }

  let priority: Priority = 3;
  const priorityStr = flags.get("priority");
  if (priorityStr) {
    if (!VALID_PRIORITIES.has(priorityStr)) {
      return formatSystemMessage(
        `Invalid priority "${priorityStr}". Use 0-4 (P0=critical, P4=lowest).`,
        "error",
      );
    }
    priority = parseInt(priorityStr, 10) as Priority;
  }

  let issueType: IssueType = "task";
  const typeStr = flags.get("type");
  if (typeStr) {
    if (!VALID_TYPES.has(typeStr)) {
      return formatSystemMessage(
        `Invalid type "${typeStr}". Use: ${[...VALID_TYPES].join(", ")}.`,
        "error",
      );
    }
    issueType = typeStr as IssueType;
  }

  const issue = await createIssue({
    title,
    priority,
    issue_type: issueType,
  });
  return formatSystemMessage(
    `Created ${issue.issue_type} ${issue.id}: ${issue.title} [P${issue.priority}]`,
    "success",
  );
}

async function handleAssign(args: string): Promise<string> {
  const parts = args.trim().split(/\s+/);
  if (parts.length < 2 || !parts[0] || !parts[1]) {
    return formatSystemMessage("Usage: /assign <issue-id> <assignee>", "info");
  }
  const [issueId, assignee] = parts;
  await updateIssue(issueId, { assignee });
  return formatSystemMessage(`Assigned ${issueId} to ${assignee}`, "success");
}

async function handleStatus(args: string): Promise<string> {
  const issueId = args.trim();
  if (issueId) {
    const issue = await getIssue(issueId);
    const lines = [
      `${issue.id}: ${issue.title}`,
      `  Status: ${issue.status}  Priority: P${issue.priority}  Type: ${issue.issue_type}`,
    ];
    if (issue.assignee) lines.push(`  Assignee: ${issue.assignee}`);
    if (issue.description) lines.push(`  ${issue.description}`);
    return formatSystemMessage(lines.join("\n"), "info");
  }

  const stats = await getStats();
  const lines = [
    "Project Status:",
    `  Total: ${stats.total_issues}  Open: ${stats.open_issues}  In Progress: ${stats.in_progress_issues}`,
    `  Closed: ${stats.closed_issues}  Blocked: ${stats.blocked_issues}  Ready: ${stats.ready_issues}`,
  ];
  return formatSystemMessage(lines.join("\n"), "info");
}

function handleHelp(args: string): string {
  const cmdName = args.trim();
  if (cmdName && COMMAND_REGISTRY.has(cmdName)) {
    const cmd = COMMAND_REGISTRY.get(cmdName)!;
    return formatSystemMessage(
      `/${cmd.name} — ${cmd.description}\n  Usage: ${cmd.usage}`,
      "info",
    );
  }
  if (cmdName) {
    return formatSystemMessage(
      `Unknown command "${cmdName}". Type /help for available commands.`,
      "error",
    );
  }
  const lines = ["Available commands:"];
  for (const cmd of COMMAND_REGISTRY.values()) {
    lines.push(`  /${cmd.name.padEnd(16)} ${cmd.description}`);
  }
  lines.push("", "Type /help <command> for detailed usage.");
  return formatSystemMessage(lines.join("\n"), "info");
}

export const COMMAND_REGISTRY: Map<string, SlashCommand> = new Map([
  [
    "create-issue",
    {
      name: "create-issue",
      description: "Create a new issue",
      usage: `/create-issue <title> [--priority 0-4] [--type ${[...VALID_TYPES].join("|")}]`,
      execute: handleCreateIssue,
    },
  ],
  [
    "assign",
    {
      name: "assign",
      description: "Assign an issue to someone",
      usage: "/assign <issue-id> <assignee>",
      execute: handleAssign,
    },
  ],
  [
    "status",
    {
      name: "status",
      description: "Show issue details or project overview",
      usage: "/status [issue-id]",
      execute: handleStatus,
    },
  ],
  [
    "help",
    {
      name: "help",
      description: "Show available commands",
      usage: "/help [command]",
      // help is synchronous but wraps in promise for interface consistency
      execute: async (args: string) => handleHelp(args),
    },
  ],
]);
