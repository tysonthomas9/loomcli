import { useEffect, useMemo, useState, type FormEvent } from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import { WorkflowSourceModal } from "@/components/WorkflowSourceModal";
import { useAutomations } from "@/hooks/workspace";
import { apiErrorMessage } from "@/types/common";

import styles from "./AutomationsModal.module.css";

// The code-review automation's fixed wiring (the GitHub PR-review workflow).
const CODE_REVIEW = {
  workflowName: "github-review-agent",
  defaultRouteKey: "github.pull_request.opened",
  eventPatterns: ["github.pull_request.*"],
  defaultName: "code-review",
} as const;

export interface AutomationsModalProps {
  isOpen: boolean;
  workspaceId: string;
  onClose: () => void;
}

export function AutomationsModal({
  isOpen,
  workspaceId,
  onClose,
}: AutomationsModalProps): JSX.Element | null {
  const {
    workflows,
    bindings,
    error: loadError,
    createBinding,
    setEnabled,
    runWorkflow,
  } = useAutomations(workspaceId, isOpen);

  // Code-review binding form.
  const [bindingName, setBindingName] = useState<string>(
    CODE_REVIEW.defaultName,
  );
  const [routeKey, setRouteKey] = useState<string>(CODE_REVIEW.defaultRouteKey);
  const [secret, setSecret] = useState("");
  const [bindingBusy, setBindingBusy] = useState(false);
  const [bindingError, setBindingError] = useState<string | null>(null);
  const [bindingDone, setBindingDone] = useState<string | null>(null);
  const [togglingId, setTogglingId] = useState<string | null>(null);

  // Generic workflow runner.
  const [selectedWorkflow, setSelectedWorkflow] = useState("");
  const [payloadText, setPayloadText] = useState("{}");
  const [runBusy, setRunBusy] = useState(false);
  const [runError, setRunError] = useState<string | null>(null);
  const [runResult, setRunResult] = useState<string | null>(null);

  // Workflow source viewer/editor (Phase B). Only builtins expose source.
  const [sourceWorkflow, setSourceWorkflow] = useState("");
  const [showSource, setShowSource] = useState(false);
  const sourceWorkflows = useMemo(
    () => workflows.filter((w) => w.builtin),
    [workflows],
  );

  const defaultWorkflow = useMemo(() => {
    if (workflows.some((w) => w.name === "epic-runner")) return "epic-runner";
    return workflows[0]?.name ?? "";
  }, [workflows]);

  useEffect(() => {
    if (!selectedWorkflow && defaultWorkflow) {
      setSelectedWorkflow(defaultWorkflow);
    }
  }, [defaultWorkflow, selectedWorkflow]);

  useEffect(() => {
    if (!sourceWorkflow && sourceWorkflows[0]) {
      setSourceWorkflow(sourceWorkflows[0].name);
    }
  }, [sourceWorkflows, sourceWorkflow]);

  const handleCreateBinding = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    setBindingError(null);
    setBindingDone(null);
    if (!secret.trim()) {
      setBindingError("A webhook secret is required to enable code review.");
      return;
    }
    setBindingBusy(true);
    try {
      const binding = await createBinding({
        workflow: CODE_REVIEW.workflowName,
        route_key: routeKey.trim() || CODE_REVIEW.defaultRouteKey,
        source_kind: "github",
        name: bindingName.trim() || CODE_REVIEW.defaultName,
        secret: secret.trim(),
        event_type_patterns: [...CODE_REVIEW.eventPatterns],
        enabled: true,
      });
      setSecret("");
      setBindingDone(`Code review enabled (${binding.binding_id}).`);
    } catch (err) {
      setBindingError(apiErrorMessage(err, "Failed to create trigger binding"));
    } finally {
      setBindingBusy(false);
    }
  };

  const handleToggle = async (
    bindingId: string,
    nextEnabled: boolean,
  ): Promise<void> => {
    setBindingError(null);
    setTogglingId(bindingId);
    try {
      await setEnabled(bindingId, nextEnabled);
    } catch (err) {
      setBindingError(apiErrorMessage(err, "Failed to update trigger binding"));
    } finally {
      setTogglingId(null);
    }
  };

  const handleRun = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    setRunError(null);
    setRunResult(null);
    if (!selectedWorkflow) {
      setRunError("Pick a workflow to run.");
      return;
    }
    let payload: unknown;
    try {
      payload = payloadText.trim() ? JSON.parse(payloadText) : {};
    } catch {
      setRunError("Payload must be valid JSON.");
      return;
    }
    setRunBusy(true);
    try {
      const run = await runWorkflow(selectedWorkflow, payload);
      setRunResult(`Started ${run.run_id} (${run.status}).`);
    } catch (err) {
      setRunError(apiErrorMessage(err, "Failed to start workflow"));
    } finally {
      setRunBusy(false);
    }
  };

  return (
    <AetherModal
      isOpen={isOpen}
      title="Automations"
      ariaLabel="Automations"
      onClose={onClose}
      overlayTestId="automations-overlay"
      closeTestId="automations-close"
      dialogClassName={aetherModalStyles.dialogWide}
      footer={
        <button
          type="button"
          className={aetherModalStyles.linkButton}
          onClick={onClose}
        >
          Close
        </button>
      }
    >
      <div className={styles.form}>
        {/* Code review on PRs */}
        <form className={styles.panel} onSubmit={handleCreateBinding}>
          <h3 className={styles.panelHeader}>Code review on pull requests</h3>
          <p className={styles.panelHint}>
            Runs the {CODE_REVIEW.workflowName} workflow when a pull request is
            opened or updated, and comments its findings. Requires a GitHub
            webhook pointing at this workspace, signed with the secret below.
          </p>
          <div className={styles.row}>
            <div className={styles.field}>
              <label className={styles.label} htmlFor="automation-binding-name">
                Name
              </label>
              <input
                id="automation-binding-name"
                className={styles.input}
                value={bindingName}
                onChange={(e) => setBindingName(e.target.value)}
                disabled={bindingBusy}
                data-testid="automation-binding-name"
              />
            </div>
            <div className={styles.field}>
              <label
                className={styles.label}
                htmlFor="automation-binding-route"
              >
                Route key
              </label>
              <input
                id="automation-binding-route"
                className={styles.input}
                value={routeKey}
                onChange={(e) => setRouteKey(e.target.value)}
                disabled={bindingBusy}
                data-testid="automation-binding-route"
              />
            </div>
          </div>
          <div className={styles.field}>
            <label className={styles.label} htmlFor="automation-binding-secret">
              Webhook secret
            </label>
            <input
              id="automation-binding-secret"
              className={styles.input}
              type="password"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              placeholder="GitHub webhook HMAC secret"
              disabled={bindingBusy}
              data-testid="automation-binding-secret"
            />
          </div>
          <div className={styles.actionRow}>
            <button
              type="submit"
              className={styles.button}
              disabled={bindingBusy}
              data-testid="automation-create-binding"
            >
              {bindingBusy ? "Enabling..." : "Enable code review"}
            </button>
          </div>
          {bindingDone && (
            <p className={styles.result} data-testid="automation-binding-done">
              {bindingDone}
            </p>
          )}
          {bindingError && (
            <div
              className={styles.error}
              role="alert"
              data-testid="automation-binding-error"
            >
              {bindingError}
            </div>
          )}
        </form>

        {/* Existing trigger bindings */}
        <div className={styles.panel}>
          <h3 className={styles.panelHeader}>Active triggers</h3>
          {bindings.length === 0 ? (
            <p
              className={styles.emptyHint}
              data-testid="automation-no-bindings"
            >
              No trigger bindings yet.
            </p>
          ) : (
            <div
              className={styles.bindingList}
              data-testid="automation-binding-list"
            >
              {bindings.map((binding) => (
                <div key={binding.binding_id} className={styles.bindingItem}>
                  <div className={styles.bindingMeta}>
                    <span className={styles.bindingName}>{binding.name}</span>
                    <span className={styles.bindingRoute}>
                      {binding.route_key} → {binding.driver_id}
                    </span>
                  </div>
                  <span
                    className={`${styles.statusBadge} ${
                      binding.enabled ? styles.statusOn : styles.statusOff
                    }`}
                  >
                    {binding.enabled ? "On" : "Off"}
                  </span>
                  <button
                    type="button"
                    className={styles.toggleBtn}
                    disabled={togglingId === binding.binding_id}
                    onClick={() =>
                      handleToggle(binding.binding_id, !binding.enabled)
                    }
                    data-testid={`automation-binding-toggle-${binding.binding_id}`}
                  >
                    {binding.enabled ? "Disable" : "Enable"}
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Generic workflow runner */}
        <form className={styles.panel} onSubmit={handleRun}>
          <h3 className={styles.panelHeader}>Run a workflow</h3>
          <p className={styles.panelHint}>
            Start any workflow on demand with a JSON payload.
          </p>
          <div className={styles.field}>
            <label
              className={styles.label}
              htmlFor="automation-workflow-select"
            >
              Workflow
            </label>
            <select
              id="automation-workflow-select"
              className={styles.select}
              value={selectedWorkflow}
              onChange={(e) => setSelectedWorkflow(e.target.value)}
              disabled={runBusy}
              data-testid="automation-workflow-select"
            >
              {workflows.length === 0 && <option value="">No workflows</option>}
              {workflows.map((wf) => (
                <option key={wf.name} value={wf.name}>
                  {wf.name}
                </option>
              ))}
            </select>
          </div>
          <div className={styles.field}>
            <label className={styles.label} htmlFor="automation-payload">
              Payload (JSON)
            </label>
            <textarea
              id="automation-payload"
              className={styles.textarea}
              value={payloadText}
              onChange={(e) => setPayloadText(e.target.value)}
              disabled={runBusy}
              data-testid="automation-payload"
            />
          </div>
          <div className={styles.actionRow}>
            <button
              type="submit"
              className={styles.button}
              disabled={runBusy}
              data-testid="automation-run"
            >
              {runBusy ? "Starting..." : "Run workflow"}
            </button>
          </div>
          {runResult && (
            <p className={styles.result} data-testid="automation-run-result">
              {runResult}
            </p>
          )}
          {runError && (
            <div
              className={styles.error}
              role="alert"
              data-testid="automation-run-error"
            >
              {runError}
            </div>
          )}
        </form>

        {/* Workflow source (view / edit TS, rebuild a version) */}
        <div className={styles.panel}>
          <h3 className={styles.panelHeader}>Workflow source</h3>
          <p className={styles.panelHint}>
            View and edit a builtin workflow&apos;s TypeScript source, then
            rebuild it into a new driver version. Builds run the flue toolchain
            on the serve host and can fail — diagnostics are shown in full.
          </p>
          <div className={styles.field}>
            <label className={styles.label} htmlFor="automation-source-select">
              Workflow
            </label>
            <select
              id="automation-source-select"
              className={styles.select}
              value={sourceWorkflow}
              onChange={(e) => setSourceWorkflow(e.target.value)}
              data-testid="automation-source-select"
            >
              {sourceWorkflows.length === 0 && (
                <option value="">No workflows</option>
              )}
              {sourceWorkflows.map((wf) => (
                <option key={wf.name} value={wf.name}>
                  {wf.name}
                </option>
              ))}
            </select>
          </div>
          <div className={styles.actionRow}>
            <button
              type="button"
              className={styles.button}
              onClick={() => setShowSource(true)}
              disabled={!sourceWorkflow}
              data-testid="automation-open-source"
            >
              View / edit source
            </button>
          </div>
        </div>

        {loadError && (
          <div className={styles.error} role="alert">
            {loadError}
          </div>
        )}
      </div>
      {sourceWorkflow && (
        <WorkflowSourceModal
          isOpen={showSource}
          workspaceId={workspaceId}
          workflowName={sourceWorkflow}
          onClose={() => setShowSource(false)}
        />
      )}
    </AetherModal>
  );
}
