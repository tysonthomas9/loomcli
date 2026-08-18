import type { BuiltInTeamTemplate } from "@/types/teamTemplate";

/**
 * Frontend mirror of internal/teamtemplate/bundles/*.yaml.
 *
 * Keep IDs, copy, revisions, agent role names, display labels and agent names
 * in sync with those code-shipped bundles. The HTTP catalog carries the same
 * public subset; this mirror is also needed before the picker has been opened
 * so onboarding can derive template detection and the architect handoff.
 */
export const BUILT_IN_TEAM_TEMPLATES = [
  {
    id: "fullstack-app",
    label: "Full-Stack App Development",
    description:
      "Architect-led team with split frontend/backend implementers plus a QA and review pair.",
    revision: 1,
    schema_version: 1,
    architectRoleName: "app-architect",
    roles: [
      {
        name: "app-architect",
        kind: "worker",
        display_label: "Architecture",
        description:
          "Turns full-stack tasks into implementable designs with interface contracts, data models and file-level change lists.",
      },
      {
        name: "frontend-dev",
        kind: "worker",
        display_label: "Developer",
        description:
          "Implements UI tasks against an approved design spec; verifies loading/empty/error states and accessibility basics.",
      },
      {
        name: "backend-dev",
        kind: "worker",
        display_label: "Developer",
        description:
          "Implements server-side tasks against the design's API contract; extends tests for changed behavior.",
      },
      {
        name: "qa-engineer",
        kind: "worker",
        display_label: "QA",
        description:
          "Writes and runs unit, e2e, integration and contract tests against a task's acceptance criteria and files defects as follow-up tasks.",
      },
      {
        name: "code-reviewer",
        kind: "interactive",
        display_label: "QA",
        description:
          "Human-driven PR review terminal; reads diffs and asks before any GitHub mutation.",
      },
    ],
    agents: [
      { name: "app-architect-1", role_name: "app-architect" },
      { name: "frontend-dev-1", role_name: "frontend-dev" },
      { name: "backend-dev-1", role_name: "backend-dev" },
      { name: "qa-engineer-1", role_name: "qa-engineer" },
    ],
  },
  {
    id: "website",
    label: "Website Development",
    description:
      "Designer-led team for marketing and content sites, with a frontend implementer, a copy writer and a QA and review pair.",
    revision: 1,
    schema_version: 1,
    architectRoleName: "web-designer",
    roles: [
      {
        name: "web-designer",
        kind: "worker",
        display_label: "Architecture",
        description:
          "Turns page and site tasks into implementable designs with layout, component structure, responsive breakpoints and content slots.",
      },
      {
        name: "frontend-dev",
        kind: "worker",
        display_label: "Developer",
        description:
          "Implements UI tasks against an approved design spec; verifies loading/empty/error states and accessibility basics.",
      },
      {
        name: "content-writer",
        kind: "worker",
        display_label: "Developer",
        description:
          "Writes and revises site copy against an approved design spec, including page metadata and calls to action.",
      },
      {
        name: "site-qa",
        kind: "worker",
        display_label: "QA",
        description:
          "Checks pages against acceptance criteria across browsers and viewports, including accessibility basics, and files defects as follow-up tasks.",
      },
      {
        name: "code-reviewer",
        kind: "interactive",
        display_label: "QA",
        description:
          "Human-driven PR review terminal; reads diffs and asks before any GitHub mutation.",
      },
    ],
    agents: [
      { name: "web-designer-1", role_name: "web-designer" },
      { name: "frontend-dev-1", role_name: "frontend-dev" },
      { name: "content-writer-1", role_name: "content-writer" },
      { name: "site-qa-1", role_name: "site-qa" },
    ],
  },
  {
    id: "ai-agent",
    label: "AI Agent Development",
    description:
      "Architect-and-researcher pair over an agent implementer, with an eval engineer and a review terminal.",
    revision: 1,
    schema_version: 1,
    architectRoleName: "agent-architect",
    roles: [
      {
        name: "agent-architect",
        kind: "worker",
        display_label: "Architecture",
        description:
          "Turns agent-system tasks into implementable designs covering tool boundaries, prompt contracts, state and failure handling.",
      },
      {
        name: "researcher",
        kind: "worker",
        display_label: "Architecture",
        description:
          "Investigates open questions and produces written findings with sources and a recommendation; never implements the result.",
      },
      {
        name: "agent-dev",
        kind: "worker",
        display_label: "Developer",
        description:
          "Implements agent and tool-integration tasks against an approved design; extends harness tests for changed behavior.",
      },
      {
        name: "eval-engineer",
        kind: "worker",
        display_label: "QA",
        description:
          "Builds and runs evaluation suites against a task's acceptance criteria and reports regressions as follow-up tasks.",
      },
      {
        name: "code-reviewer",
        kind: "interactive",
        display_label: "QA",
        description:
          "Human-driven PR review terminal; reads diffs and asks before any GitHub mutation.",
      },
    ],
    agents: [
      { name: "agent-architect-1", role_name: "agent-architect" },
      { name: "researcher-1", role_name: "researcher" },
      { name: "agent-dev-1", role_name: "agent-dev" },
      { name: "eval-engineer-1", role_name: "eval-engineer" },
    ],
  },
  {
    id: "backend",
    label: "Backend Development",
    description:
      "Architect-led service team with a backend implementer, a data engineer and a QA and review pair.",
    revision: 1,
    schema_version: 1,
    architectRoleName: "api-architect",
    roles: [
      {
        name: "api-architect",
        kind: "worker",
        display_label: "Architecture",
        description:
          "Turns backend tasks into implementable designs with API contracts, data models, migration plans and file-level change lists.",
      },
      {
        name: "backend-dev",
        kind: "worker",
        display_label: "Developer",
        description:
          "Implements server-side tasks against the design's API contract; extends tests for changed behavior.",
      },
      {
        name: "data-engineer",
        kind: "worker",
        display_label: "Developer",
        description:
          "Implements schema, migration and data-pipeline tasks against an approved design, with reversible migrations and seed fixtures.",
      },
      {
        name: "qa-engineer",
        kind: "worker",
        display_label: "QA",
        description:
          "Writes and runs unit, e2e, integration and contract tests against a task's acceptance criteria and files defects as follow-up tasks.",
      },
      {
        name: "code-reviewer",
        kind: "interactive",
        display_label: "QA",
        description:
          "Human-driven PR review terminal; reads diffs and asks before any GitHub mutation.",
      },
    ],
    agents: [
      { name: "api-architect-1", role_name: "api-architect" },
      { name: "backend-dev-1", role_name: "backend-dev" },
      { name: "data-engineer-1", role_name: "data-engineer" },
      { name: "qa-engineer-1", role_name: "qa-engineer" },
    ],
  },
] as const satisfies readonly BuiltInTeamTemplate[];

export type BuiltInTeamTemplateID =
  (typeof BUILT_IN_TEAM_TEMPLATES)[number]["id"];

export function builtInTeamTemplateById(
  id: string,
): BuiltInTeamTemplate | undefined {
  return BUILT_IN_TEAM_TEMPLATES.find((teamTemplate) => teamTemplate.id === id);
}
