import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { useNavigate } from "react-router-dom";

import {
  createPromptAgentRecord,
  type CreatePromptAgentRecordRequest,
} from "@/api/agents"; // eslint-disable-line boundaries/dependencies -- Pending hook migration.
import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import type {
  CreateRoleRequest,
  InteractivePromptInfo,
  RepoInfo,
  RoleWithPrompt,
  WorkspaceAgentInfo,
  WorkspaceRole,
} from "@/api/workspace";
import { getWorkspaceRole, listWorkspaceRoles } from "@/api/workspace"; // eslint-disable-line boundaries/dependencies -- Pending hook migration.
import {
  useCreateWorkspaceAgent,
  useEnsureWorkspaceRole,
  useInteractivePrompts,
} from "@/hooks/agents";
import {
  GITHUB_CONNECTOR_ID,
  dispatchBindingsChanged,
  useAutomations,
  useBackends,
  useConnectorProvisioning,
  useLocalSettings,
} from "@/hooks/workspace";
import { ApiError } from "@/types/common";
import {
  normalizeStoredAgentName,
  validateStoredAgentName,
} from "@/utils/agentName";

import { AgentTemplateCard } from "./AgentTemplateCard";
import {
  BUILTIN_ROLE_TEMPLATES,
  LEGACY_BUG_TRIAGE_PROMPT,
  LEGACY_BUG_TRIAGE_PROMPT_FILE_BASENAME,
  LEGACY_DAEMON_TEMPLATES,
  NEW_ROLE_TEMPLATE,
  ROLE_TRIGGER_OPTIONS,
  SCRIPTED_WORKFLOW_TEMPLATES,
  customRoleTemplate,
  grantsForRepo,
  roleTriggerOption,
  rolePromptFilename,
  supervisedTemplateForRole,
  templateForRole,
  TEMPLATE_SECTIONS,
  type AgentTemplate,
  type DefaultRole,
  type RoleTrigger,
  type SupervisedRole,
} from "./agentTemplates";
import styles from "./CreateAgentModal.module.css";

/** Cadence choices for a scheduled workflow, each mapping to a cron expression. */
const CADENCE_OPTIONS = [
  { value: "10m", label: "Every 10 minutes", cron: "*/10 * * * *" },
  { value: "hourly", label: "Hourly", cron: "0 * * * *" },
  { value: "daily", label: "Daily (09:00)", cron: "0 9 * * *" },
] as const;

const DEFAULT_CADENCE = CADENCE_OPTIONS[0].value;

const BUILTIN_ROLE_NAMES = new Set(["lead", "plan", "task"]);

const CUSTOM_PROMPT_ID = "custom";

const BUG_TRIAGE_ROLE_NAME = "bug-triage";
const BUG_TRIAGE_FALLBACK_ROLE_NAME = "loom-bug-triage-v2";
const LEGACY_BUG_TRIAGE_DESCRIPTION =
  "Reproduces and triages ready tickets; does not write fixes.";

const INTERACTIVE_ACCENTS = {
  lead: "#db2777",
  prompt: "#2563eb",
  custom: "#7c3aed",
} as const;

const DEFAULT_INTERACTIVE_PROMPTS: InteractivePromptInfo[] = [
  { id: "lead", label: "Lead" },
  { id: "pr-review", label: "PR Review" },
];

const CUSTOM_PROMPT_TEMPLATE = {
  title: "Custom prompt",
  description: "Define a terminal teammate with your own inline instructions.",
  glyph: "✦",
  placeholder: "reviewer",
  testId: "create-agent-template-custom-prompt",
  accentColor: INTERACTIVE_ACCENTS.custom,
};

function interactivePromptCard(prompt: InteractivePromptInfo) {
  if (prompt.id === "lead") {
    return {
      description: "Orchestrates work interactively in a terminal.",
      glyph: "L",
      placeholder: "lead",
      testId: "create-agent-template-lead",
      accentColor: INTERACTIVE_ACCENTS.lead,
    };
  }
  if (prompt.id === "pr-review") {
    return {
      description: "Reviews pull requests with focused terminal guidance.",
      glyph: "R",
      placeholder: "reviewer",
      testId: "create-agent-template-interactive-pr-review",
      accentColor: INTERACTIVE_ACCENTS.prompt,
    };
  }
  return {
    description:
      "Starts an interactive terminal agent with this built-in prompt.",
    glyph: prompt.label.trim().charAt(0).toUpperCase() || "I",
    placeholder: prompt.id,
    testId: `create-agent-template-interactive-${prompt.id}`,
    accentColor: INTERACTIVE_ACCENTS.prompt,
  };
}

function isInteractiveWorkspaceRole(role: WorkspaceRole): boolean {
  const explicitKind = role.kind?.trim().toLowerCase();
  if (explicitKind) return explicitKind === "interactive";
  const roleName = role.name.trim().toLowerCase();
  return roleName === "lead" || roleName === "orchestrator";
}

function isPromptAgentTaskFilterSupported(
  taskFilter: string | undefined,
): boolean {
  switch (taskFilter?.trim() ?? "") {
    case "":
    case "any":
    case "needs_plan":
    case "has_design":
    case "review":
    case "bug":
      return true;
    default:
      return false;
  }
}

function isCustomPromptAgentRoleCandidate(role: WorkspaceRole): boolean {
  const roleName = role.name.trim();
  return (
    roleName !== "" &&
    !BUILTIN_ROLE_NAMES.has(roleName.toLowerCase()) &&
    !isInteractiveWorkspaceRole(role) &&
    isPromptAgentTaskFilterSupported(role.task_filter) &&
    (role.task_filter?.trim() !== "bug" || role.read_only === true) &&
    (role.task_filter?.trim() !== "review" || role.read_only !== true)
  );
}

function isEmptyRoleList(value: string[] | undefined): boolean {
  return value === undefined || value.length === 0;
}

function isLegacyBugTriagePromptFile(promptFile: string | undefined): boolean {
  const normalized = promptFile?.replace(/\\/g, "/") ?? "";
  return ["bug-triage.md", LEGACY_BUG_TRIAGE_PROMPT_FILE_BASENAME].some(
    (basename) =>
      normalized === `.loom/prompts/${basename}` ||
      normalized.endsWith(`/.loom/prompts/${basename}`),
  );
}

