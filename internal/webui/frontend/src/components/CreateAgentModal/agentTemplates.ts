/**
 * Agent template catalog — the kind-tagged descriptors that power the
 * CreateAgentModal gallery.
 *
 * Each template declares which agent "lane" it belongs to via `kind`, which is
 * the seam the submit handler routes on:
 *
 *   lead         → POST /agents (role=lead) + open the lead terminal
 *   builtin-role → POST /agents (role=plan|task)
 *   custom-role  → ensure Role + prompt file (role API), then POST /agents
 *   workflow     → create a cron trigger binding (which self-heals + activates
 *                  the builtin workflow); the review loop also provisions a
 *                  github connector + grants. No agent row is created.
 *
 * The event-driven workflow lane (code review on PRs) is a separate surface —
 * see AutomationsModal — because it creates a trigger binding, not an agent row.
 */

export type AgentTemplateKind =
  | "lead"
  | "builtin-role"
  | "custom-role"
  | "workflow";

export type TemplateSection = "background" | "workflows" | "lead";

/** Provisioning data for a `custom-role` template. */
export interface CustomRoleSpec {
  /** Canonical role name to ensure (e.g. "bug-triage"). */
  roleName: string;
  /** Prompt filename seeded under the workspace prompt dir on first use. */
  promptFilename: string;
  /** Prompt body written when the role is first provisioned. */
  promptContent: string;
  /** Optional role description. */
  description?: string;
  /**
   * Task-phase filter: "needs_plan" | "has_design" | "any" (defaults to
   * "has_design"). Filters by task phase, not issue type.
   */
  taskFilter?: string;
  /** Constrain the role to read-only tools. */
  readOnly?: boolean;
  /** Optional explicit tool allow/deny lists. */
  allowedTools?: string[];
  deniedTools?: string[];
}

/** A single deny-by-default connector grant provisioned for a `workflow`. */
export interface WorkflowGrantSpec {
  /** Connector action authorized (e.g. "github.pull_request.read"). */
  action: string;
  /**
   * Grant resource pattern. The literal token "<owner/name>" is replaced with
   * the selected target repo's GitHub slug at submit time (see grantsForRepo).
   */
  resource: string;
}

/** The literal token in grant resource patterns replaced by the repo slug. */
const REPO_SLUG_TOKEN = "<owner/name>";

/**
 * Materialize a workflow's grant specs for a concrete "owner/name" repo slug.
 * Keeps the REPO_SLUG_TOKEN convention inside this module so descriptors and
 * their substitution can't drift apart.
 */
export function grantsForRepo(
  spec: WorkflowSpec,
  slug: string,
): WorkflowGrantSpec[] {
  return (spec.grants ?? []).map((grant) => ({
    ...grant,
    resource: grant.resource.replace(REPO_SLUG_TOKEN, slug),
  }));
}

/** Provisioning data for a `workflow` template. */
export interface WorkflowSpec {
  /**
   * Builtin workflow name (e.g. "bug-fix-agent"). Creating a cron binding for
   * it self-heals the builtin and activates it.
   */
  workflow: string;
  /**
   * Stable trigger-binding id (also the grants' binding_id for review). For a
   * cron binding the backend derives the routing route_key from this, so two
   * scheduled workflows never collide on a shared route.
   */
  bindingId: string;
  /**
   * When set, the submit path also provisions a github connector (reusing the
   * Settings runtime credential) plus these grants, scoped to the target repo.
   * Presence signals the connector path; it is gated on the Settings GitHub
   * token being configured.
   */
  grants?: WorkflowGrantSpec[];
}

export interface AgentTemplate {
  id: string;
  kind: AgentTemplateKind;
  section: TemplateSection;
  title: string;
  description: string;
  glyph: string;
  accentColor: string;
  /** Placeholder shown in the name field when this template is selected. */
  defaultName: string;
  testId: string;
  /**
   * Canonical role the created agent takes (lead | plan | task | a custom
   * role). Empty for `workflow` templates, which create a binding, not an agent.
   */
  roleName: string;
  /** Set when kind === "custom-role". */
  customRole?: CustomRoleSpec;
  /** Set when kind === "workflow". */
  workflow?: WorkflowSpec;
}

export interface TemplateSectionMeta {
  id: TemplateSection;
  label: string;
  hint: string;
  /** Stable id used for aria-labelledby wiring. */
  labelId: string;
}

export const TEMPLATE_SECTIONS: TemplateSectionMeta[] = [
  {
    id: "background",
    label: "Background agents",
    hint: "Supervised workers that run automatically",
    labelId: "create-agent-background-label",
  },
  {
    id: "workflows",
    label: "Background workflows",
    hint: "Scheduled workflows that run on a cadence",
    labelId: "create-agent-workflows-label",
  },
  {
    id: "lead",
    label: "Lead agent",
    hint: "Interactive orchestrator in a terminal",
    labelId: "create-agent-lead-label",
  },
];

const ACCENTS = {
  plan: "#0d9488",
  task: "#ea580c",
  lead: "#db2777",
  triage: "#9333ea",
  bugfix: "#2563eb",
  review: "#16a34a",
} as const;

