/**
 * Unit tests verifying that OpenAPI-generated type aliases are structurally
 * correct and that the generated openapi.ts file exports the expected shapes.
 *
 * These tests create objects matching generated schemas and assign them to the
 * aliased types, confirming the aliases resolve correctly at both the type
 * level and runtime.
 */

import { describe, it, expect } from "vitest";

import type { components, paths } from "../generated/openapi";
import type { Comment } from "@/types/issue";
import type { EditorInfo } from "@/types/common";
import type { SessionRecord, TranscriptEntry } from "@/types/agent";
import type { Statistics } from "@/types/issue";
import type {
  UsageSession,
  UsageAgentSummary,
  UsageBackendSummary,
  UsageDailyCost,
  UsageResponse,
} from "@/types/agent";
import type {
  HourlyBucket,
  MetricsSnapshot,
  ObservabilityMetricsResponse,
} from "@/types/agent";
import type { Dependency } from "@/types/issue";
import type { Event } from "@/types/workspace";
import type { Issue, BlockerRef } from "@/types/issue";
import type {
  LoomCommitDetail,
  LoomFileChange,
  LoomAgentStatus,
  LoomAgentsResponse,
  LoomTaskInfo,
  LoomTaskSummary,
  WorktreeSyncDetail,
  LoomSyncInfo,
  LoomStats,
  LoomStatusResponse,
  LoomTasksResponse,
} from "@/types/agent";

// ---------------------------------------------------------------------------
// 1. Generated openapi.ts exports `components` and `paths`
// ---------------------------------------------------------------------------
describe("Generated openapi.ts exports", () => {
  it("components type includes schemas", () => {
    // Type-level: if this compiles, `components` has a `schemas` member.
    const _schema: components["schemas"]["Comment"] = {
      id: 1,
      issue_id: "loom-1",
      author: "alice",
      text: "hello",
      created_at: "2024-01-01T00:00:00Z",
    };
    expect(_schema.id).toBe(1);
  });

  it("paths type includes at least /health", () => {
    // Type-level: if this compiles, `paths` has the /health key.
    type HealthPath = paths["/health"];
    // Runtime: the type exists (no-op assertion).
    const check: boolean = true as HealthPath extends object ? true : false;
    expect(check).toBe(true);
  });

  it("IssueTab generated type includes the built-in sessions tab", () => {
    const tab: components["schemas"]["IssueTab"] = {
      id: "sessions",
      type: "sessions",
      label: "Sessions",
      sort_order: 1,
    };

    expect(tab.type).toBe("sessions");
  });
});

// ---------------------------------------------------------------------------
// 2. comment.ts -- Comment alias
// ---------------------------------------------------------------------------
describe("Comment alias", () => {
  it("has expected fields from generated Comment schema", () => {
    const comment: Comment = {
      id: 42,
      issue_id: "loom-100",
      author: "bob",
      text: "Looks good",
      created_at: "2024-06-15T09:00:00Z",
    };

    expect(comment.id).toBe(42);
    expect(comment.issue_id).toBe("loom-100");
    expect(comment.author).toBe("bob");
    expect(comment.text).toBe("Looks good");
    expect(comment.created_at).toBe("2024-06-15T09:00:00Z");
  });
});

