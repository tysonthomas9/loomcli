import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import type {
  InteractivePromptInfo,
  RepoInfo,
  WorkspaceAgentInfo,
} from "@/api/workspace";
import { useCreateWorkspaceAgent, useInteractivePrompts } from "@/hooks/agents";
import { useBackends } from "@/hooks/workspace";
import { ApiError } from "@/types/common";
import {
  normalizeStoredAgentName,
  validateStoredAgentName,
} from "@/utils/agentName";

import { AgentTemplateCard } from "./AgentTemplateCard";
import styles from "./CreateAgentModal.module.css";

type AgentKind = "background" | "lead" | "interactive";
type BackgroundRole = "plan" | "task";
type PromptSource = "builtin" | "file";

const TEMPLATE_ACCENTS = {
  plan: "#0d9488",
  task: "#ea580c",
  lead: "#db2777",
  interactive: "#2563eb",
} as const;

const DEFAULT_INTERACTIVE_PROMPTS: InteractivePromptInfo[] = [
  { id: "lead", label: "Lead" },
  { id: "pr-review", label: "PR Review" },
];

const BACKGROUND_TEMPLATES: {
  role: BackgroundRole;
  title: string;
  description: string;
  glyph: string;
  placeholder: string;
  testId: string;
  accentColor: string;
}[] = [
  {
    role: "plan",
    title: "Planner",
    description: "Breaks epics into tasks under daemon supervision.",
    glyph: "P",
    placeholder: "planner",
    testId: "create-agent-template-planner",
    accentColor: TEMPLATE_ACCENTS.plan,
  },
  {
    role: "task",
    title: "Task Runner",
    description: "Claims and runs ready tasks under daemon supervision.",
    glyph: "T",
    placeholder: "worker",
    testId: "create-agent-template-task",
    accentColor: TEMPLATE_ACCENTS.task,
  },
];

const LEAD_TEMPLATE = {
  title: "Lead",
  description: "Orchestrates an epic interactively in a terminal.",
  glyph: "L",
  placeholder: "lead",
  testId: "create-agent-template-lead",
  accentColor: TEMPLATE_ACCENTS.lead,
};

const INTERACTIVE_TEMPLATE = {
  title: "Interactive",
  description: "Custom terminal teammate with a built-in or workspace prompt.",
  glyph: "I",
  placeholder: "reviewer",
  testId: "create-agent-template-interactive",
  accentColor: TEMPLATE_ACCENTS.interactive,
};

function resolveInitialSelection(
  defaultKind: AgentKind | undefined,
  defaultRoleName: BackgroundRole,
): { kind: AgentKind; backgroundRole: BackgroundRole } {
  if (defaultKind === "lead") {
    return { kind: "lead", backgroundRole: defaultRoleName };
  }
  if (defaultKind === "interactive") {
    return { kind: "interactive", backgroundRole: defaultRoleName };
  }
  return { kind: "background", backgroundRole: defaultRoleName };
}

export interface CreateAgentModalProps {
  isOpen: boolean;
  workspaceId: string;
  repos: RepoInfo[];
  defaultBackend?: string;
  defaultName?: string;
  defaultRoleName?: BackgroundRole;
  defaultKind?: AgentKind;
  onClose: () => void;
  onSuccess: (agent: WorkspaceAgentInfo) => void;
}