/**
 * Prompt seeded for the bug-triage custom role on first use. Kept here so the
 * descriptor is the single source of truth for the template.
 */
export const BUG_TRIAGE_PROMPT = `# Bug triage agent

You are a focused bug-triage agent. You claim incoming bug tickets, reproduce
them, and write a crisp triage summary. You do NOT implement fixes.

For each bug ticket you claim:
1. Restate the report in one line and identify the affected area of the code.
2. Attempt to reproduce. Record exact steps, expected vs actual, and the
   smallest failing case you can find.
3. Locate the most likely root cause in the code (file:line) and explain why.
4. Assess severity and blast radius.
5. Post a triage summary comment and set the ticket's labels/priority. Leave the
   ticket ready for an implementer — do not change product code yourself.

Be concise and evidence-based. Prefer reading code and logs over speculation.
`;

const PLANNER_TEMPLATE: AgentTemplate = {
  id: "planner",
  kind: "builtin-role",
  section: "background",
  roleName: "plan",
  title: "Planner",
  description: "Breaks epics into tasks under daemon supervision.",
  glyph: "P",
  accentColor: ACCENTS.plan,
  defaultName: "planner",
  testId: "create-agent-template-planner",
};

const TASK_TEMPLATE: AgentTemplate = {
  id: "task",
  kind: "builtin-role",
  section: "background",
  roleName: "task",
  title: "Task Runner",
  description: "Claims and runs ready tasks under daemon supervision.",
  glyph: "T",
  accentColor: ACCENTS.task,
  defaultName: "worker",
  testId: "create-agent-template-task",
};

const BUG_TRIAGE_TEMPLATE: AgentTemplate = {
  id: "bug-triage",
  kind: "custom-role",
  section: "background",
  roleName: "bug-triage",
  title: "Bug triage",
  description: "Reproduces and triages ready tickets (read-only).",
  glyph: "B",
  accentColor: ACCENTS.triage,
  defaultName: "triage",
  testId: "create-agent-template-bug-triage",
  customRole: {
    roleName: "bug-triage",
    promptFilename: "bug-triage.md",
    promptContent: BUG_TRIAGE_PROMPT,
    description: "Reproduces and triages ready tickets; does not write fixes.",
    taskFilter: "any",
    readOnly: true,
  },
};

const BUG_FIX_WORKFLOW_TEMPLATE: AgentTemplate = {
  id: "bug-fix-agent",
  kind: "workflow",
  section: "workflows",
  roleName: "",
  title: "Bug-fix",
  description: "Claims a ready bug on a schedule and opens a fix PR.",
  glyph: "F",
  accentColor: ACCENTS.bugfix,
  defaultName: "bug-fix",
  testId: "create-agent-template-bug-fix",
  workflow: {
    workflow: "bug-fix-agent",
    bindingId: "s1-bug-fix",
  },
};

const REVIEW_LOOP_WORKFLOW_TEMPLATE: AgentTemplate = {
  id: "review-loop-agent",
  kind: "workflow",
  section: "workflows",
  roleName: "",
  title: "Review loop",
  description: "Reviews cards in review on a schedule, capped per card.",
  glyph: "R",
  accentColor: ACCENTS.review,
  defaultName: "review-loop",
  testId: "create-agent-template-review-loop",
  workflow: {
    workflow: "review-loop-agent",
    bindingId: "s2-review-loop",
    grants: [
      { action: "github.pull_request.read", resource: "repo:<owner/name>" },
      { action: "github.compare.read", resource: "repo:<owner/name>" },
      { action: "github.review.post", resource: "repo:<owner/name>" },
    ],
  },
};

const LEAD_TEMPLATE: AgentTemplate = {
  id: "lead",
  kind: "lead",
  section: "lead",
  roleName: "lead",
  title: "Lead",
  description: "Orchestrates an epic interactively in a terminal.",
  glyph: "L",
  accentColor: ACCENTS.lead,
  defaultName: "lead",
  testId: "create-agent-template-lead",
};

/** The gallery catalog, in display order. */
export const AGENT_TEMPLATES: AgentTemplate[] = [
  PLANNER_TEMPLATE,
  TASK_TEMPLATE,
  BUG_TRIAGE_TEMPLATE,
  BUG_FIX_WORKFLOW_TEMPLATE,
  REVIEW_LOOP_WORKFLOW_TEMPLATE,
  LEAD_TEMPLATE,
];

export type DefaultRole = "lead" | "plan" | "task";

/**
 * Resolve the template a `defaultRole` prop should pre-select (Task by default).
 * Collapses the legacy `defaultRoleName` + `defaultKind` pair: a single role
 * string maps to exactly one template.
 */
export function templateForRole(role: DefaultRole | undefined): AgentTemplate {
  return AGENT_TEMPLATES.find((t) => t.roleName === role) ?? TASK_TEMPLATE;
}

export function templatesForSection(section: TemplateSection): AgentTemplate[] {
  return AGENT_TEMPLATES.filter((t) => t.section === section);
}
