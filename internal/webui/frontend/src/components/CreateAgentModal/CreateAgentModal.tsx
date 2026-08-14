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
import {
  presentServerErrorMessage,
  type PresentedErrorMessage,
} from "@/utils/errorMessagePresenter";

import { AgentTemplateCard } from "./AgentTemplateCard";
import styles from "./CreateAgentModal.module.css";

type AgentKind = "background" | "interactive";
type BackgroundRole = "plan" | "task";
type TemplateSelection =
  | { kind: "background"; role: BackgroundRole }
  | { kind: "interactive"; promptID: string };

const CUSTOM_PROMPT_ID = "custom";

const TEMPLATE_ACCENTS = {
  plan: "#0d9488",
  task: "#ea580c",
  lead: "#db2777",
  interactive: "#2563eb",
  custom: "#7c3aed",
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

const CUSTOM_PROMPT_TEMPLATE = {
  title: "Custom prompt",
  description: "Define a terminal teammate with your own inline instructions.",
  glyph: "✦",
  placeholder: "reviewer",
  testId: "create-agent-template-custom-prompt",
  accentColor: TEMPLATE_ACCENTS.custom,
};

function interactivePromptCard(prompt: InteractivePromptInfo) {
  if (prompt.id === "lead") {
    return {
      description: "Orchestrates work interactively in a terminal.",
      glyph: "L",
      placeholder: "lead",
      testId: "create-agent-template-lead",
      accentColor: TEMPLATE_ACCENTS.lead,
    };
  }
  if (prompt.id === "pr-review") {
    return {
      description: "Reviews pull requests with focused terminal guidance.",
      glyph: "R",
      placeholder: "reviewer",
      testId: "create-agent-template-interactive-pr-review",
      accentColor: TEMPLATE_ACCENTS.interactive,
    };
  }
  return {
    description:
      "Starts an interactive terminal agent with this built-in prompt.",
    glyph: prompt.label.trim().charAt(0).toUpperCase() || "I",
    placeholder: prompt.id,
    testId: `create-agent-template-interactive-${prompt.id}`,
    accentColor: TEMPLATE_ACCENTS.interactive,
  };
}

function resolveInitialSelection(
  defaultKind: AgentKind | undefined,
  defaultRoleName: BackgroundRole,
): TemplateSelection {
  if (defaultKind === "interactive") {
    return { kind: "interactive", promptID: "pr-review" };
  }
  return { kind: "background", role: defaultRoleName };
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
  const [nameTouched, setNameTouched] = useState(false);
  const [selection, setSelection] =
    useState<TemplateSelection>(initialSelection);
  const [backend, setBackend] = useState(resolvedDefaultBackend);
  const [selectedRepos, setSelectedRepos] = useState<string[]>([]);
  const [customPrompt, setCustomPrompt] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<PresentedErrorMessage | null>(null);
  const [showScrollHint, setShowScrollHint] = useState(false);
  const wasOpenRef = useRef(false);
  const nameRef = useRef<HTMLInputElement>(null);
  const modalBodyRef = useRef<HTMLDivElement>(null);
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
    if (selection.kind === "interactive") {
      if (selection.promptID === CUSTOM_PROMPT_ID) {
        return CUSTOM_PROMPT_TEMPLATE.placeholder;
      }
      const selectedPrompt = interactivePrompts.find(
        (prompt) => prompt.id === selection.promptID,
      );
      return selectedPrompt
        ? interactivePromptCard(selectedPrompt).placeholder
        : "reviewer";
    }
    const template = BACKGROUND_TEMPLATES.find(
      (t) => t.role === selection.role,
    );
    return template?.placeholder ?? "agent";
  }, [selection, interactivePrompts]);

  useEffect(() => {
    if (selection.kind !== "interactive") {
      return;
    }
    if (selection.promptID === CUSTOM_PROMPT_ID) {
      return;
    }
    if (interactivePrompts.some((prompt) => prompt.id === selection.promptID)) {
      return;
    }
    setSelection({
      kind: "interactive",
      promptID: interactivePrompts[0]?.id ?? "lead",
    });
  }, [interactivePrompts, selection]);

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
    setNameTouched(false);
    setSelection(selection);
    setBackend(resolvedDefaultBackend);
    setSelectedRepos(defaultRepos);
    setCustomPrompt("");
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

  useEffect(() => {
    if (!isOpen) {
      setShowScrollHint(false);
      return;
    }
    const body = modalBodyRef.current;
    if (!body) return;

    const updateScrollHint = () => {
      const maxScrollTop = body.scrollHeight - body.clientHeight;
      setShowScrollHint(maxScrollTop > 2 && body.scrollTop < maxScrollTop - 2);
    };

    updateScrollHint();
    body.addEventListener("scroll", updateScrollHint, { passive: true });
    window.addEventListener("resize", updateScrollHint);

    let resizeObserver: ResizeObserver | null = null;
    if (typeof ResizeObserver !== "undefined") {
      resizeObserver = new ResizeObserver(updateScrollHint);
      resizeObserver.observe(body);
      if (body.firstElementChild) {
        resizeObserver.observe(body.firstElementChild);
      }
    }

    return () => {
      body.removeEventListener("scroll", updateScrollHint);
      window.removeEventListener("resize", updateScrollHint);
      resizeObserver?.disconnect();
    };
  }, [
    customPrompt,
    error,
    isOpen,
    name,
    promptLoadError,
    repoOptions.length,
    selection,
  ]);

  const nameValidationMessage = validateStoredAgentName(name);
  const showNameValidation =
    nameTouched && nameValidationMessage !== null && !isSubmitting;
  const hasPromptSelection =
    selection.kind !== "interactive" ||
    (selection.promptID === CUSTOM_PROMPT_ID
      ? customPrompt.trim() !== ""
      : selection.promptID.trim() !== "");
  const canSubmit =
    nameValidationMessage === null && hasPromptSelection && !isSubmitting;

  const selectBackground = (role: BackgroundRole): void => {
    setSelection({ kind: "background", role });
  };

  const selectInteractive = (promptID: string): void => {
    setSelection({ kind: "interactive", promptID });
  };

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    const trimmedName = normalizeStoredAgentName(name);
    const trimmedBackend = backend.trim();
    const nameError = validateStoredAgentName(name);
    if (nameError) {
      setNameTouched(true);
      return;
    }

    setIsSubmitting(true);
    try {
      let roleName: string;
      let interactiveFields: {
        kind?: "interactive";
        prompt?: string;
        prompt_file?: string;
      } = {};
      const isLeadSelection =
        selection.kind === "interactive" && selection.promptID === "lead";
      if (selection.kind === "interactive") {
        if (selection.promptID === CUSTOM_PROMPT_ID) {
          roleName = trimmedName;
          interactiveFields = {
            kind: "interactive",
            prompt: customPrompt.trim(),
          };
        } else if (isLeadSelection) {
          roleName = "lead";
        } else {
          roleName = selection.promptID;
          interactiveFields = {
            kind: "interactive",
            prompt_file: `builtin:${selection.promptID}`,
          };
        }
      } else {
        roleName = selection.role;
      }

      const request = {
        name: trimmedName,
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
      const resetSelection = resolveInitialSelection(
        defaultKind,
        resolvedDefaultRoleName,
      );
      setName(resolvedDefaultName);
      setNameTouched(false);
      setSelection(resetSelection);
      setBackend(resolvedDefaultBackend);
      setSelectedRepos(defaultRepos);
      setCustomPrompt("");
    } catch (err) {
      if (err instanceof ApiError) {
        setError(presentServerErrorMessage(err.message, err.status));
      } else if (err instanceof Error) {
        setError(presentServerErrorMessage(err.message));
      } else {
        setError(presentServerErrorMessage("Failed to create agent"));
      }
    } finally {
      setIsSubmitting(false);
    }
  };

  const repoHint =
    repoOptions.length === 0
      ? ""
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
      bodyRef={modalBodyRef}
      bodyClassName={styles.modalBody}
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
      <>
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
                      selection.kind === "background" &&
                      selection.role === template.role
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
                        selection.kind === "interactive" &&
                        selection.promptID === prompt.id
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
                    selection.kind === "interactive" &&
                    selection.promptID === CUSTOM_PROMPT_ID
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
                  onChange={(event) => {
                    setNameTouched(true);
                    setName(event.target.value);
                  }}
                  placeholder={namePlaceholder}
                  disabled={isSubmitting}
                  aria-invalid={showNameValidation ? true : undefined}
                  aria-describedby={
                    showNameValidation ? "agent-name-error" : undefined
                  }
                  data-testid="create-agent-name"
                />
                {showNameValidation && (
                  <p
                    id="agent-name-error"
                    className={styles.fieldError}
                    data-testid="create-agent-name-error"
                  >
                    {nameValidationMessage}
                  </p>
                )}
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

            {selection.kind === "interactive" &&
              selection.promptID === CUSTOM_PROMPT_ID && (
                <div
                  className={`${styles.fieldGroup} ${styles.fieldGroupSpaced}`}
                >
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

            <div className={`${styles.fieldGroup} ${styles.fieldGroupSpaced}`}>
              <span className={styles.label} id="agent-repos-label">
                Repos
              </span>
              {repoOptions.length === 0 ? (
                <p
                  className={styles.emptyHint}
                  data-testid="create-agent-no-repos"
                >
                  No repos in this workspace yet — background agents need at
                  least one repo; interactive agents run with workspace scope.
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
              {repoHint && <p className={styles.hint}>{repoHint}</p>}
            </div>
          </div>

          {error && (
            <div
              className={styles.error}
              role="alert"
              data-testid="create-agent-error"
              title={error.raw}
            >
              {error.message}
            </div>
          )}
        </form>
        {showScrollHint && (
          <div className={styles.scrollHint} aria-hidden="true" />
        )}
      </>
    </AetherModal>
  );
}
