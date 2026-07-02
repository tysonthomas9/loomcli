import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import type { RepoInfo, WorkspaceAgentInfo, WorkspaceRole } from "@/api/workspace";
import { listWorkspaceRoles } from "@/api/workspace";
import {
  useCreateWorkspaceAgent,
  useEnsureWorkspaceRole,
} from "@/hooks/agents";
import {
  GITHUB_CONNECTOR_ID,
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
  AGENT_TEMPLATES,
  grantsForRepo,
  promptAgentBindingId,
  templateForRole,
  templatesForSection,
  TEMPLATE_SECTIONS,
  type AgentTemplate,
  type DefaultRole,
} from "./agentTemplates";
import styles from "./CreateAgentModal.module.css";

/** Cadence choices for a scheduled workflow, each mapping to a cron expression. */
const CADENCE_OPTIONS = [
  { value: "10m", label: "Every 10 minutes", cron: "*/10 * * * *" },
  { value: "hourly", label: "Hourly", cron: "0 * * * *" },
  { value: "daily", label: "Daily (09:00)", cron: "0 9 * * *" },
] as const;

const DEFAULT_CADENCE = CADENCE_OPTIONS[0].value;

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

// The agent-creation gallery renders the agent-producing kinds (lead +
// supervised workers, builtin or custom-role) plus the scheduled-workflow
// templates (bug-fix / review-loop) that create a cron trigger binding. Once a
// binding exists it is a first-class agent in the sidebar + detail page, so the
// retired Automations modal is no longer the home for scheduled workflows.
//
// Sections + their cards are resolved once at module load.
const SECTIONS = TEMPLATE_SECTIONS.map((meta) => ({
  meta,
  templates: templatesForSection(meta.id),
}));

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
  onOpenSettings,
  onClose,
  onSuccess,
  onWorkflowActivated,
}: CreateAgentModalProps): JSX.Element | null {
  const resolvedDefaultBackend = defaultBackend?.trim() || "codex";
  const resolvedDefaultName = defaultName?.trim() ?? "";
  const initialTemplate = templateForRole(defaultRole);

  const [name, setName] = useState(resolvedDefaultName);
  const [selectedTemplateId, setSelectedTemplateId] = useState<string>(
    initialTemplate.id,
  );
  const [backend, setBackend] = useState(resolvedDefaultBackend);
  const [selectedRepos, setSelectedRepos] = useState<string[]>([]);
  const [cadence, setCadence] = useState<string>(DEFAULT_CADENCE);
  // Prompt-agent role selection: pick an existing role, or the "__new__"
  // sentinel to create one from the name + prompt fields below.
  const NEW_ROLE = "__new__";
  const [roleChoice, setRoleChoice] = useState<string>(NEW_ROLE);
  const [newRoleName, setNewRoleName] = useState<string>("");
  const [rolePrompt, setRolePrompt] = useState<string>("");
  const [existingRoles, setExistingRoles] = useState<WorkspaceRole[]>([]);
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

  const selectedTemplate: AgentTemplate =
    AGENT_TEMPLATES.find((t) => t.id === selectedTemplateId) ?? initialTemplate;

  const isWorkflow = selectedTemplate.kind === "workflow";
  const isPromptAgent = selectedTemplate.kind === "prompt-agent";
  const promptAgentSpec = selectedTemplate.promptAgent;
  const workflowSpec = selectedTemplate.workflow;
  const needsConnector = (workflowSpec?.grants?.length ?? 0) > 0;
  // Prompt agents and scheduled workflows both fire on a cadence.
  const showCadence = isWorkflow || isPromptAgent;
  // The effective role name a prompt agent will wear: an existing pick, else the
  // typed new-role name.
  const promptRoleName = (
    roleChoice === NEW_ROLE ? newRoleName : roleChoice
  ).trim();

  const namePlaceholder = selectedTemplate.defaultName;

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
    setSelectedTemplateId(templateForRole(defaultRole).id);
    setBackend(resolvedDefaultBackend);
    setSelectedRepos(defaultRepos);
    setCadence(DEFAULT_CADENCE);
    setRoleChoice(NEW_ROLE);
    setNewRoleName("");
    setRolePrompt("");
  }, [resolvedDefaultName, resolvedDefaultBackend, defaultRepos, defaultRole]);

  // Seed the new-role name + prompt from the prompt-agent template the first
  // time it is selected, so the acceptance path (new role + docs prompt) starts
  // pre-filled and editable.
  useEffect(() => {
    if (isPromptAgent && promptAgentSpec) {
      setNewRoleName((prev) => prev || promptAgentSpec.roleName);
      setRolePrompt((prev) => prev || promptAgentSpec.promptContent);
    }
  }, [isPromptAgent, promptAgentSpec]);

  // Fetch existing roles for the prompt-agent role dropdown (best-effort: a
  // fetch failure just leaves the "Create new role" path).
  useEffect(() => {
    if (!isOpen || !isPromptAgent) return;
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
  }, [isOpen, isPromptAgent, workspaceId]);

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

  // A prompt agent also needs a role: an existing pick or a typed new-role name
  // (and, for a new role, a prompt body).
  const promptAgentReady =
    !isPromptAgent ||
    (promptRoleName !== "" &&
      (roleChoice !== NEW_ROLE || rolePrompt.trim() !== ""));
  const canSubmit =
    validateStoredAgentName(name) === null && promptAgentReady && !isSubmitting;

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

      // Prompt-agent templates: ensure the Role (prompt as data), then create a
      // cron trigger binding on the `prompt-agent` builtin whose run_input
      // carries the roleName (+ backend). The dispatch source merges run_input
      // into each fired run's payload, so the run resolves the role's prompt and
      // claims a ready task — no daemon supervisor. One prompt edit (from the
      // detail textarea, later) updates every agent wearing the role.
      if (isPromptAgent && promptAgentSpec) {
        const spec = promptAgentSpec;
        const roleName = promptRoleName;
        const cron =
          CADENCE_OPTIONS.find((c) => c.value === cadence)?.cron ??
          CADENCE_OPTIONS[0].cron;

        // A new role is ensured (idempotent) with its prompt body; an existing
        // pick is used as-is (its prompt is edited from the detail page).
        if (roleChoice === NEW_ROLE) {
          await ensureRole({
            name: roleName,
            prompt: rolePrompt,
            prompt_filename: spec.promptFilename,
            ...(spec.description ? { description: spec.description } : {}),
            ...(spec.taskFilter ? { task_filter: spec.taskFilter } : {}),
            ...(trimmedBackend ? { backend: trimmedBackend } : {}),
          });
        }

        const bindingId = promptAgentBindingId(spec, roleName);
        await createBinding({
          workflow: "prompt-agent",
          source_kind: "cron",
          schedule: cron,
          binding_id: bindingId,
          name: trimmedName,
          enabled: true,
          run_input: {
            roleName,
            ...(trimmedBackend ? { backend: trimmedBackend } : {}),
          },
        });

        if (onWorkflowActivated) {
          onWorkflowActivated({
            name: trimmedName,
            workflow: "prompt-agent",
            bindingId,
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
      if (selectedTemplate.kind === "custom-role" && selectedTemplate.customRole) {
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
      const request = {
        name: trimmedName,
        // roleName is the canonical role ("lead" | "plan" | "task" | a custom
        // role name) — never a display alias.
        role_name: selectedTemplate.roleName,
        auto: false,
        cross_repo: crossRepo,
        repos: crossRepo ? [] : selectedRepos,
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
              ? showCadence
                ? "Activating..."
                : "Creating..."
              : showCadence
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

          {SECTIONS.map(({ meta, templates }) => (
            <div
              key={meta.id}
              className={styles.group}
              role="group"
              aria-labelledby={meta.labelId}
            >
              <span className={styles.groupLabel} id={meta.labelId}>
                {meta.label}
              </span>
              <p className={styles.groupHint}>{meta.hint}</p>
              <div className={styles.templateList}>
                {templates.map((template) => (
                  <AgentTemplateCard
                    key={template.id}
                    title={template.title}
                    description={template.description}
                    glyph={template.glyph}
                    accentColor={template.accentColor}
                    selected={selectedTemplateId === template.id}
                    disabled={isSubmitting}
                    ariaLabel={`${template.title}, ${meta.label}`}
                    testId={template.testId}
                    onSelect={() => setSelectedTemplateId(template.id)}
                  />
                ))}
              </div>
            </div>
          ))}
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
                data-testid="create-agent-name"
              />
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
            ) : (
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
            )}
          </div>

          {isPromptAgent && (
            <div
              className={`${styles.fieldGroup} ${styles.fieldGroupSpaced}`}
              data-testid="create-agent-prompt-agent-config"
            >
              <div className={styles.configRow}>
                <div className={styles.fieldGroup}>
                  <label className={styles.label} htmlFor="prompt-agent-role">
                    Role
                  </label>
                  <select
                    id="prompt-agent-role"
                    className={styles.select}
                    value={roleChoice}
                    onChange={(event) => setRoleChoice(event.target.value)}
                    disabled={isSubmitting}
                    data-testid="create-agent-role-select"
                  >
                    <option value={NEW_ROLE}>+ Create new role…</option>
                    {existingRoles.map((role) => (
                      <option key={role.name} value={role.name}>
                        {role.name}
                      </option>
                    ))}
                  </select>
                </div>
                <div className={styles.fieldGroup}>
                  <label
                    className={styles.label}
                    htmlFor="prompt-agent-backend"
                  >
                    AI Backend
                  </label>
                  <select
                    id="prompt-agent-backend"
                    className={styles.select}
                    value={backend}
                    onChange={(event) => setBackend(event.target.value)}
                    disabled={isSubmitting}
                    data-testid="create-agent-prompt-agent-backend"
                  >
                    {backendOptions.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </select>
                </div>
              </div>

              {roleChoice === NEW_ROLE ? (
                <>
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
                      data-testid="create-agent-role-name"
                    />
                  </div>
                  <div className={styles.fieldGroup}>
                    <label
                      className={styles.label}
                      htmlFor="prompt-agent-prompt"
                    >
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
                      The prompt is the role's brain (data, not code). Every agent
                      wearing this role shares it; edit it later from the agent
                      detail page.
                    </p>
                  </div>
                </>
              ) : (
                <p className={styles.hint}>
                  This agent wears the existing role{" "}
                  <strong>{promptRoleName}</strong>. Edit its prompt from the
                  agent detail page.
                </p>
              )}
            </div>
          )}

          {/* A prompt agent claims ready tasks workspace-wide (no per-repo
              scope on the binding), so the repo picker does not apply. */}
          {!isPromptAgent && (
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