// ---------------------------------------------------------------------------
// 3. editor.ts -- EditorInfo alias
// ---------------------------------------------------------------------------
describe("EditorInfo alias", () => {
  it("has expected fields from generated EditorInfo schema", () => {
    const editor: EditorInfo = {
      id: "vscode",
      display_name: "VS Code",
      icon_name: "vscode-icon",
      detected: true,
    };

    expect(editor.id).toBe("vscode");
    expect(editor.display_name).toBe("VS Code");
    expect(editor.icon_name).toBe("vscode-icon");
    expect(editor.detected).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// 4. session.ts -- SessionRecord uses session_id / task_id (not id)
// ---------------------------------------------------------------------------
describe("SessionRecord alias", () => {
  it("has session_id and task_id fields (not id)", () => {
    const session: SessionRecord = {
      session_id: "sess-abc",
      task_id: "loom-200",
      agent_name: "nova",
      backend: "claude",
      started_at: "2024-07-01T10:00:00Z",
      status: "completed",
      exit_code: 0,
      input_tokens: 1000,
      output_tokens: 500,
      cache_read_tokens: 200,
      cache_write_tokens: 100,
      estimated_cost_usd: 0.05,
      files_changed: 3,
      lines_added: 50,
      lines_removed: 10,
      attempt_num: 1,
    };

    expect(session.session_id).toBe("sess-abc");
    expect(session.task_id).toBe("loom-200");
    // Confirm there is no `id` field at runtime
    expect("id" in session).toBe(false);
  });

  it("supports optional fields", () => {
    const session: SessionRecord = {
      session_id: "sess-def",
      task_id: "loom-201",
      agent_name: "falcon",
      backend: "openai",
      model: "gpt-4",
      phase: "implement",
      started_at: "2024-07-02T08:00:00Z",
      ended_at: "2024-07-02T09:00:00Z",
      duration_s: 3600,
      status: "completed",
      exit_code: 0,
      input_tokens: 2000,
      output_tokens: 1000,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
      estimated_cost_usd: 0.12,
      files_changed: 5,
      lines_added: 120,
      lines_removed: 30,
      files_touched: ["main.go", "handler.go"],
      attempt_num: 2,
      epic_id: "epic-10",
    };

    expect(session.model).toBe("gpt-4");
    expect(session.epic_id).toBe("epic-10");
    expect(session.files_touched).toEqual(["main.go", "handler.go"]);
  });
});

describe("TranscriptEntry alias", () => {
  it("has expected fields from generated TranscriptEntry schema", () => {
    const entry: TranscriptEntry = {
      seq: 1,
      ts: "2024-07-01T10:01:00Z",
      role: "assistant",
      type: "text",
      content: "I will fix the bug.",
    };

    expect(entry.seq).toBe(1);
    expect(entry.role).toBe("assistant");
    expect(entry.type).toBe("text");
    expect(entry.content).toBe("I will fix the bug.");
  });
});

// ---------------------------------------------------------------------------
// 5. statistics.ts -- Statistics alias
// ---------------------------------------------------------------------------
describe("Statistics alias", () => {
  it("has expected fields from generated Statistics schema", () => {
    const stats: Statistics = {
      total_issues: 100,
      open_issues: 40,
      in_progress_issues: 15,
      closed_issues: 30,
      blocked_issues: 5,
      deferred_issues: 3,
      ready_issues: 7,
      tombstone_issues: 0,
      pinned_issues: 2,
      epics_eligible_for_closure: 1,
      average_lead_time_hours: 48.5,
    };

    expect(stats.total_issues).toBe(100);
    expect(stats.open_issues).toBe(40);
    expect(stats.average_lead_time_hours).toBe(48.5);
    expect(stats.epics_eligible_for_closure).toBe(1);
  });
});

// ---------------------------------------------------------------------------
// 6. usage.ts -- UsageSession + 4 more aliases
// ---------------------------------------------------------------------------
describe("Usage type aliases", () => {
  it("UsageSession has expected fields from UsageSessionRecord schema", () => {
    const usage: UsageSession = {
      agent_name: "nova",
      backend: "claude",
      input_tokens: 5000,
      output_tokens: 2000,
      cache_read_tokens: 100,
      cache_write_tokens: 50,
      estimated_cost_usd: 0.25,
      started_at: "2024-08-01T00:00:00Z",
      ended_at: "2024-08-01T01:00:00Z",
      exit_code: 0,
    };

    expect(usage.agent_name).toBe("nova");
    expect(usage.estimated_cost_usd).toBe(0.25);
  });

  it("UsageSession supports optional session_id and task_id", () => {
    const usage: UsageSession = {
      agent_name: "falcon",
      backend: "openai",
      input_tokens: 1000,
      output_tokens: 500,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
      estimated_cost_usd: 0.05,
      started_at: "2024-08-01T00:00:00Z",
      ended_at: "2024-08-01T00:30:00Z",
      exit_code: 0,
      session_id: "sess-123",
      task_id: "loom-50",
    };

    expect(usage.session_id).toBe("sess-123");
    expect(usage.task_id).toBe("loom-50");
  });

  it("UsageAgentSummary has expected fields", () => {
    const summary: UsageAgentSummary = {
      name: "nova",
      sessions: 10,
      input_tokens: 50000,
      output_tokens: 25000,
      total_cost: 5.0,
    };

    expect(summary.name).toBe("nova");
    expect(summary.sessions).toBe(10);
    expect(summary.total_cost).toBe(5.0);
  });

  it("UsageBackendSummary has expected fields", () => {
    const summary: UsageBackendSummary = {
      name: "claude",
      sessions: 20,
      total_cost: 12.5,
    };

    expect(summary.name).toBe("claude");
    expect(summary.total_cost).toBe(12.5);
  });

  it("UsageDailyCost has expected fields", () => {
    const daily: UsageDailyCost = {
      date: "2024-08-01",
      cost: 3.75,
      sessions: 8,
    };

    expect(daily.date).toBe("2024-08-01");
    expect(daily.cost).toBe(3.75);
  });

  it("UsageResponse contains aggregate arrays", () => {
    const response: UsageResponse = {
      total_input_tokens: 100000,
      total_output_tokens: 50000,
      total_cache_read_tokens: 5000,
      total_cache_write_tokens: 2500,
      total_cost: 25.0,
      session_count: 30,
      by_agent: [
        {
          name: "nova",
          sessions: 15,
          input_tokens: 50000,
          output_tokens: 25000,
          total_cost: 12.5,
        },
      ],
      by_backend: [{ name: "claude", sessions: 30, total_cost: 25.0 }],
      daily_costs: [{ date: "2024-08-01", cost: 5.0, sessions: 10 }],
      sessions: [],
      timestamp: "2024-08-01T12:00:00Z",
    };

    expect(response.total_cost).toBe(25.0);
    expect(response.by_agent).toHaveLength(1);
    expect(response.by_backend).toHaveLength(1);
    expect(response.daily_costs).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// 7. observability.ts -- 3 aliases
// ---------------------------------------------------------------------------
describe("Observability type aliases", () => {
  it("HourlyBucket has expected fields", () => {
    const bucket: HourlyBucket = {
      hour: "2024-08-01T14:00:00Z",
      completed: 5,
      failed: 1,
      avg_duration: 120.5,
    };

    expect(bucket.hour).toBe("2024-08-01T14:00:00Z");
    expect(bucket.completed).toBe(5);
    expect(bucket.failed).toBe(1);
  });

  it("MetricsSnapshot has expected fields", () => {
    const snapshot: MetricsSnapshot = {
      timestamp: "2024-08-01T15:00:00Z",
      tasks_completed_last_hour: 3,
      tasks_completed_24h: 42,
      avg_task_duration_sec: 180.0,
      lines_changed_last_hour: 250,
      error_rate_pct: 2.5,
      restart_count_24h: 1,
      restarts_by_agent: { nova: 1 },
      agent_utilization: { nova: 0.85 },
      tasks_by_role: { implement: 10 },
      tasks_by_epic: { "epic-1": 5 },
      tasks_by_agent: { nova: 20 },
      hourly_completions: [],
      total_tasks_completed: 100,
      total_tasks_failed: 5,
    };

    expect(snapshot.tasks_completed_24h).toBe(42);
    expect(snapshot.restarts_by_agent).toEqual({ nova: 1 });
    expect(snapshot.total_tasks_completed).toBe(100);
  });

  it("ObservabilityMetricsResponse wraps MetricsSnapshot", () => {
    const response: ObservabilityMetricsResponse = {
      success: true,
      data: {
        timestamp: "2024-08-01T15:00:00Z",
        tasks_completed_last_hour: 0,
        tasks_completed_24h: 0,
        avg_task_duration_sec: 0,
        lines_changed_last_hour: 0,
        error_rate_pct: 0,
        restart_count_24h: 0,
        restarts_by_agent: {},
        agent_utilization: {},
        tasks_by_role: {},
        tasks_by_epic: {},
        tasks_by_agent: {},
        hourly_completions: [],
        total_tasks_completed: 0,
        total_tasks_failed: 0,
      },
    };

    expect(response.success).toBe(true);
    expect(response.data).toBeDefined();
  });
});

// ---------------------------------------------------------------------------
// 8. dependency.ts -- Dependency alias
// ---------------------------------------------------------------------------
describe("Dependency alias", () => {
  it("has expected fields from generated Dependency schema", () => {
    const dep: Dependency = {
      issue_id: "loom-10",
      depends_on_id: "loom-20",
      type: "blocks",
      created_at: "2024-01-01T00:00:00Z",
    };

    expect(dep.issue_id).toBe("loom-10");
    expect(dep.depends_on_id).toBe("loom-20");
    expect(dep.type).toBe("blocks");
    expect(dep.created_at).toBe("2024-01-01T00:00:00Z");
  });

  it("supports optional fields", () => {
    const dep: Dependency = {
      issue_id: "loom-10",
      depends_on_id: "loom-20",
      type: "blocks",
      created_at: "2024-01-01T00:00:00Z",
      created_by: "alice",
      metadata: '{"reason":"architecture"}',
      thread_id: "thread-1",
    };

    expect(dep.created_by).toBe("alice");
    expect(dep.metadata).toContain("architecture");
    expect(dep.thread_id).toBe("thread-1");
  });
});

// ---------------------------------------------------------------------------
// 9. event.ts -- Event alias (maps to IssueEvent)
// ---------------------------------------------------------------------------
describe("Event alias (IssueEvent)", () => {
  it("has expected fields from generated IssueEvent schema", () => {
    const event: Event = {
      id: 1,
      issue_id: "loom-5",
      event_type: "issue.created",
      actor: "alice",
      created_at: "2024-03-01T12:00:00Z",
    };

    expect(event.id).toBe(1);
    expect(event.issue_id).toBe("loom-5");
    expect(event.event_type).toBe("issue.created");
    expect(event.actor).toBe("alice");
  });

  it("supports optional old_value/new_value/comment", () => {
    const event: Event = {
      id: 2,
      issue_id: "loom-5",
      event_type: "issue.status_changed",
      actor: "bob",
      old_value: "open",
      new_value: "in_progress",
      comment: "Starting work",
      created_at: "2024-03-01T13:00:00Z",
    };

    expect(event.old_value).toBe("open");
    expect(event.new_value).toBe("in_progress");
    expect(event.comment).toBe("Starting work");
  });
});

// ---------------------------------------------------------------------------
// 10. issue.ts -- Issue (Omit + IssueExtensions), BlockerRef alias
// ---------------------------------------------------------------------------
describe("Issue type (Omit + IssueExtensions)", () => {
  it("has base fields from generated schema", () => {
    const issue: Issue = {
      id: "loom-1",
      title: "Fix login bug",
      priority: 2,
      created_at: "2024-01-01T00:00:00Z",
      updated_at: "2024-01-01T00:00:00Z",
    };

    expect(issue.id).toBe("loom-1");
    expect(issue.title).toBe("Fix login bug");
    expect(issue.priority).toBe(2);
  });

  it("accepts extension fields not in generated schema", () => {
    const issue: Issue = {
      id: "loom-2",
      title: "HOP validated issue",
      priority: 1,
      created_at: "2024-01-01T00:00:00Z",
      updated_at: "2024-01-01T00:00:00Z",
      // Extension fields
      quality_score: 0.95,
      crystallizes: true,
      is_template: false,
      repo: "api",
      work_type: "mutex",
    };

    expect(issue.quality_score).toBe(0.95);
    expect(issue.crystallizes).toBe(true);
    expect(issue.repo).toBe("api");
  });

  it("status accepts wider union than generated enum", () => {
    // The IssueExtensions type widens status to the full Status union,
    // including internal statuses like tombstone, pinned, hooked.
    const issue: Issue = {
      id: "loom-3",
      title: "Tombstoned issue",
      priority: 3,
      status: "tombstone",
      created_at: "2024-01-01T00:00:00Z",
      updated_at: "2024-01-01T00:00:00Z",
    };

    expect(issue.status).toBe("tombstone");
  });
});

describe("BlockerRef alias", () => {
  it("has expected fields from generated BlockerRef schema", () => {
    const blocker: BlockerRef = {
      id: "loom-99",
      title: "Blocking dependency",
      priority: 1,
    };

    expect(blocker.id).toBe("loom-99");
    expect(blocker.title).toBe("Blocking dependency");
    expect(blocker.priority).toBe(1);
  });
});

// ---------------------------------------------------------------------------
// 11. agent.ts -- Loom* types aliased to Monitor* schemas
// ---------------------------------------------------------------------------
describe("Agent type aliases", () => {
  it("LoomCommitDetail has expected fields", () => {
    const commit: LoomCommitDetail = {
      hash: "abc123",
      message: "Fix bug in parser",
    };

    expect(commit.hash).toBe("abc123");
    expect(commit.message).toBe("Fix bug in parser");
  });

  it("LoomFileChange has expected fields", () => {
    const change: LoomFileChange = {
      status: "M",
      path: "src/parser.ts",
    };

    expect(change.status).toBe("M");
    expect(change.path).toBe("src/parser.ts");
  });

  it("LoomAgentStatus extends MonitorAgentStatus with state/cross_repo/path/worktree_path", () => {
    const agent: LoomAgentStatus = {
      name: "nova",
      branch: "feature/fix",
      status: "working",
      ahead: 2,
      behind: 0,
      state: "active",
      cross_repo: true,
      path: "/home/user/repo",
      worktree_path: "/home/user/repo/.worktrees/nova",
    };

    expect(agent.name).toBe("nova");
    expect(agent.state).toBe("active");
    expect(agent.cross_repo).toBe(true);
    expect(agent.path).toBe("/home/user/repo");
    expect(agent.worktree_path).toBe("/home/user/repo/.worktrees/nova");
  });

  it("LoomAgentStatus workspace is optional for unassigned agents", () => {
    const agent: LoomAgentStatus = {
      name: "falcon",
      branch: "main",
      status: "ready",
      ahead: 0,
      behind: 0,
    };

    expect(agent.workspace).toBeUndefined();
  });

  it("LoomTaskInfo has expected fields", () => {
    const task: LoomTaskInfo = {
      id: "loom-50",
      title: "Implement search",
      priority: 2,
      status: "open",
    };

    expect(task.id).toBe("loom-50");
    expect(task.title).toBe("Implement search");
  });

  it("LoomTaskSummary has expected fields including epics", () => {
    const summary: LoomTaskSummary = {
      needs_planning: 3,
      ready_to_implement: 5,
      in_progress: 2,
      need_review: 1,
      backlog: 10,
      epics: 4,
    };

    expect(summary.needs_planning).toBe(3);
    expect(summary.epics).toBe(4);
  });

  it("WorktreeSyncDetail has expected fields", () => {
    const detail: WorktreeSyncDetail = {
      name: "nova",
      count: 3,
    };

    expect(detail.name).toBe("nova");
    expect(detail.count).toBe(3);
  });

  it("LoomSyncInfo has expected fields", () => {
    const sync: LoomSyncInfo = {
      db_synced: true,
      db_last_sync: "2024-08-01T12:00:00Z",
      git_needs_push: 0,
      git_needs_pull: 1,
    };

    expect(sync.db_synced).toBe(true);
    expect(sync.git_needs_pull).toBe(1);
  });

  it("LoomStats has expected fields", () => {
    const stats: LoomStats = {
      open: 15,
      closed: 40,
      total: 60,
      completion: 66.7,
      remaining: 20,
      in_progress: 5,
      review: 3,
      blocked: 2,
    };

    expect(stats.total).toBe(60);
    expect(stats.completion).toBe(66.7);
  });

  it("LoomStatusResponse has full status structure", () => {
    const response: LoomStatusResponse = {
      workspace: { mode: "workspace", name: "my-ws" },
      agents: [],
      tasks: {
        needs_planning: 0,
        ready_to_implement: 0,
        in_progress: 0,
        need_review: 0,
        backlog: 0,
        epics: 0,
      },
      in_progress_list: [],
      agent_tasks: {},
      stats: {
        open: 0,
        closed: 0,
        total: 0,
        completion: 0,
        remaining: 0,
        in_progress: 0,
        review: 0,
        blocked: 0,
      },
      sync: {
        db_synced: true,
        db_last_sync: "2024-08-01T00:00:00Z",
        git_needs_push: 0,
        git_needs_pull: 0,
      },
      timestamp: "2024-08-01T12:00:00Z",
    };

    expect(response.workspace.mode).toBe("workspace");
    expect(response.timestamp).toBe("2024-08-01T12:00:00Z");
  });

  it("LoomAgentsResponse has expected fields", () => {
    const response: LoomAgentsResponse = {
      workspace: { mode: "workspace" },
      agents: [
        {
          name: "nova",
          branch: "main",
          status: "ready",
          ahead: 0,
          behind: 0,
          workspace: "default",
        },
      ],
      timestamp: "2024-08-01T12:00:00Z",
    };

    expect(response.agents).toHaveLength(1);
    expect(response.agents[0].name).toBe("nova");
  });

  it("LoomTasksResponse has expected fields", () => {
    const response: LoomTasksResponse = {
      summary: {
        needs_planning: 1,
        ready_to_implement: 2,
        in_progress: 1,
        need_review: 0,
        backlog: 5,
        epics: 1,
      },
      needs_planning: [
        { id: "loom-1", title: "Plan X", priority: 1, status: "open" },
      ],
      ready_to_implement: [],
      needs_review: [],
      in_progress: [
        { id: "loom-2", title: "Build Y", priority: 2, status: "in_progress" },
      ],
      backlog: [],
      done: [],
      timestamp: "2024-08-01T12:00:00Z",
    };

    expect(response.summary.needs_planning).toBe(1);
    expect(response.needs_planning).toHaveLength(1);
    expect(response.in_progress).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// 12. Structural compatibility: generated schema objects satisfy aliased types
// ---------------------------------------------------------------------------
describe("Structural compatibility", () => {
  it("generated Comment schema object satisfies Comment alias", () => {
    const raw: components["schemas"]["Comment"] = {
      id: 1,
      issue_id: "loom-1",
      author: "test",
      text: "test",
      created_at: "2024-01-01T00:00:00Z",
    };
    const aliased: Comment = raw;
    expect(aliased).toBe(raw);
  });

  it("generated EditorInfo schema object satisfies EditorInfo alias", () => {
    const raw: components["schemas"]["EditorInfo"] = {
      id: "vim",
      display_name: "Vim",
      icon_name: "vim",
      detected: false,
    };
    const aliased: EditorInfo = raw;
    expect(aliased).toBe(raw);
  });

  it("generated SessionResponse schema object satisfies SessionRecord alias", () => {
    const raw: components["schemas"]["SessionResponse"] = {
      session_id: "s1",
      task_id: "t1",
      agent_name: "a1",
      backend: "b1",
      started_at: "2024-01-01T00:00:00Z",
      status: "completed",
      exit_code: 0,
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
      estimated_cost_usd: 0,
      files_changed: 0,
      lines_added: 0,
      lines_removed: 0,
      attempt_num: 1,
      is_active: false,
      has_transcript: false,
      has_diff: false,
    };
    const aliased: SessionRecord = raw;
    expect(aliased).toBe(raw);
  });

  it("generated Statistics schema object satisfies Statistics alias", () => {
    const raw: components["schemas"]["Statistics"] = {
      total_issues: 0,
      open_issues: 0,
      in_progress_issues: 0,
      closed_issues: 0,
      blocked_issues: 0,
      deferred_issues: 0,
      ready_issues: 0,
      tombstone_issues: 0,
      pinned_issues: 0,
      epics_eligible_for_closure: 0,
      average_lead_time_hours: 0,
    };
    const aliased: Statistics = raw;
    expect(aliased).toBe(raw);
  });

  it("generated IssueEvent schema object satisfies Event alias", () => {
    const raw: components["schemas"]["IssueEvent"] = {
      id: 1,
      issue_id: "loom-1",
      event_type: "issue.created",
      actor: "system",
      created_at: "2024-01-01T00:00:00Z",
    };
    const aliased: Event = raw;
    expect(aliased).toBe(raw);
  });

  it("generated Dependency schema object satisfies Dependency alias", () => {
    const raw: components["schemas"]["Dependency"] = {
      issue_id: "loom-1",
      depends_on_id: "loom-2",
      type: "blocks",
      created_at: "2024-01-01T00:00:00Z",
    };
    const aliased: Dependency = raw;
    expect(aliased).toBe(raw);
  });

  it("generated BlockerRef schema object satisfies BlockerRef alias", () => {
    const raw: components["schemas"]["BlockerRef"] = {
      id: "loom-1",
      title: "Blocker",
      priority: 1,
    };
    const aliased: BlockerRef = raw;
    expect(aliased).toBe(raw);
  });
});
