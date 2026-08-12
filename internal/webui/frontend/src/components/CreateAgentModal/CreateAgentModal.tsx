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
  InteractivePromptInfo,
  RepoInfo,
  WorkspaceAgentInfo,
  WorkspaceRole,
} from "@/api/workspace";
import { listWorkspaceRoles } from "@/api/workspace"; // eslint-disable-line boundaries/dependencies -- Pending hook migration.
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
  LEGACY_DAEMON_TEMPLATES,
  NEW_ROLE_TEMPLATE,
  SCRIPTED_WORKFLOW_TEMPLATES,
  customRoleTemplate,
  grantsForRepo,
  rolePromptFilename,
  supervisedTemplateForRole,
  templateForRole,
  TEMPLATE_SECTIONS,
  type AgentTemplate,
  type DefaultRole,
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
  const { backends } = useBackends();
  // active=false: we only need createBinding here, not the automations catalog.
  const { createBinding } = useAutomations(workspaceId, false);
  const { ensureConnector, addGrant } = useConnectorProvisioning(workspaceId);
  // Fetch only while open: this modal is mounted (closed) on every page load.
  const { settings: localSettings } = useLocalSettings(isOpen);
  const githubConfigured =
    localSettings?.runtime_credentials?.github?.configured ?? false;
  const { prompts: fetchedInteractivePrompts, error: promptLoadError } =
    useInteractivePrompts(workspaceId);

  const customRoleTemplates = useMemo(
    () =>
      existingRoles
        .filter((role) => {
          const roleName = role.name.trim();
          return (
            roleName !== "" &&
            !BUILTIN_ROLE_NAMES.has(roleName.toLowerCase()) &&
            !isInteractiveWorkspaceRole(role)
          );
        })
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
  const workflowSpec = isWorkflow ? selectedTemplate.workflow : undefined;
  const needsConnector = (workflowSpec?.grants?.length ?? 0) > 0;
  const showCadence = isWorkflow;
  const isActivation = isWorkflow || isRoleBehavior;
  const showBackend = isInteractive || isRoleBehavior || isLegacyDaemon;
  const showRepos = !isRoleBehavior;
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
    setSelectedRepos((prev) =>
      prev.includes(repo) ? prev.filter((r) => r !== repo) : [...prev, repo],
    );

  const backendOptions = useMemo(() => {
    const opts = backends.map((b) => ({ value: b.name, label: b.displayName }));
    if (backend && !opts.some((o) => o.value === backend)) {
      opts.unshift({ value: backend, label: backend });
    }
    return opts.length > 0 ? opts : [{ value: backend, label: backend }];
  }, [backend, backends]);

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

  // Fetch roles while the modal is open so the behavior grid can show every
  // custom role in the workspace. Failure still leaves builtin cards usable.
  useEffect(() => {
    if (!isOpen || supervisedRole) return;
    let cancelled = false;
    listWorkspaceRoles(workspaceId)
      .then((roles) => {
        if (!cancelled) setExistingRoles(roles);
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
  const canSubmit =
    nameError === null &&
    roleCreateReady &&
    hasPromptSelection &&
    !isSubmitting;

  const selectTemplate = (templateID: string): void => {
    setSelectedBuiltinPromptID(null);
    setSelectedTemplateId(templateID);
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
                    ...(selectedTemplate.roleCreate?.taskFilter
                      ? { task_filter: selectedTemplate.roleCreate.taskFilter }
                      : {}),
                    ...(trimmedBackend ? { backend: trimmedBackend } : {}),
                  },
                }
              : {}),
          },
          trigger: {
            source_kind: "internal",
            event_type_patterns: ["internal.task.ready"],
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
        // The detail resolver still accepts binding ids; a later wave switches
        // this route to the durable agent record id returned as agentRecord.id.
        navigate(
          `/ws/${encodeURIComponent(workspaceId)}/agents/${encodeURIComponent(bindingId)}`,
        );
        return;
      }

      // Workflow templates create a cron trigger binding — which self-heals and
      // activates the builtin workflow — instead of an agent row. The review
      // loop additionally reaches GitHub through a connector that reuses the
      // Settings runtime credential.
      if (isWorkflow && workflowSpec) {
        const wf = workflowSpec;
        const cron =
          CADENCE_OPTIONS.find((c) => c.value === cadence)?.cron ??
          CADENCE_OPTIONS[0].cron;

        // The connector path needs both a Settings token and a concrete target
        // repo to scope its grants — fail fast rather than half-provision.
        let targetSlug = "";
        if (needsConnector) {
          if (!githubConfigured) {
            setError(
              "Connect a GitHub token in Settings before activating the review loop.",
            );
            return;
          }
          targetSlug = githubRepoSlug(targetRepo);
          if (!targetSlug) {
            setError(
              "Select a target repo with a GitHub remote for the review loop.",
            );
            return;
          }
        }

        // No route_key: for a cron binding the backend derives it from the
        // (unique) binding_id, so activating both S1 and S2 in one workspace no
        // longer collides on a shared route.
        await createBinding({
          workflow: wf.workflow,
          source_kind: "cron",
          schedule: cron,
          binding_id: wf.bindingId,
          name: trimmedName,
          enabled: true,
        });

        if (needsConnector) {
          await ensureConnector({
            source: "github",
            connector_id: GITHUB_CONNECTOR_ID,
            reuse_runtime_credential: true,
          });
          await Promise.all(
            grantsForRepo(wf, targetSlug).map((grant) =>
              addGrant(GITHUB_CONNECTOR_ID, {
                binding_id: wf.bindingId,
                action: grant.action,
                resource_pattern: grant.resource,
              }),
            ),
          );
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
      if (
        !isInteractive &&
        selectedTemplate.kind === "custom-role" &&
        selectedTemplate.customRole
      ) {
        const cr = selectedTemplate.customRole;
        await ensureRole({
          name: cr.roleName,
          prompt: cr.promptContent,
          prompt_filename: cr.promptFilename,
          ...(cr.description ? { description: cr.description } : {}),
          ...(cr.taskFilter ? { task_filter: cr.taskFilter } : {}),
          ...(cr.readOnly !== undefined ? { read_only: cr.readOnly } : {}),
          ...(cr.allowedTools ? { allowed_tools: cr.allowedTools } : {}),
          ...(cr.deniedTools ? { denied_tools: cr.deniedTools } : {}),
        });
      }
      let roleName = selectedTemplate.roleName;
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
        auto: false,
        cross_repo: crossRepo,
        repos: crossRepo ? [] : selectedRepos,
        ...interactiveFields,
      };
      const agent = await createAgent({
        ...request,
        ...(trimmedBackend ? { backend: trimmedBackend } : {}),
      });
      onSuccess(agent);
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
                  value={backend}
                  onChange={(event) => setBackend(event.target.value)}
                  disabled={isSubmitting}
                  data-testid="create-agent-backend"
                >
                  {backendOptions.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </div>
            ) : null}
          </div>

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
              <span className={styles.label}>Trigger</span>
              <div className={styles.readOnlyField}>
                Runs when a task becomes ready
              </div>
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
                  No repos yet — add one from the sidebar first. This agent will
                  run with workspace scope.
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

          {isWorkflow && needsConnector && !githubConfigured && (
            <p
              className={styles.hint}
              data-testid="create-agent-review-needs-github"
            >
              The review loop reuses your Settings GitHub token, which is not
              configured yet.{" "}
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
