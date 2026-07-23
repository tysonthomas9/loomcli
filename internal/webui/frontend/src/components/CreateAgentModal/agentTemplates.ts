/**
 * Agent template catalog for the CreateAgentModal behavior gallery.
 *
 * Main cards are organized by what the user wants the agent to do:
 * role-backed behavior cards (Planner, Coder, custom roles, + New role) and
 * scripted workflow behaviors. The old daemon-supervised cards remain available
 * in an advanced section because they still hit the legacy agentdef endpoint.
 */

export type AgentTemplateKind =
  | "role"
  | "role-create"
  | "workflow"
  | "lead"
  | "builtin-role"
  | "custom-role";

export type TemplateSection = "behavior" | "advanced";

/** Provisioning data for a legacy `custom-role` daemon template. */
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

/** Defaults used when the + New role card creates a role transactionally. */
export interface RoleCreateDefaults {
  promptFilename: string;
  taskFilter?: string;
  description?: string;
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
   * Stable trigger-binding id. For cron bindings the backend derives the route
   * key from this so scheduled workflows do not collide on a shared route.
   */
  bindingId: string;
  /** Backend that must be healthy before this workflow can be activated. */
  requiredBackend?: string;
  /** The workflow cannot run usefully without the configured GitHub runtime credential and a GitHub target remote. */
  requiresGitHub?: boolean;
  /**
   * When set, the submit path also provisions a github connector (reusing the
   * Settings runtime credential) plus these grants, scoped to the target repo.
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
  /** Optional small visual tag, e.g. "custom code". */
  tag?: string;
  /** Placeholder shown in the name field when this template is selected. */
  defaultName: string;
  testId: string;
  /** Canonical role name for role and legacy daemon templates. */
  roleName: string;
  /** Set when kind === "role-create". */
  roleCreate?: RoleCreateDefaults;
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
    id: "behavior",
    label: "Behavior",
    hint: "Pick what this agent should do.",
    labelId: "create-agent-behavior-label",
  },
  {
    id: "advanced",
    label: "Advanced: daemon-supervised (legacy)",
    hint: "Older Go-daemon agents kept for compatibility.",
    labelId: "create-agent-advanced-label",
  },
];

const ACCENTS = {
  plan: "#0d9488",
  task: "#ea580c",
  lead: "#db2777",
  triage: "#9333ea",
  bugfix: "#2563eb",
  review: "#16a34a",
  localReview: "#0891b2",
  custom: "#7c3aed",
  newRole: "#475569",
} as const;