/**
 * Recognize only the exact role definition shipped by the previous built-in
 * bug-triage card. This accepts both its original mutable prompt path and the
 * later immutable path. A 409 for anything else is shared user authority and
 * must remain a conflict rather than being bypassed by template provisioning.
 */
function isExactLegacyBugTriageRole(current: RoleWithPrompt): boolean {
  const role = current.role;
  const kind = role.kind?.trim() ?? "";
  return (
    role.name === BUG_TRIAGE_ROLE_NAME &&
    (kind === "" || kind === "worker") &&
    role.description === LEGACY_BUG_TRIAGE_DESCRIPTION &&
    (role.prompt?.trim() ?? "") === "" &&
    isLegacyBugTriagePromptFile(role.prompt_file) &&
    role.task_filter === "any" &&
    role.read_only === true &&
    (role.model?.trim() ?? "") === "" &&
    (role.backend?.trim() ?? "") === "" &&
    (role.effort?.trim() ?? "") === "" &&
    isEmptyRoleList(role.path_patterns) &&
    isEmptyRoleList(role.skills) &&
    isEmptyRoleList(role.allowed_tools) &&
    isEmptyRoleList(role.denied_tools) &&
    role.max_priority === undefined &&
    role.max_concurrency === undefined &&
    role.max_budget_usd === undefined &&
    current.prompt === LEGACY_BUG_TRIAGE_PROMPT
  );
}

async function ensureTemplateRole(
  workspaceId: string,
  request: CreateRoleRequest,
  ensureRole: (req: CreateRoleRequest) => Promise<WorkspaceRole>,
): Promise<WorkspaceRole> {
  try {
    return await ensureRole(request);
  } catch (error) {
    if (
      !(error instanceof ApiError) ||
      error.status !== 409 ||
      request.name !== BUG_TRIAGE_ROLE_NAME
    ) {
      throw error;
    }

    const current = await getWorkspaceRole(workspaceId, request.name);
    if (!isExactLegacyBugTriageRole(current)) {
      throw error;
    }

    // Never rewrite the shared legacy name from the browser: role PATCH has no
    // compare-and-swap precondition, so an operator edit racing this request
    // could otherwise be lost. Exact-ensure a reserved successor instead. The
    // server rejects an incompatible fallback collision, while an identical
    // existing successor remains idempotent.
    return ensureRole({
      ...request,
      name: BUG_TRIAGE_FALLBACK_ROLE_NAME,
    });
  }
}

/** Details of a workflow activation, surfaced to the caller on success. */
export interface WorkflowActivationResult {
  /** The display name the user gave the trigger binding. */
  name: string;
  /** Builtin workflow that was activated (e.g. "review-loop-agent"). */
  workflow: string;
  /** Stable binding id created for the schedule. */
  bindingId: string;
}

/** Parse an "owner/name" GitHub slug from a repo's remote, or "" if unknown. */
function githubRepoSlug(repo: RepoInfo | undefined): string {
  if (!repo) return "";
  const src = (repo.remote_url || repo.remote || "").trim();
  const match = src.match(/github\.com[/:]([^/]+)\/(.+?)(?:\.git)?$/i);
  if (match && match[1] && match[2]) {
    return `${match[1]}/${match[2]}`;
  }
  return "";
}

function isBackendReady(
  backend:
    | {
        available: boolean;
        installed?: boolean;
      }
    | undefined,
): boolean {
  return backend?.available === true && backend.installed !== false;
}

function submissionErrorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "workflow activation failed";
}

export interface CreateAgentModalProps {
  isOpen: boolean;
  workspaceId: string;
  repos: RepoInfo[];
  defaultBackend?: string;
  defaultName?: string;
  /**
   * Pre-selected template, keyed by role. Collapses the legacy
   * `defaultRoleName` + `defaultKind` pair into one prop: "lead" selects the
   * Lead card, "plan"/"task" select the matching background worker.
   */
  defaultRole?: DefaultRole;
  /**
   * Constrain creation to one daemon-supervised role. This is intentionally
   * stronger than a default selection: onboarding and PR assignment require a
   * workspace-agent result through `onSuccess` and cannot accept a role-backed
   * prompt-agent binding or an interactive terminal agent.
   */
  supervisedRole?: SupervisedRole;
  /**
   * Deep-link to Settings, used by the review-loop template when the GitHub
   * runtime credential it reuses is not configured yet.
   */
  onOpenSettings?: () => void;
  onClose: () => void;
  onSuccess: (agent: WorkspaceAgentInfo) => void;
  /**
   * Called after a `workflow` template activates (cron binding created, plus
   * connector + grants for review). No agent row exists, so this is distinct
   * from `onSuccess`. Owns closing the modal, mirroring `onSuccess`.
   */
  onWorkflowActivated?: (result: WorkflowActivationResult) => void;
}