export function CreateAgentModal({
  isOpen,
  workspaceId,
  repos,
  defaultBackend,
  defaultName,
  defaultRoleName,
  defaultKind,
  onClose,
  onSuccess,
}: CreateAgentModalProps): JSX.Element | null {
  const resolvedDefaultBackend = defaultBackend?.trim() || "codex";
  const resolvedDefaultName = defaultName?.trim() ?? "";
  const resolvedDefaultRoleName = defaultRoleName ?? "task";
  const initialSelection = resolveInitialSelection(
    defaultKind,
    resolvedDefaultRoleName,
  );

  const [name, setName] = useState(resolvedDefaultName);
  const [selectedKind, setSelectedKind] = useState<AgentKind>(
    initialSelection.kind,
  );
  const [backgroundRole, setBackgroundRole] = useState<BackgroundRole>(
    initialSelection.backgroundRole,
  );
  const [backend, setBackend] = useState(resolvedDefaultBackend);
  const [selectedRepos, setSelectedRepos] = useState<string[]>([]);
  const [promptSource, setPromptSource] = useState<PromptSource>("builtin");
  const [selectedBuiltinPromptID, setSelectedBuiltinPromptID] =
    useState("pr-review");
  const [workspacePromptFile, setWorkspacePromptFile] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wasOpenRef = useRef(false);
  const nameRef = useRef<HTMLInputElement>(null);
  const createAgent = useCreateWorkspaceAgent(workspaceId);
  const { backends } = useBackends();
  const { prompts: fetchedInteractivePrompts, error: promptLoadError } =
    useInteractivePrompts(workspaceId);

  const interactivePrompts = useMemo(
    () =>
      fetchedInteractivePrompts.length > 0
        ? fetchedInteractivePrompts
        : DEFAULT_INTERACTIVE_PROMPTS,
    [fetchedInteractivePrompts],
  );

  const namePlaceholder = useMemo(() => {
    if (selectedKind === "lead") return LEAD_TEMPLATE.placeholder;
    if (selectedKind === "interactive") return INTERACTIVE_TEMPLATE.placeholder;
    const template = BACKGROUND_TEMPLATES.find(
      (t) => t.role === backgroundRole,
    );
    return template?.placeholder ?? "agent";
  }, [selectedKind, backgroundRole]);

  useEffect(() => {
    if (
      interactivePrompts.some((prompt) => prompt.id === selectedBuiltinPromptID)
    ) {
      return;
    }
    setSelectedBuiltinPromptID(interactivePrompts[0]?.id ?? "lead");
  }, [interactivePrompts, selectedBuiltinPromptID]);

  const repoOptions = useMemo(
    () =>
      repos.filter((repo) => !repo.is_linked_worktree).map((repo) => repo.name),
    [repos],
  );
  const defaultRepos = useMemo(
    () => (repoOptions[0] ? [repoOptions[0]] : []),
    [repoOptions],
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

  useEffect(() => {
    if (!isOpen) {
      wasOpenRef.current = false;
      return;
    }

    if (wasOpenRef.current) return;
    wasOpenRef.current = true;
    const selection = resolveInitialSelection(
      defaultKind,
      resolvedDefaultRoleName,
    );
    setName(resolvedDefaultName);
    setSelectedKind(selection.kind);
    setBackgroundRole(selection.backgroundRole);
    setBackend(resolvedDefaultBackend);
    setSelectedRepos(defaultRepos);
    setPromptSource("builtin");
    setSelectedBuiltinPromptID("pr-review");
    setWorkspacePromptFile("");
    setIsSubmitting(false);
    setError(null);
  }, [
    isOpen,
    resolvedDefaultName,
    resolvedDefaultRoleName,
    resolvedDefaultBackend,
    defaultRepos,
    defaultKind,
  ]);

  useEffect(() => {
    if (isOpen) {
      nameRef.current?.focus();
    }
  }, [isOpen]);

  const hasPromptSelection =
    selectedKind !== "interactive" ||
    (promptSource === "builtin" && selectedBuiltinPromptID.trim() !== "") ||
    (promptSource === "file" && workspacePromptFile.trim() !== "");
  const canSubmit =
    validateStoredAgentName(name) === null &&
    hasPromptSelection &&
    !isSubmitting;

  const selectBackground = (role: BackgroundRole): void => {
    setSelectedKind("background");
    setBackgroundRole(role);
  };

  const selectLead = (): void => {
    setSelectedKind("lead");
  };

  const selectInteractive = (): void => {
    setSelectedKind("interactive");
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
      let roleName: string;
      let promptFile: string | undefined;
      if (selectedKind === "lead") {
        roleName = "lead";
      } else if (selectedKind === "interactive") {
        if (promptSource === "builtin") {
          roleName = selectedBuiltinPromptID;
          promptFile = `builtin:${selectedBuiltinPromptID}`;
        } else {
          // Custom-file interactive roles use the agent name as the role slug so
          // repeated submissions are deterministic without another field.
          roleName = trimmedName;
          promptFile = workspacePromptFile.trim();
        }
      } else {
        roleName = backgroundRole;
      }

      const request = {
        name: trimmedName,
        role_name: roleName,
        auto: false,
        cross_repo: selectedKind === "interactive" ? false : crossRepo,
        repos: selectedKind === "interactive" || crossRepo ? [] : selectedRepos,
        ...(selectedKind === "interactive"
          ? { kind: "interactive", prompt_file: promptFile ?? "" }
          : {}),
      };
      const agent = await createAgent({
        ...request,
        ...(trimmedBackend ? { backend: trimmedBackend } : {}),
      });
      onSuccess(agent);
      const selection = resolveInitialSelection(
        defaultKind,
        resolvedDefaultRoleName,
      );
      setName(resolvedDefaultName);
      setSelectedKind(selection.kind);
      setBackgroundRole(selection.backgroundRole);
      setBackend(resolvedDefaultBackend);
      setSelectedRepos(defaultRepos);
      setPromptSource("builtin");
      setSelectedBuiltinPromptID("pr-review");
      setWorkspacePromptFile("");
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

  const repoHint = crossRepo
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
            {isSubmitting ? "Creating..." : "Create Agent"}
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

          <div
            className={styles.group}
            role="group"
            aria-labelledby="create-agent-background-label"
          >
            <span
              className={styles.groupLabel}
              id="create-agent-background-label"
            >
              Background agents
            </span>
            <p className={styles.groupHint}>
              Supervised workers that run automatically
            </p>
            <div className={styles.templateList}>
              {BACKGROUND_TEMPLATES.map((template) => (
                <AgentTemplateCard
                  key={template.role}
                  title={template.title}
                  description={template.description}
                  glyph={template.glyph}
                  accentColor={template.accentColor}
                  selected={
                    selectedKind === "background" &&
                    backgroundRole === template.role
                  }
                  disabled={isSubmitting}
                  ariaLabel={`${template.title}, background agent`}
                  testId={template.testId}
                  onSelect={() => selectBackground(template.role)}
                />
              ))}
            </div>
          </div>

          <div
            className={styles.group}
            role="group"
            aria-labelledby="create-agent-lead-label"
          >
            <span className={styles.groupLabel} id="create-agent-lead-label">
              Lead agent
            </span>
            <p className={styles.groupHint}>
              Interactive orchestrator in a terminal
            </p>
            <div className={styles.templateList}>
              <AgentTemplateCard
                title={LEAD_TEMPLATE.title}
                description={LEAD_TEMPLATE.description}
                glyph={LEAD_TEMPLATE.glyph}
                accentColor={LEAD_TEMPLATE.accentColor}
                selected={selectedKind === "lead"}
                disabled={isSubmitting}
                ariaLabel={`${LEAD_TEMPLATE.title}, lead agent`}
                testId={LEAD_TEMPLATE.testId}
                onSelect={selectLead}
              />
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
              <AgentTemplateCard
                title={INTERACTIVE_TEMPLATE.title}
                description={INTERACTIVE_TEMPLATE.description}
                glyph={INTERACTIVE_TEMPLATE.glyph}
                accentColor={INTERACTIVE_TEMPLATE.accentColor}
                selected={selectedKind === "interactive"}
                disabled={isSubmitting}
                ariaLabel={`${INTERACTIVE_TEMPLATE.title}, interactive agent`}
                testId={INTERACTIVE_TEMPLATE.testId}
                onSelect={selectInteractive}
              />
            </div>
          </div>
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
          </div>

          {selectedKind === "interactive" && (
            <div className={`${styles.fieldGroup} ${styles.fieldGroupSpaced}`}>
              <span className={styles.label} id="agent-prompt-source-label">
                Prompt source
              </span>
              <div
                className={styles.promptSourceGroup}
                role="radiogroup"
                aria-labelledby="agent-prompt-source-label"
              >
                <label className={styles.promptSourceOption}>
                  <input
                    type="radio"
                    name="agent-prompt-source"
                    value="builtin"
                    checked={promptSource === "builtin"}
                    onChange={() => setPromptSource("builtin")}
                    disabled={isSubmitting}
                  />
                  Built-in prompt
                </label>
                <label className={styles.promptSourceOption}>
                  <input
                    type="radio"
                    name="agent-prompt-source"
                    value="file"
                    checked={promptSource === "file"}
                    onChange={() => setPromptSource("file")}
                    disabled={isSubmitting}
                  />
                  Workspace file
                </label>
              </div>

              {promptSource === "builtin" ? (
                <select
                  className={styles.select}
                  value={selectedBuiltinPromptID}
                  onChange={(event) =>
                    setSelectedBuiltinPromptID(event.target.value)
                  }
                  disabled={isSubmitting}
                  data-testid="create-agent-interactive-builtin"
                >
                  {interactivePrompts.map((prompt) => (
                    <option key={prompt.id} value={prompt.id}>
                      {prompt.label}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  className={styles.input}
                  value={workspacePromptFile}
                  onChange={(event) =>
                    setWorkspacePromptFile(event.target.value)
                  }
                  placeholder="prompts/pr-review.md"
                  disabled={isSubmitting}
                  data-testid="create-agent-interactive-file"
                />
              )}
              {promptLoadError && (
                <p className={styles.hint}>
                  Prompt list unavailable; showing built-in defaults.
                </p>
              )}
            </div>
          )}

          {selectedKind !== "interactive" && (
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