/**
 * Prompt seeded for the legacy bug-triage daemon role on first use. The main
 * behavior grid only shows bug triage when a workspace role exists for it.
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

const PLANNER_ROLE_TEMPLATE: AgentTemplate = {
  id: "role-plan",
  kind: "role",
  section: "behavior",
  roleName: "plan",
  title: "Planner",
  description: "Plans ready tasks into implementation designs.",
  glyph: "P",
  accentColor: ACCENTS.plan,
  defaultName: "planner",
  testId: "create-agent-template-planner",
};

const CODER_ROLE_TEMPLATE: AgentTemplate = {
  id: "role-task",
  kind: "role",
  section: "behavior",
  roleName: "task",
  title: "Coder",
  description: "Implements approved designs when tasks become ready.",
  glyph: "C",
  accentColor: ACCENTS.task,
  defaultName: "coder",
  testId: "create-agent-template-task",
};

export const NEW_ROLE_TEMPLATE: AgentTemplate = {
  id: "role-new",
  kind: "role-create",
  section: "behavior",
  roleName: "",
  title: "+ New role...",
  description: "Define a new role prompt and run it on ready tasks.",
  glyph: "+",
  accentColor: ACCENTS.newRole,
  defaultName: "custom-agent",
  testId: "create-agent-template-new-role",
  roleCreate: {
    promptFilename: "custom-role.md",
    taskFilter: "has_design",
  },
};

const BUG_FIX_WORKFLOW_TEMPLATE: AgentTemplate = {
  id: "bug-fix-agent",
  kind: "workflow",
  section: "behavior",
  roleName: "",
  title: "Bug-fix",
  description: "Runs a scheduled custom-code loop to fix ready bugs.",
  glyph: "F",
  accentColor: ACCENTS.bugfix,
  tag: "custom code",
  defaultName: "bug-fix",
  testId: "create-agent-template-bug-fix",
  workflow: {
    workflow: "bug-fix-agent",
    bindingId: "s1-bug-fix",
    requiresGitHub: true,
  },
};

const REVIEW_LOOP_WORKFLOW_TEMPLATE: AgentTemplate = {
  id: "review-loop-agent",
  kind: "workflow",
  section: "behavior",
  roleName: "",
  title: "Review loop",
  description: "Runs scheduled custom code to review GitHub PR cards.",
  glyph: "R",
  accentColor: ACCENTS.review,
  tag: "custom code",
  defaultName: "review-loop",
  testId: "create-agent-template-review-loop",
  workflow: {
    workflow: "review-loop-agent",
    bindingId: "s2-review-loop",
    requiredBackend: "codex",
    requiresGitHub: true,
    grants: [
      { action: "github.pull_request.read", resource: "repo:<owner/name>" },
      { action: "github.compare.read", resource: "repo:<owner/name>" },
      { action: "github.review.post", resource: "repo:<owner/name>" },
    ],
  },
};

const LOCAL_REVIEW_WORKFLOW_TEMPLATE: AgentTemplate = {
  id: "local-review-agent",
  kind: "workflow",
  section: "behavior",
  roleName: "",
  title: "Local review",
  description: "Runs scheduled custom code to review local-branch deliveries.",
  glyph: "L",
  accentColor: ACCENTS.localReview,
  tag: "custom code",
  defaultName: "local-review",
  testId: "create-agent-template-local-review",
  workflow: {
    workflow: "local-review-agent",
    bindingId: "s3-local-review",
    requiredBackend: "codex",
  },
};

const LEGACY_PLANNER_TEMPLATE: AgentTemplate = {
  id: "legacy-planner",
  kind: "builtin-role",
  section: "advanced",
  roleName: "plan",
  title: "Planner",
  description: "Legacy Go-daemon planner.",
  glyph: "P",
  accentColor: ACCENTS.plan,
  defaultName: "planner",
  testId: "create-agent-template-legacy-planner",
};

const LEGACY_TASK_TEMPLATE: AgentTemplate = {
  id: "legacy-task",
  kind: "builtin-role",
  section: "advanced",
  roleName: "task",
  title: "Task Runner",
  description: "Legacy Go-daemon task runner.",
  glyph: "T",
  accentColor: ACCENTS.task,
  defaultName: "worker",
  testId: "create-agent-template-legacy-task",
};

const LEGACY_BUG_TRIAGE_TEMPLATE: AgentTemplate = {
  id: "legacy-bug-triage",
  kind: "custom-role",
  section: "advanced",
  roleName: "bug-triage",
  title: "Bug triage",
  description: "Legacy Go-daemon triage worker (read-only).",
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

const LEGACY_LEAD_TEMPLATE: AgentTemplate = {
  id: "lead",
  kind: "lead",
  section: "advanced",
  roleName: "lead",
  title: "Lead",
  description: "Legacy interactive lead terminal.",
  glyph: "L",
  accentColor: ACCENTS.lead,
  defaultName: "lead",
  testId: "create-agent-template-lead",
};

export const BUILTIN_ROLE_TEMPLATES: AgentTemplate[] = [
  PLANNER_ROLE_TEMPLATE,
  CODER_ROLE_TEMPLATE,
];

export const SCRIPTED_WORKFLOW_TEMPLATES: AgentTemplate[] = [
  BUG_FIX_WORKFLOW_TEMPLATE,
  REVIEW_LOOP_WORKFLOW_TEMPLATE,
  LOCAL_REVIEW_WORKFLOW_TEMPLATE,
];

export const LEGACY_DAEMON_TEMPLATES: AgentTemplate[] = [
  LEGACY_PLANNER_TEMPLATE,
  LEGACY_TASK_TEMPLATE,
  LEGACY_BUG_TRIAGE_TEMPLATE,
  LEGACY_LEAD_TEMPLATE,
];

/** The static catalog, excluding dynamic custom-role cards from the roles API. */
export const AGENT_TEMPLATES: AgentTemplate[] = [
  ...BUILTIN_ROLE_TEMPLATES,
  NEW_ROLE_TEMPLATE,
  ...SCRIPTED_WORKFLOW_TEMPLATES,
  ...LEGACY_DAEMON_TEMPLATES,
];

export type DefaultRole = "lead" | "plan" | "task";
export type SupervisedRole = "plan" | "task";

/**
 * Resolve the template a `defaultRole` prop should pre-select. plan/task select
 * the TS role-backed behavior cards; lead remains legacy/advanced for Phase 5.
 */
export function templateForRole(role: DefaultRole | undefined): AgentTemplate {
  if (role === "plan") return PLANNER_ROLE_TEMPLATE;
  if (role === "lead") return LEGACY_LEAD_TEMPLATE;
  return CODER_ROLE_TEMPLATE;
}

/**
 * Resolve a daemon-supervised template for flows that must receive an agent
 * row through CreateAgentModal.onSuccess (for example onboarding and PR
 * reviewer assignment). These callers cannot accept role-backed prompt-agent
 * bindings, so the modal constrains selection to this one template.
 */
export function supervisedTemplateForRole(role: SupervisedRole): AgentTemplate {
  return role === "plan" ? LEGACY_PLANNER_TEMPLATE : LEGACY_TASK_TEMPLATE;
}

export function templatesForSection(section: TemplateSection): AgentTemplate[] {
  if (section === "advanced") return LEGACY_DAEMON_TEMPLATES;
  return [
    ...BUILTIN_ROLE_TEMPLATES,
    NEW_ROLE_TEMPLATE,
    ...SCRIPTED_WORKFLOW_TEMPLATES,
  ];
}

export function customRoleTemplate(role: {
  name: string;
  description?: string;
}): AgentTemplate {
  const roleName = role.name.trim();
  return {
    id: `role-${roleName}`,
    kind: "role",
    section: "behavior",
    roleName,
    title: roleName,
    description:
      role.description?.trim() ||
      `Runs the ${roleName} role when a task becomes ready.`,
    glyph: roleName.slice(0, 1).toUpperCase() || "R",
    accentColor: ACCENTS.custom,
    defaultName: roleName || "custom-agent",
    testId: `create-agent-template-role-${roleName}`,
  };
}

export function rolePromptFilename(roleName: string): string {
  const slug = roleName.trim() || "custom-role";
  return `${slug}.md`;
}