export function CreateAgentModal({
  isOpen,
  workspaceId,
  repos,
  defaultBackend,
  defaultName,
  defaultRole,
  supervisedRole,
  onOpenSettings,
  onClose,
  onSuccess,
  onWorkflowActivated,
}: CreateAgentModalProps): JSX.Element | null {
  const navigate = useNavigate();
  const resolvedDefaultBackend = defaultBackend?.trim() || "codex";
  const resolvedDefaultName = defaultName?.trim() ?? "";
  const initialTemplate = supervisedRole
    ? supervisedTemplateForRole(supervisedRole)
    : templateForRole(defaultRole);

  const [name, setName] = useState(resolvedDefaultName);
  const [selectedTemplateId, setSelectedTemplateId] = useState<string>(
    initialTemplate.id,
  );
  const [backend, setBackend] = useState(resolvedDefaultBackend);
  const [selectedRepos, setSelectedRepos] = useState<string[]>([]);
  const [cadence, setCadence] = useState<string>(DEFAULT_CADENCE);
  const [newRoleName, setNewRoleName] = useState<string>("");
  const [rolePrompt, setRolePrompt] = useState<string>("");
  const [newRoleTrigger, setNewRoleTrigger] = useState<RoleTrigger>("ready");
  const [existingRoles, setExistingRoles] = useState<WorkspaceRole[]>([]);
  const [selectedBuiltinPromptID, setSelectedBuiltinPromptID] = useState<
    string | null
  >(supervisedRole ? null : defaultRole === "lead" ? "lead" : null);
  const [customPrompt, setCustomPrompt] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wasOpenRef = useRef(false);
  const nameRef = useRef<HTMLInputElement>(null);
  const createAgent = useCreateWorkspaceAgent(workspaceId);
  const ensureRole = useEnsureWorkspaceRole(workspaceId);
  const {
    backends,
    isLoading: backendsLoading,
    error: backendsError,
  } = useBackends(isOpen);
  // active=false: mutations do not require loading the automations catalog.
  const { createBinding, updateBinding, setEnabled } = useAutomations(
    workspaceId,
    false,
  );
  const { preflightCredential, ensureConnector, replaceGrants } =
    useConnectorProvisioning(workspaceId);
  // Fetch only while open: this modal is mounted (closed) on every page load.
  const { settings: localSettings } = useLocalSettings(isOpen);
  const githubCredentialStatus =
    localSettings?.runtime_credentials?.github ?? null;
  const githubConfigured = githubCredentialStatus?.configured ?? false;
  // Older servers/tests may omit `usable`; submit-time preflight remains the
  // authority, while the field lets current servers warn before submission.
  const githubUsable = githubCredentialStatus?.usable ?? githubConfigured;
  const { prompts: fetchedInteractivePrompts, error: promptLoadError } =
    useInteractivePrompts(workspaceId, isOpen);

  const customRoleTemplates = useMemo(
    () =>
      existingRoles
        .filter(isCustomPromptAgentRoleCandidate)
        .sort((a, b) => a.name.localeCompare(b.name))
        .map(customRoleTemplate),
    [existingRoles],
  );
  const behaviorTemplates = useMemo(
    () =>
      supervisedRole
        ? []
        : [
            ...BUILTIN_ROLE_TEMPLATES,
            ...customRoleTemplates,
            NEW_ROLE_TEMPLATE,
            ...SCRIPTED_WORKFLOW_TEMPLATES,
          ],
    [customRoleTemplates, supervisedRole],
  );
  // Lead is rendered with the v5 interactive prompts. Its payload remains the
  // legacy lead payload, so keeping the old advanced card would be a duplicate.
  const legacyDaemonTemplates = useMemo(() => {
    if (supervisedRole) return [supervisedTemplateForRole(supervisedRole)];
    return LEGACY_DAEMON_TEMPLATES.filter(
      (template) => template.kind !== "lead",
    );
  }, [supervisedRole]);
  const allTemplates = useMemo(
    () => [...behaviorTemplates, ...legacyDaemonTemplates],
    [behaviorTemplates, legacyDaemonTemplates],
  );
  const selectedTemplate: AgentTemplate =
    allTemplates.find((t) => t.id === selectedTemplateId) ?? initialTemplate;

  const isInteractive =
    supervisedRole === undefined && selectedBuiltinPromptID !== null;
  const isRoleBehavior =
    !isInteractive &&
    (selectedTemplate.kind === "role" ||
      selectedTemplate.kind === "role-create");
  const isRoleCreate =
    !isInteractive && selectedTemplate.kind === "role-create";
  const isLegacyDaemon =
    !isInteractive &&
    (selectedTemplate.kind === "lead" ||
      selectedTemplate.kind === "builtin-role" ||
      selectedTemplate.kind === "custom-role");
  const isWorkflow = !isInteractive && selectedTemplate.kind === "workflow";
  const selectedRoleTrigger = isRoleCreate
    ? newRoleTrigger
    : (selectedTemplate.roleTrigger ?? "ready");
  const selectedRoleTriggerOption = roleTriggerOption(selectedRoleTrigger);
  const workflowSpec = isWorkflow ? selectedTemplate.workflow : undefined;
  const needsConnector = (workflowSpec?.grants?.length ?? 0) > 0;
  const needsGitHub = workflowSpec?.requiresGitHub === true;
  const showCadence = isWorkflow;
  const isActivation = isWorkflow || isRoleBehavior;
  const showBackend = isInteractive || isRoleBehavior || isLegacyDaemon;
  // Interactive agents use the same explicit repository-scope contract as
  // supervised agents: selected chips scope the agent to those repositories,
  // while an empty selection grants workspace-wide scope. Keeping these
  // controls visible prevents the default first repository from becoming an
  // invisible, immutable choice.
  const showRepos = !isRoleBehavior;
  // Scripted workflows do not expose a backend picker. Review/local-review
  // require Codex explicitly; bug-fix intentionally keeps using the current
  // workspace/default backend and only verifies that it is runnable.
  const workflowBackendName = isWorkflow
    ? workflowSpec?.requiredBackend?.trim() || resolvedDefaultBackend
    : "";
  const submissionBackendName = isWorkflow
    ? workflowBackendName
    : showBackend
      ? backend.trim()
      : "";
  const needsBackendHealth = isWorkflow || showBackend;
  const submissionBackend = backends.find(
    (candidate) => candidate.name === submissionBackendName,
  );
  const backendReadinessMessage = !needsBackendHealth
    ? null
    : backendsLoading
      ? "Checking AI backend availability. Wait before submitting."
      : backendsError
        ? `Could not verify AI backend availability: ${backendsError}`
        : backends.length === 0
          ? "No available AI backends were detected."
          : !isBackendReady(submissionBackend)
            ? `${submissionBackend?.displayName || submissionBackendName || "Selected backend"} is unavailable or not installed. Configure it before ${
                isWorkflow ? "activating this workflow" : "creating this agent"
              }.`
            : null;
  const selectedRoleName = isRoleCreate
    ? normalizeStoredAgentName(newRoleName)
    : selectedTemplate.roleName.trim();
  const behaviorSection = TEMPLATE_SECTIONS[0]!;
  const advancedSection = TEMPLATE_SECTIONS[1]!;

  const interactivePrompts = useMemo(
    () =>
      fetchedInteractivePrompts.length > 0
        ? fetchedInteractivePrompts
        : DEFAULT_INTERACTIVE_PROMPTS,
    [fetchedInteractivePrompts],
  );

  const namePlaceholder = useMemo(() => {
    if (isInteractive) {
      if (selectedBuiltinPromptID === CUSTOM_PROMPT_ID) {
        return CUSTOM_PROMPT_TEMPLATE.placeholder;
      }
      const selectedPrompt = interactivePrompts.find(
        (prompt) => prompt.id === selectedBuiltinPromptID,
      );
      return selectedPrompt
        ? interactivePromptCard(selectedPrompt).placeholder
        : "reviewer";
    }
    return selectedTemplate.defaultName;
  }, [
    isInteractive,
    interactivePrompts,
    selectedBuiltinPromptID,
    selectedTemplate,
  ]);

  useEffect(() => {
    if (!isInteractive || selectedBuiltinPromptID === CUSTOM_PROMPT_ID) {
      return;
    }
    if (
      interactivePrompts.some((prompt) => prompt.id === selectedBuiltinPromptID)
    ) {
      return;
    }
    setSelectedBuiltinPromptID(interactivePrompts[0]?.id ?? "lead");
  }, [isInteractive, interactivePrompts, selectedBuiltinPromptID]);

  const repoOptions = useMemo(
    () =>
      repos.filter((repo) => !repo.is_linked_worktree).map((repo) => repo.name),
    [repos],
  );
  const defaultRepos = useMemo(
    () => (repoOptions[0] ? [repoOptions[0]] : []),
    [repoOptions],
  );

  // A workflow runs against a single target repo — the first selected chip.
  const targetRepo = useMemo(
    () => repos.find((repo) => repo.name === selectedRepos[0]),
    [repos, selectedRepos],
  );

  const crossRepo = selectedRepos.length === 0;
  const toggleRepo = (repo: string): void =>
    setSelectedRepos((prev) => {
      if (isWorkflow) {
        return prev.length === 1 && prev[0] === repo ? [] : [repo];
      }
      return prev.includes(repo)
        ? prev.filter((r) => r !== repo)
        : [...prev, repo];
    });

  const readyBackends = useMemo(
    () => backends.filter((candidate) => isBackendReady(candidate)),
    [backends],
  );
  const backendOptions = useMemo(
    () =>
      readyBackends.map((candidate) => ({
        value: candidate.name,
        label: candidate.displayName,
      })),
    [readyBackends],
  );
  const selectedBackendIsVisible = backendOptions.some(
    (option) => option.value === backend,
  );

  const resetToDefaults = useCallback((): void => {
    setName(resolvedDefaultName);
    setSelectedTemplateId(
      supervisedRole
        ? supervisedTemplateForRole(supervisedRole).id
        : templateForRole(defaultRole).id,
    );
    setBackend(resolvedDefaultBackend);
    setSelectedRepos(defaultRepos);
    setCadence(DEFAULT_CADENCE);
    setNewRoleName("");
    setRolePrompt("");
    setNewRoleTrigger("ready");
    setSelectedBuiltinPromptID(
      supervisedRole ? null : defaultRole === "lead" ? "lead" : null,
    );
    setCustomPrompt("");
  }, [
    resolvedDefaultName,
    resolvedDefaultBackend,
    defaultRepos,
    defaultRole,
    supervisedRole,
  ]);

  // Fetch roles while the modal is open, then hydrate each candidate through
  // the single-role endpoint. The list response intentionally omits prompt
  // bodies, while prompt-agent refuses roles without a readable prompt or with
  // an unsupported phase filter. Only publish cards that have passed the same
  // observable preconditions so every selectable behavior can be activated.
  // Failure still leaves builtin cards usable.
  useEffect(() => {
    if (!isOpen || supervisedRole) return;
    let cancelled = false;
    // Never carry a previously hydrated role across workspace/open
    // transitions while the new workspace's eligibility checks are pending.
    setExistingRoles([]);
    listWorkspaceRoles(workspaceId)
      .then(async (roles) => {
        const candidates = roles.filter(isCustomPromptAgentRoleCandidate);
        const hydrated = await Promise.all(
          candidates.map(async (listedRole) => {
            try {
              const detail = await getWorkspaceRole(
                workspaceId,
                listedRole.name,
              );
              const role = detail.role ?? listedRole;
              if (
                !isCustomPromptAgentRoleCandidate(role) ||
                detail.prompt.trim() === "" ||
                isExactLegacyBugTriageRole(detail)
              ) {
                return null;
              }
              return role;
            } catch {
              // A missing/unreadable role is not runnable by prompt-agent. Keep
              // the rest of the gallery available and omit only this card.
              return null;
            }
          }),
        );
        if (!cancelled) {
          setExistingRoles(
            hydrated.filter((role): role is WorkspaceRole => role !== null),
          );
        }
      })
      .catch(() => {
        if (!cancelled) setExistingRoles([]);
      });
    return () => {
      cancelled = true;
    };
  }, [isOpen, supervisedRole, workspaceId]);

  useEffect(() => {
    if (!isOpen) {
      wasOpenRef.current = false;
      return;
    }
    // Seed defaults only on the open transition, not on every re-render.
    if (wasOpenRef.current) return;
    wasOpenRef.current = true;
    resetToDefaults();
    setIsSubmitting(false);
    setError(null);
  }, [isOpen, resetToDefaults]);

  // Run after the open-transition reset above. If a configured/default backend
  // is unavailable, move interactive, role-backed, and legacy-agent creation
  // to the first healthy option. Workflows never take this fallback: their
  // backend is explicitly required or remains the workspace/default backend.
  useEffect(() => {
    if (
      !isOpen ||
      !showBackend ||
      backendsLoading ||
      backendsError ||
      selectedBackendIsVisible ||
      readyBackends.length === 0
    ) {
      return;
    }
    setBackend(readyBackends[0]!.name);
  }, [
    backendsError,
    backendsLoading,
    isOpen,
    readyBackends,
    selectedBackendIsVisible,
    showBackend,
  ]);

  useEffect(() => {
    if (isOpen) {
      nameRef.current?.focus();
    }
  }, [isOpen]);

  const nameError = validateStoredAgentName(name);
  const showNameError = name.length > 0 && nameError !== null;
  const roleNameError = isRoleCreate
    ? validateStoredAgentName(newRoleName)
    : null;
  const showRoleNameError = newRoleName.length > 0 && roleNameError !== null;
  const rolePromptError =
    isRoleCreate && rolePrompt.trim() === "" ? "Role prompt is required" : null;
  const roleCreateReady =
    !isRoleCreate || (roleNameError === null && rolePromptError === null);
  const hasPromptSelection =
    !isInteractive ||
    (selectedBuiltinPromptID === CUSTOM_PROMPT_ID
      ? customPrompt.trim() !== ""
      : (selectedBuiltinPromptID?.trim() ?? "") !== "");
  const legacyRepoReady = !isLegacyDaemon || repoOptions.length > 0;
  const workflowRepoReady =
    !isWorkflow || (selectedRepos.length === 1 && targetRepo !== undefined);
  const canSubmit =
    nameError === null &&
    roleCreateReady &&
    hasPromptSelection &&
    legacyRepoReady &&
    workflowRepoReady &&
    backendReadinessMessage === null &&
    !isSubmitting;

  const selectTemplate = (templateID: string): void => {
    setSelectedBuiltinPromptID(null);
    setSelectedTemplateId(templateID);
    const nextTemplate = allTemplates.find(
      (template) => template.id === templateID,
    );
    if (nextTemplate?.kind === "workflow") {
      setSelectedRepos((current) => current.slice(0, 1));
    }
  };

  const selectInteractive = (promptID: string): void => {
    setSelectedBuiltinPromptID(promptID);
  };

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    const trimmedName = normalizeStoredAgentName(name);
    const trimmedBackend = backend.trim();
    const nameError = validateStoredAgentName(name);
    if (nameError) {
      setError(nameError);
      return;
    }
    // Defense in depth for programmatic form submission: the disabled submit
    // button is not the authority. Backend readiness must be proven before any
    // agent, role, binding, connector, or grant mutation.
    if (backendReadinessMessage) {
      setError(backendReadinessMessage);
      return;
    }

    setIsSubmitting(true);
    try {
      if (isRoleBehavior) {
        if (roleNameError) {
          setError(roleNameError);
          return;
        }
        if (rolePromptError) {
          setError(rolePromptError);
          return;
        }

        const req: CreatePromptAgentRecordRequest = {
          kind: "prompt",
          name: trimmedName,
          ...(trimmedBackend ? { backend: trimmedBackend } : {}),
          behavior: {
            role_name: selectedRoleName,
            ...(isRoleCreate
              ? {
                  role_create: {
                    prompt: rolePrompt,
                    prompt_filename: rolePromptFilename(selectedRoleName),
                    ...(selectedTemplate.roleCreate?.description
                      ? { description: selectedTemplate.roleCreate.description }
                      : {}),
                    task_filter: selectedRoleTriggerOption.taskFilter,
                    ...(trimmedBackend ? { backend: trimmedBackend } : {}),
                  },
                }
              : {}),
          },
          trigger: {
            source_kind: "internal",
            event_type_patterns: [selectedRoleTriggerOption.eventTypePattern],
          },
          enabled: true,
        };

        const agentRecord = await createPromptAgentRecord(workspaceId, req);
        const bindingId = agentRecord.bindings?.[0]?.binding_id;
        if (!bindingId) {
          throw new Error("Created agent did not return a trigger binding.");
        }

        // The transactional create bypasses useAutomations.createBinding, so
        // mounted bindings consumers (AgentsPage's resolver, the sidebar) must
        // be nudged explicitly or navigation below resolves against a stale
        // list and renders an empty shell.
        dispatchBindingsChanged(workspaceId);
        onClose();
        resetToDefaults();
        // Route by the durable AgentService identity so its Runs tab can
        // aggregate history across every attached trigger binding.
        navigate(
          `/ws/${encodeURIComponent(workspaceId)}/agents/${encodeURIComponent(agentRecord.id)}`,
        );
        return;
      }

      // Workflow templates create a cron trigger binding — which self-heals and
      // activates the builtin workflow — instead of an agent row. The review
      // loop additionally reaches GitHub through a connector that reuses the
      // Settings runtime credential.
      if (isWorkflow && workflowSpec) {
        if (selectedRepos.length !== 1 || !targetRepo) {
          setError("Select exactly one target repo for this workflow.");
          return;
        }
        const wf = workflowSpec;
        const cron =
          CADENCE_OPTIONS.find((c) => c.value === cadence)?.cron ??
          CADENCE_OPTIONS[0].cron;

        // The connector path needs both a Settings token and a concrete target
        // repo to scope its grants — fail fast rather than half-provision.
        let targetSlug = "";
        if (needsGitHub) {
          if (!githubConfigured) {
            setError(
              "Connect a GitHub token in Settings before activating this workflow.",
            );
            return;
          }
          targetSlug = githubRepoSlug(targetRepo);
          if (!targetSlug) {
            setError(
              "Select a target repo with a GitHub remote for this workflow.",
            );
            return;
          }
          const readiness = await preflightCredential("github");
          if (!readiness.configured || !readiness.usable) {
            setError(
              "The GitHub token in Settings cannot be opened. Re-save it before activating this workflow.",
            );
            return;
          }
        }
        const runInput: Record<string, unknown> = {};
        if (targetRepo?.name) {
          runInput.targetRepo = targetRepo.name;
        }
        if (targetSlug) {
          runInput.githubRepo = targetSlug;
        }

        // Validate (or create) the exact active connector and usable sealed
        // credential before touching the singleton binding. A connector
        // collision or stale vault key therefore cannot leave an existing
        // workflow disabled or partially retargeted.
        if (needsConnector) {
          await ensureConnector({
            source: "github",
            connector_id: GITHUB_CONNECTOR_ID,
            reuse_runtime_credential: true,
          });
        }

        let bindingMustRemainDisabled = false;
        try {
          // Create/ensure disabled first. Repeated activation may return an
          // existing singleton, so explicitly disable before reconciling
          // mutable name/cadence/input. Any later failure is compensated with
          // another disable before the error is surfaced.
          await createBinding({
            workflow: wf.workflow,
            source_kind: "cron",
            schedule: cron,
            binding_id: wf.bindingId,
            name: trimmedName,
            run_input: runInput,
            enabled: false,
          });
          bindingMustRemainDisabled = true;
          await setEnabled(wf.bindingId, false);
          const disabledBinding = await updateBinding(wf.bindingId, {
            name: trimmedName,
            schedule: cron,
            run_input: runInput,
          });

          if (needsConnector) {
            if (
              !disabledBinding.created_at?.trim() ||
              !disabledBinding.updated_at?.trim()
            ) {
              throw new Error(
                "Updated workflow binding did not return its generation timestamps.",
              );
            }
            await replaceGrants(GITHUB_CONNECTOR_ID, wf.bindingId, {
              expected_binding_created_at: disabledBinding.created_at,
              expected_binding_updated_at: disabledBinding.updated_at,
              grants: grantsForRepo(wf, targetSlug).map((grant) => ({
                action: grant.action,
                resource_pattern: grant.resource,
              })),
            });
          }
          await setEnabled(wf.bindingId, true);
          bindingMustRemainDisabled = false;
        } catch (activationError) {
          if (bindingMustRemainDisabled) {
            try {
              await setEnabled(wf.bindingId, false);
            } catch (disableError) {
              throw new Error(
                `${submissionErrorMessage(activationError)}; additionally failed to leave binding disabled: ${submissionErrorMessage(disableError)}`,
              );
            }
          }
          throw activationError;
        }

        if (onWorkflowActivated) {
          onWorkflowActivated({
            name: trimmedName,
            workflow: wf.workflow,
            bindingId: wf.bindingId,
          });
        } else {
          onClose();
        }
        resetToDefaults();
        return;
      }

      // Custom-role templates provision their Role (and seed its prompt file)
      // on first use, before the agent that references the role is created.
      // The endpoint is idempotent, so re-creating the same template is safe.
      let roleName = selectedTemplate.roleName;
      if (
        !isInteractive &&
        selectedTemplate.kind === "custom-role" &&
        selectedTemplate.customRole
      ) {
        const cr = selectedTemplate.customRole;
        const roleRequest: CreateRoleRequest = {
          name: cr.roleName,
          prompt: cr.promptContent,
          prompt_filename: cr.promptFilename,
          ...(cr.description ? { description: cr.description } : {}),
          ...(cr.taskFilter ? { task_filter: cr.taskFilter } : {}),
          ...(cr.readOnly !== undefined ? { read_only: cr.readOnly } : {}),
          ...(cr.allowedTools ? { allowed_tools: cr.allowedTools } : {}),
          ...(cr.deniedTools ? { denied_tools: cr.deniedTools } : {}),
        };
        const ensuredRole = await ensureTemplateRole(
          workspaceId,
          roleRequest,
          ensureRole,
        );
        roleName = ensuredRole.name;
      }
      let interactiveFields: {
        kind?: "interactive";
        prompt?: string;
        prompt_file?: string;
      } = {};
      if (isInteractive) {
        const promptID = selectedBuiltinPromptID ?? "lead";
        if (promptID === CUSTOM_PROMPT_ID) {
          roleName = trimmedName;
          interactiveFields = {
            kind: "interactive",
            prompt: customPrompt.trim(),
          };
        } else if (promptID === "lead") {
          roleName = "lead";
        } else {
          roleName = promptID;
          interactiveFields = {
            kind: "interactive",
            prompt_file: `builtin:${promptID}`,
          };
        }
      }

      const request = {
        name: trimmedName,
        // Non-interactive templates keep their canonical role name. Interactive
        // prompts use the built-in prompt id (or the agent name for inline text).
        role_name: roleName,
        // Advanced workers have no browser-owned terminal or manual Start
        // control: the local-mode daemon manager discovers their workspace
        // only when at least one assignment is marked auto. Interactive agents
        // remain browser-launched and must never be daemon-supervised.
        auto: !isInteractive,
        cross_repo: crossRepo,
        repos: crossRepo ? [] : selectedRepos,
        ...interactiveFields,
      };
      const agent = await createAgent({
        ...request,
        ...(trimmedBackend ? { backend: trimmedBackend } : {}),
      });
      // The workspace-agent response predates role kinds and may omit them.
      // Preserve the kind selected in this modal so the shell can immediately
      // place every interactive template (not just legacy role names) in its
      // Terminal without waiting for a workspace refetch.
      onSuccess({
        ...agent,
        kind: isInteractive ? "interactive" : (agent.kind ?? "worker"),
      });
      resetToDefaults();
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else if (err instanceof Error) {
        setError(err.message);
      } else {
        setError("Failed to create agent");
      }
    } finally {
      setIsSubmitting(false);
    }
  };

  const repoHint = isWorkflow
    ? "Pick the target repo this workflow runs against."
    : crossRepo
      ? "No repo selected — the agent gets workspace-wide scope."
      : "Pick every repo this agent works in. Leave all unselected for workspace scope.";

  return (
    <AetherModal
      isOpen={isOpen}
      title="New Agent"
      ariaLabel="New Agent"
      onClose={onClose}
      overlayTestId="create-agent-overlay"
      closeTestId="create-agent-close"
      dialogClassName={aetherModalStyles.dialogWide}
      footer={
        <>
          <button
            type="button"
            className={aetherModalStyles.linkButton}
            onClick={onClose}
            disabled={isSubmitting}
          >
            Cancel
          </button>
          <button
            type="submit"
            form="create-agent-form"
            className={`${aetherModalStyles.primaryButton}${isSubmitting ? ` ${aetherModalStyles.submitting}` : ""}`}
            disabled={!canSubmit}
            data-testid="create-agent-submit"
          >
            {isSubmitting
              ? isActivation
                ? "Activating..."
                : "Creating..."
              : isActivation
                ? "Activate"
                : "Create Agent"}
          </button>
        </>
      }
    >
      <form
        id="create-agent-form"
        className={styles.form}
        onSubmit={handleSubmit}
      >
        <div className={styles.panel}>
          <h3 className={styles.panelHeader}>Agent type</h3>

          {supervisedRole ? (
            <div
              className={styles.group}
              role="group"
              aria-labelledby="create-agent-supervised-label"
              data-testid="create-agent-supervised-mode"
            >
              <span
                className={styles.groupLabel}
                id="create-agent-supervised-label"
              >
                Supervised agent
              </span>
              <p className={styles.groupHint}>
                This flow requires a daemon-supervised {supervisedRole} agent.
              </p>
              <div className={styles.templateList}>
                {legacyDaemonTemplates.map((template) => (
                  <AgentTemplateCard
                    key={template.id}
                    title={template.title}
                    description={template.description}
                    glyph={template.glyph}
                    accentColor={template.accentColor}
                    selected={selectedTemplateId === template.id}
                    disabled={isSubmitting}
                    ariaLabel={`${template.title}, supervised agent`}
                    testId={template.testId}
                    onSelect={() => selectTemplate(template.id)}
                  />
                ))}
              </div>
            </div>
          ) : (
            <>
              <div
                className={styles.group}
                role="group"
                aria-labelledby={behaviorSection.labelId}
              >
                <span
                  className={styles.groupLabel}
                  id={behaviorSection.labelId}
                >
                  {behaviorSection.label}
                </span>
                <p className={styles.groupHint}>{behaviorSection.hint}</p>
                <div className={styles.templateList}>
                  {behaviorTemplates.map((template) => (
                    <AgentTemplateCard
                      key={template.id}
                      title={template.title}
                      description={template.description}
                      glyph={template.glyph}
                      accentColor={template.accentColor}
                      tag={template.tag}
                      selected={
                        !isInteractive && selectedTemplateId === template.id
                      }
                      disabled={isSubmitting}
                      ariaLabel={`${template.title}, ${behaviorSection.label}`}
                      testId={template.testId}
                      onSelect={() => selectTemplate(template.id)}
                    />
                  ))}
                </div>
              </div>

              <div
                className={styles.group}
                role="group"
                aria-labelledby="create-agent-interactive-label"
              >
                <span
                  className={styles.groupLabel}
                  id="create-agent-interactive-label"
                >
                  Interactive agents
                </span>
                <p className={styles.groupHint}>
                  Terminal teammates you talk to directly
                </p>
                <div className={styles.templateList}>
                  {interactivePrompts.map((prompt) => {
                    const card = interactivePromptCard(prompt);
                    return (
                      <AgentTemplateCard
                        key={prompt.id}
                        title={prompt.label}
                        description={card.description}
                        glyph={card.glyph}
                        accentColor={card.accentColor}
                        selected={
                          isInteractive && selectedBuiltinPromptID === prompt.id
                        }
                        disabled={isSubmitting}
                        ariaLabel={`${prompt.label}, built-in interactive prompt`}
                        testId={card.testId}
                        onSelect={() => selectInteractive(prompt.id)}
                      />
                    );
                  })}
                  <AgentTemplateCard
                    title={CUSTOM_PROMPT_TEMPLATE.title}
                    description={CUSTOM_PROMPT_TEMPLATE.description}
                    glyph={CUSTOM_PROMPT_TEMPLATE.glyph}
                    accentColor={CUSTOM_PROMPT_TEMPLATE.accentColor}
                    selected={
                      isInteractive &&
                      selectedBuiltinPromptID === CUSTOM_PROMPT_ID
                    }
                    disabled={isSubmitting}
                    ariaLabel="Custom prompt, interactive agent"
                    testId={CUSTOM_PROMPT_TEMPLATE.testId}
                    onSelect={() => selectInteractive(CUSTOM_PROMPT_ID)}
                  />
                </div>
                {promptLoadError && (
                  <p className={styles.hint}>
                    Prompt list unavailable; showing built-in defaults.
                  </p>
                )}
              </div>

              <details className={styles.advancedGroup} open={isLegacyDaemon}>
                <summary className={styles.advancedSummary}>
                  <span
                    className={styles.groupLabel}
                    id={advancedSection.labelId}
                  >
                    {advancedSection.label}
                  </span>
                  <span className={styles.groupHint}>
                    {advancedSection.hint}
                  </span>
                </summary>
                <div
                  className={styles.templateList}
                  role="group"
                  aria-labelledby={advancedSection.labelId}
                >
                  {legacyDaemonTemplates.map((template) => (
                    <AgentTemplateCard
                      key={template.id}
                      title={template.title}
                      description={template.description}
                      glyph={template.glyph}
                      accentColor={template.accentColor}
                      selected={
                        !isInteractive && selectedTemplateId === template.id
                      }
                      disabled={isSubmitting}
                      ariaLabel={`${template.title}, ${advancedSection.label}`}
                      testId={template.testId}
                      onSelect={() => selectTemplate(template.id)}
                    />
                  ))}
                </div>
              </details>
            </>
          )}
        </div>

        <div className={styles.panel}>
          <h3 className={styles.panelHeader}>Configuration</h3>

          <div className={styles.configRow}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="agent-name">
                Name
              </label>
              <input
                id="agent-name"
                ref={nameRef}
                className={styles.input}
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={namePlaceholder}
                disabled={isSubmitting}
                aria-invalid={showNameError || undefined}
                aria-describedby={
                  showNameError ? "agent-name-error" : undefined
                }
                data-testid="create-agent-name"
              />
              {showNameError ? (
                <p
                  className={styles.fieldError}
                  id="agent-name-error"
                  data-testid="create-agent-name-error"
                >
                  {nameError}
                </p>
              ) : null}
            </div>

            {showCadence ? (
              <div className={styles.fieldGroup}>
                <label className={styles.label} htmlFor="agent-cadence">
                  Cadence
                </label>
                <select
                  id="agent-cadence"
                  className={styles.select}
                  value={cadence}
                  onChange={(event) => setCadence(event.target.value)}
                  disabled={isSubmitting}
                  data-testid="create-agent-cadence"
                >
                  {CADENCE_OPTIONS.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </div>
            ) : showBackend ? (
              <div className={styles.fieldGroup}>
                <label className={styles.label} htmlFor="agent-backend">
                  AI Backend
                </label>
                <select
                  id="agent-backend"
                  className={styles.select}
                  value={selectedBackendIsVisible ? backend : ""}
                  onChange={(event) => setBackend(event.target.value)}
                  disabled={
                    isSubmitting ||
                    backendsLoading ||
                    backendsError !== null ||
                    backendOptions.length === 0
                  }
                  data-testid="create-agent-backend"
                >
                  {!selectedBackendIsVisible && (
                    <option value="" disabled>
                      {backendsLoading
                        ? "Checking AI backends..."
                        : "Select an available backend"}
                    </option>
                  )}
                  {backendOptions.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </div>
            ) : null}
          </div>

          {needsBackendHealth && backendReadinessMessage && (
            <p
              className={styles.hint}
              data-testid="create-agent-backend-readiness"
            >
              {backendReadinessMessage}
            </p>
          )}

          {isInteractive && selectedBuiltinPromptID === CUSTOM_PROMPT_ID && (
            <div className={`${styles.fieldGroup} ${styles.fieldGroupSpaced}`}>
              <label className={styles.label} htmlFor="agent-custom-prompt">
                Custom prompt
              </label>
              <textarea
                id="agent-custom-prompt"
                className={styles.textarea}
                value={customPrompt}
                onChange={(event) => setCustomPrompt(event.target.value)}
                placeholder="Describe how this interactive agent should help..."
                disabled={isSubmitting}
                data-testid="create-agent-interactive-prompt"
              />
            </div>
          )}

          {isRoleBehavior && (
            <div
              className={`${styles.fieldGroup} ${styles.fieldGroupSpaced}`}
              data-testid="create-agent-role-trigger"
            >
              {isRoleCreate ? (
                <>
                  <label
                    className={styles.label}
                    htmlFor="prompt-agent-role-trigger"
                  >
                    Runs when
                  </label>
                  <select
                    id="prompt-agent-role-trigger"
                    className={styles.select}
                    value={newRoleTrigger}
                    onChange={(event) =>
                      setNewRoleTrigger(event.target.value as RoleTrigger)
                    }
                    disabled={isSubmitting}
                    data-testid="create-agent-role-trigger-select"
                  >
                    {ROLE_TRIGGER_OPTIONS.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </select>
                </>
              ) : (
                <>
                  <span className={styles.label}>Trigger</span>
                  <div className={styles.readOnlyField}>
                    {selectedRoleTriggerOption.readOnlyLabel}
                  </div>
                </>
              )}
            </div>
          )}

          {isRoleCreate && (
            <div
              className={`${styles.fieldGroup} ${styles.fieldGroupSpaced}`}
              data-testid="create-agent-new-role-config"
            >
              <div className={styles.fieldGroup}>
                <label
                  className={styles.label}
                  htmlFor="prompt-agent-role-name"
                >
                  New role name
                </label>
                <input
                  id="prompt-agent-role-name"
                  className={styles.input}
                  value={newRoleName}
                  onChange={(event) => setNewRoleName(event.target.value)}
                  placeholder="docs-assistant"
                  disabled={isSubmitting}
                  aria-invalid={showRoleNameError || undefined}
                  aria-describedby={
                    showRoleNameError
                      ? "prompt-agent-role-name-error"
                      : undefined
                  }
                  data-testid="create-agent-role-name"
                />
                {showRoleNameError ? (
                  <p
                    className={styles.fieldError}
                    id="prompt-agent-role-name-error"
                    data-testid="create-agent-role-name-error"
                  >
                    {roleNameError}
                  </p>
                ) : null}
              </div>
              <div className={styles.fieldGroup}>
                <label className={styles.label} htmlFor="prompt-agent-prompt">
                  Role prompt
                </label>
                <textarea
                  id="prompt-agent-prompt"
                  className={styles.input}
                  rows={8}
                  value={rolePrompt}
                  onChange={(event) => setRolePrompt(event.target.value)}
                  disabled={isSubmitting}
                  spellCheck={false}
                  data-testid="create-agent-role-prompt"
                />
                <p className={styles.hint}>
                  Every agent wearing this role shares this prompt.
                </p>
              </div>
            </div>
          )}

          {showRepos && (
            <div className={`${styles.fieldGroup} ${styles.fieldGroupSpaced}`}>
              <span className={styles.label} id="agent-repos-label">
                Repos
              </span>
              {repoOptions.length === 0 ? (
                <p
                  className={styles.emptyHint}
                  data-testid="create-agent-no-repos"
                >
                  {isLegacyDaemon
                    ? "No repos yet — add one from the sidebar before creating a legacy supervised agent."
                    : "No repos yet — add one from the sidebar to give this agent repository scope."}
                </p>
              ) : (
                <div
                  className={styles.repoChips}
                  role="group"
                  aria-labelledby="agent-repos-label"
                  data-testid="create-agent-repo-chips"
                >
                  {repoOptions.map((repo) => {
                    const on = selectedRepos.includes(repo);
                    return (
                      <button
                        key={repo}
                        type="button"
                        className={styles.repoChip}
                        data-active={on || undefined}
                        aria-pressed={on}
                        onClick={() => toggleRepo(repo)}
                        disabled={isSubmitting}
                      >
                        <span className={styles.repoChipBox} aria-hidden="true">
                          {on ? "✓" : ""}
                        </span>
                        {repo}
                      </button>
                    );
                  })}
                </div>
              )}
              <p className={styles.hint}>{repoHint}</p>
            </div>
          )}

          {isWorkflow && needsGitHub && !githubUsable && (
            <p
              className={styles.hint}
              data-testid="create-agent-review-needs-github"
            >
              {githubConfigured
                ? "This workflow requires a usable Settings GitHub token. The saved token cannot be opened; re-save it."
                : "This workflow requires your Settings GitHub token, which is not configured yet."}{" "}
              {onOpenSettings && (
                <button
                  type="button"
                  className={styles.settingsLink}
                  onClick={onOpenSettings}
                  data-testid="create-agent-open-settings"
                >
                  Open Settings
                </button>
              )}
            </p>
          )}
        </div>

        {error && (
          <div
            className={styles.error}
            role="alert"
            data-testid="create-agent-error"
          >
            {error}
          </div>
        )}
      </form>
    </AetherModal>
  );
}
