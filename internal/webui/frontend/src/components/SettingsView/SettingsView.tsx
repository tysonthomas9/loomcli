/**
 * SettingsView component.
 * Displays project backend configuration with a dropdown and per-agent overrides table.
 */

import { useEffect, useState } from "react";

import { ErrorDisplay } from "@/components/ErrorDisplay";
import { LoadingSkeleton } from "@/components/LoadingSkeleton";
import type { ViewMode } from "@/types";
import {
  AIBackendSetupList,
  type AIBackendSetupAction,
} from "@/components/AIBackendSetupList";
import {
  useBackendConfig,
  useBackends,
  useLocalSettings,
  useWorkspaceDesignFormat,
  useWorkspaceContext,
} from "@/hooks/workspace";
import type { BackendInfo } from "@/utils/workspace";
import { useToast } from "@/hooks/ui";
import { restartOnboarding } from "@/utils/onboardingState";
import { requestCliSetup } from "@/utils/cliSetup";

import styles from "./SettingsView.module.css";

export interface SettingsViewProps {
  className?: string;
  onNavigate?: (view: ViewMode) => void;
}

interface RedisFormState {
  enabled: boolean;
  url: string;
  addr: string;
  password: string;
  db: string;
  tls: boolean;
}

type AgentRuntimeDefault = "local" | "daytona";
type RuntimeCredentialProvider = "daytona" | "github";

const EMPTY_REDIS_FORM: RedisFormState = {
  enabled: false,
  url: "",
  addr: "",
  password: "",
  db: "0",
  tls: false,
};

export function SettingsView({
  className,
  onNavigate,
}: SettingsViewProps): JSX.Element {
  const { workspaceId, workspace } = useWorkspaceContext();
  const {
    config,
    isLoading,
    error,
    isSaving,
    isCached,
    updateBackend,
    refetch,
  } = useBackendConfig(workspaceId);
  const {
    backends,
    isLoading: isLoadingBackends,
    error: backendsError,
    refetch: refetchBackends,
  } = useBackends();
  const { showToast } = useToast();
  const [selectedBackend, setSelectedBackend] = useState<string | null>(null);
  const {
    settings: localSettings,
    isSaving: isSavingLocalSettings,
    error: localSettingsError,
    updateAgentRuntime,
    updateLocalTaskRunner,
    updateRedis,
    updateRuntimeCredentials,
  } = useLocalSettings();
  const redisSettings = localSettings?.fleetdb_redis;
  const runtimeSettings = localSettings?.agent_runtime;
  const localTaskRunnerSettings = localSettings?.local_task_runner;
  const runtimeCredentials = localSettings?.runtime_credentials;
  const [redisForm, setRedisForm] = useState<RedisFormState>(EMPTY_REDIS_FORM);
  const [agentRuntime, setAgentRuntime] =
    useState<AgentRuntimeDefault>("local");
  const [daytonaApiKey, setDaytonaApiKey] = useState("");
  const [githubToken, setGithubToken] = useState("");
  const [opencodeModel, setOpencodeModel] = useState("");
  const persistedDesignFormat = workspace?.design_format ?? "markdown";
  const [designFormat, setDesignFormat] = useState<"markdown" | "html">(
    persistedDesignFormat,
  );
  const { isSaving: isSavingDesignFormat, updateDesignFormat } =
    useWorkspaceDesignFormat();

  useEffect(() => {
    if (!redisSettings) return;
    setRedisForm({
      enabled: redisSettings.enabled,
      addr: redisSettings.addr ?? "",
      db: String(redisSettings.db ?? 0),
      tls: redisSettings.tls,
      password: "",
      url: "",
    });
  }, [redisSettings]);

  useEffect(() => {
    if (!runtimeSettings?.default) return;
    setAgentRuntime(runtimeSettings.default);
  }, [runtimeSettings?.default]);

  useEffect(() => {
    setOpencodeModel(localTaskRunnerSettings?.opencode_model ?? "");
  }, [localTaskRunnerSettings?.opencode_model]);

  useEffect(() => {
    setDesignFormat(persistedDesignFormat);
  }, [persistedDesignFormat, workspaceId]);

  const rootClassName = [styles.settingsView, className]
    .filter(Boolean)
    .join(" ");

  // Loading state
  if (isLoading && !config) {
    return (
      <div className={rootClassName} data-testid="settings-view">
        <h2 className={styles.pageTitle}>Settings</h2>
        <div className={styles.panel}>
          <div className={styles.panelHeader}>
            <h3 className={styles.panelTitle}>Project Default Backend</h3>
          </div>
          <div className={styles.panelContent}>
            <LoadingSkeleton shape="text" lines={3} />
          </div>
        </div>
      </div>
    );
  }

  // Error state (no data at all)
  if (error && !config) {
    return (
      <div className={rootClassName} data-testid="settings-view">
        <h2 className={styles.pageTitle}>Settings</h2>
        <ErrorDisplay
          variant="fetch-error"
          title="Backend configuration unavailable"
          error={new Error(error)}
          showDetails
          onRetry={refetch}
          isRetrying={isLoading}
        />
      </div>
    );
  }

  if (!config) return <div className={rootClassName} />;

  // Determine the current dropdown value
  const currentValue = selectedBackend ?? config.backend;
  const hasChanges = currentValue !== config.backend;

  // Check if any agents have backend overrides
  const agentsWithOverrides = config.agents.filter((a) => a.backend !== "");

  const handleSelectChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    setSelectedBackend(e.target.value);
  };

  const handleSave = async () => {
    if (!hasChanges || isSaving) return;

    const ok = await updateBackend(currentValue);
    if (ok) {
      setSelectedBackend(null);
      showToast("Backend updated successfully", { type: "success" });
    } else {
      showToast("Failed to update backend", { type: "error" });
    }
  };

  const handleDesignFormatSave = async () => {
    if (
      !workspaceId ||
      designFormat === persistedDesignFormat ||
      isSavingDesignFormat
    ) {
      return;
    }
    const ok = await updateDesignFormat(designFormat);
    showToast(
      ok
        ? "Design format updated successfully"
        : "Failed to update design format",
      { type: ok ? "success" : "error" },
    );
  };

  const handleRestartOnboarding = () => {
    restartOnboarding(workspaceId);
    showToast("Onboarding checklist restored", { type: "success" });
  };

  const handleBackendSetupAction = async (
    backend: BackendInfo,
    action: AIBackendSetupAction,
  ) => {
    if (action === "set-default") {
      const ok = await updateBackend(backend.name);
      if (ok) {
        setSelectedBackend(null);
        refetchBackends();
        showToast(`${backend.displayName} set as default`, {
          type: "success",
        });
      } else {
        showToast(`Failed to set ${backend.displayName} as default`, {
          type: "error",
        });
      }
      return;
    }
    requestCliSetup(backend, action);
    onNavigate?.("terminal");
  };

  const handleRedisUrlChange = (value: string) => {
    const trimmed = value.trim();
    setRedisForm((current) => ({
      ...current,
      url: value,
      tls:
        current.tls ||
        trimmed.startsWith("rediss://") ||
        /(^|\s)--tls(\s|$)/.test(value),
    }));
  };

  const handleRedisSave = async () => {
    if (isSavingLocalSettings) return;
    const db = Number(redisForm.db);
    if (!Number.isInteger(db) || db < 0) {
      showToast("Redis DB must be 0 or greater", { type: "error" });
      return;
    }
    const payload = {
      enabled: redisForm.enabled,
      db,
      tls: redisForm.tls,
      ...(redisForm.url.trim() ? { redis_url: redisForm.url.trim() } : {}),
      ...(!redisForm.url.trim() && redisForm.addr.trim()
        ? { addr: redisForm.addr.trim() }
        : {}),
      ...(redisForm.password ? { password: redisForm.password } : {}),
    };
    const ok = await updateRedis(payload);
    if (ok) {
      setRedisForm((current) => ({ ...current, password: "", url: "" }));
      showToast("Redis settings saved. Restart Loom to apply them.", {
        type: "success",
      });
    } else {
      showToast("Failed to save Redis settings", { type: "error" });
    }
  };

  const handleAgentRuntimeSave = async () => {
    if (isSavingLocalSettings) return;
    const ok = await updateAgentRuntime({ default: agentRuntime });
    showToast(
      ok ? "Agent runtime settings saved" : "Failed to save agent runtime",
      {
        type: ok ? "success" : "error",
      },
    );
  };

  const handleLocalTaskRunnerSave = async () => {
    if (isSavingLocalSettings) return;
    const ok = await updateLocalTaskRunner({
      opencode_model: opencodeModel.trim(),
    });
    showToast(
      ok
        ? "Local task runner settings saved"
        : "Failed to save local task runner settings",
      {
        type: ok ? "success" : "error",
      },
    );
  };

  const handleRuntimeCredentialSave = async (
    provider: RuntimeCredentialProvider,
  ) => {
    if (isSavingLocalSettings) return;
    const value =
      provider === "daytona" ? daytonaApiKey.trim() : githubToken.trim();
    if (!value) {
      showToast(
        provider === "daytona"
          ? "Enter a Daytona API key to save"
          : "Enter a GitHub token to save",
        {
          type: "error",
        },
      );
      return;
    }
    const payload =
      provider === "daytona"
        ? { daytona: { api_key: value } }
        : { github: { token: value } };
    const ok = await updateRuntimeCredentials(payload);
    if (ok) {
      if (provider === "daytona") {
        setDaytonaApiKey("");
      } else {
        setGithubToken("");
      }
      showToast("Runtime credentials saved", { type: "success" });
    } else {
      showToast("Failed to save runtime credentials", { type: "error" });
    }
  };

  const handleRuntimeCredentialClear = async (
    provider: RuntimeCredentialProvider,
  ) => {
    if (isSavingLocalSettings) return;
    const ok = await updateRuntimeCredentials({ [provider]: { clear: true } });
    showToast(
      ok
        ? `${provider === "daytona" ? "Daytona" : "GitHub"} credential cleared`
        : `Failed to clear ${provider === "daytona" ? "Daytona" : "GitHub"} credential`,
      { type: ok ? "success" : "error" },
    );
  };

  return (
    <div className={rootClassName} data-testid="settings-view">
      <h2 className={styles.pageTitle}>Settings</h2>

      {/* Onboarding */}
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>Onboarding</h3>
        </div>
        <div className={styles.panelContent}>
          <p className={styles.description}>
            Restore the setup checklist if it was dismissed before onboarding
            was complete.
          </p>
          <button
            type="button"
            className={styles.navButton}
            onClick={handleRestartOnboarding}
            data-testid="restart-onboarding-button"
          >
            Show Onboarding Checklist
          </button>
        </div>
      </div>

      {/* AI CLI status */}
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>AI CLIs</h3>
        </div>
        <div className={styles.panelContent}>
          <AIBackendSetupList
            backends={backends}
            defaultBackend={config.backend}
            registrableBackends={config.available}
            variant="matrix"
            isLoading={isLoadingBackends}
            error={backendsError}
            isSavingDefault={isSaving}
            onAction={handleBackendSetupAction}
          />
        </div>
      </div>

      {/* Project Default Backend */}
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>
            Project Default Backend
            {isCached && (
              <span className={styles.cachedBadge} data-testid="cached-badge">
                (cached)
              </span>
            )}
          </h3>
        </div>
        <div className={styles.panelContent}>
          <div className={styles.formGroup}>
            <label className={styles.label} htmlFor="backend-select">
              Default Backend
            </label>
            <p className={styles.description}>
              The AI backend used for new agents unless overridden.
            </p>
            <div className={styles.selectRow}>
              <select
                id="backend-select"
                className={styles.select}
                value={currentValue}
                onChange={handleSelectChange}
                data-testid="backend-select"
              >
                {config.available.map((b) => (
                  <option key={b} value={b}>
                    {b}
                  </option>
                ))}
              </select>
              <span className={styles.sourceTag}>
                {config.source === "fleetdb" ? "From FleetDB" : "Default"}
              </span>
            </div>
          </div>
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={!hasChanges || isSaving || isCached}
              onClick={handleSave}
              data-testid="save-button"
            >
              {isSaving ? "Saving..." : "Save"}
            </button>
          </div>
        </div>
      </div>

      {/* Planner Design Format */}
      <div className={styles.panel} data-testid="design-format-panel">
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>Planner Design Format</h3>
        </div>
        <div className={styles.panelContent}>
          <div className={styles.formGroup}>
            <label className={styles.label} htmlFor="design-format-select">
              Design format
            </label>
            <p className={styles.description}>
              Markdown is portable and remains the default. HTML enables richer
              layouts and sanitized inline SVG diagrams in issue designs.
            </p>
            <select
              id="design-format-select"
              className={styles.select}
              value={designFormat}
              onChange={(event) =>
                setDesignFormat(event.target.value as "markdown" | "html")
              }
              data-testid="design-format-select"
            >
              <option value="markdown">Markdown</option>
              <option value="html">HTML</option>
            </select>
          </div>
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={
                !workspaceId ||
                designFormat === persistedDesignFormat ||
                isSavingDesignFormat
              }
              onClick={handleDesignFormatSave}
              data-testid="design-format-save-button"
            >
              {isSavingDesignFormat ? "Saving..." : "Save Design Format"}
            </button>
          </div>
        </div>
      </div>

      {/* Agent Backend Overrides */}
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>Agent Backend Overrides</h3>
        </div>
        <div className={styles.panelContent}>
          {agentsWithOverrides.length > 0 ? (
            <table
              className={styles.agentTable}
              data-testid="agent-overrides-table"
            >
              <thead>
                <tr>
                  <th>Worktree</th>
                  <th>Role</th>
                  <th>Backend</th>
                </tr>
              </thead>
              <tbody>
                {agentsWithOverrides.map((agent) => (
                  <tr key={agent.worktree}>
                    <td>{agent.worktree}</td>
                    <td>{agent.role}</td>
                    <td>{agent.backend}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p
              className={styles.emptyMessage}
              data-testid="no-overrides-message"
            >
              No per-agent overrides configured.
            </p>
          )}
        </div>
      </div>

      {/* Remote runtimes */}
      <div className={styles.panel} data-testid="remote-runtimes-panel">
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>Remote runtimes</h3>
        </div>
        <div className={styles.panelContent}>
          <p className={styles.description}>
            Configure app-triggered task runtimes and Daytona access.
          </p>
          <div className={styles.formGroup}>
            <label className={styles.label} htmlFor="agent-runtime-select">
              Run task agents
            </label>
            <p className={styles.description}>
              App-triggered epic runs use this runtime for child task agents.
            </p>
            <select
              id="agent-runtime-select"
              className={styles.select}
              value={agentRuntime}
              onChange={(e) =>
                setAgentRuntime(e.target.value as AgentRuntimeDefault)
              }
              data-testid="agent-runtime-select"
            >
              <option value="local">Locally</option>
              <option value="daytona">On Daytona</option>
            </select>
          </div>
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={
                isSavingLocalSettings ||
                agentRuntime === (runtimeSettings?.default ?? "local")
              }
              onClick={handleAgentRuntimeSave}
              data-testid="agent-runtime-save-button"
            >
              {isSavingLocalSettings ? "Saving..." : "Save Agent Runtime"}
            </button>
          </div>
          <div className={styles.formGroup}>
            <label className={styles.label} htmlFor="opencode-model-input">
              Opencode Model
            </label>
            <p className={styles.description}>
              Used by app-triggered local epic runs when the project backend is
              opencode.
            </p>
            <input
              id="opencode-model-input"
              type="text"
              className={styles.input}
              value={opencodeModel}
              onChange={(e) => setOpencodeModel(e.target.value)}
              placeholder="provider/model"
              data-testid="opencode-model-input"
            />
          </div>
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={
                isSavingLocalSettings ||
                opencodeModel.trim() ===
                  (localTaskRunnerSettings?.opencode_model ?? "")
              }
              onClick={handleLocalTaskRunnerSave}
              data-testid="local-task-runner-save-button"
            >
              {isSavingLocalSettings
                ? "Saving..."
                : "Save Local Task Runner Settings"}
            </button>
          </div>
          <div className={styles.formGroup}>
            <label className={styles.label} htmlFor="daytona-api-key-input">
              Daytona API Key
            </label>
            <input
              id="daytona-api-key-input"
              type="password"
              className={styles.input}
              value={daytonaApiKey}
              onChange={(e) => setDaytonaApiKey(e.target.value)}
              placeholder={
                runtimeCredentials?.daytona.configured
                  ? "Saved key unchanged"
                  : "dtn_..."
              }
              data-testid="daytona-api-key-input"
            />
            <p className={styles.description}>
              {runtimeCredentials?.daytona.configured
                ? "Daytona credential saved"
                : "No Daytona credential saved"}
            </p>
            {runtimeCredentials?.daytona.configured && (
              <button
                type="button"
                className={styles.navButton}
                disabled={isSavingLocalSettings}
                onClick={() => handleRuntimeCredentialClear("daytona")}
                data-testid="daytona-credential-clear-button"
              >
                Clear Daytona Key
              </button>
            )}
          </div>
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={isSavingLocalSettings}
              onClick={() => handleRuntimeCredentialSave("daytona")}
              data-testid="daytona-credential-save-button"
            >
              {isSavingLocalSettings ? "Saving..." : "Save Daytona Credential"}
            </button>
          </div>
        </div>
      </div>

      {/* GitHub */}
      <div className={styles.panel} data-testid="github-settings-panel">
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>GitHub</h3>
        </div>
        <div className={styles.panelContent}>
          <div className={styles.formGroup}>
            <label className={styles.label} htmlFor="github-token-input">
              GitHub Token for Runtimes and PR Review
            </label>
            <input
              id="github-token-input"
              type="password"
              className={styles.input}
              value={githubToken}
              onChange={(e) => setGithubToken(e.target.value)}
              placeholder={
                runtimeCredentials?.github.configured
                  ? "Saved token unchanged"
                  : "github_pat_..."
              }
              data-testid="github-token-input"
            />
            <p className={styles.description}>
              {runtimeCredentials?.github.configured && "Credential saved. "}
              <span>
                Used for GitHub PR review and remote runtime provisioning.
              </span>
            </p>
            {runtimeCredentials?.github.configured && (
              <button
                type="button"
                className={styles.navButton}
                disabled={isSavingLocalSettings}
                onClick={() => handleRuntimeCredentialClear("github")}
                data-testid="github-credential-clear-button"
              >
                Clear GitHub Token
              </button>
            )}
          </div>
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={isSavingLocalSettings}
              onClick={() => handleRuntimeCredentialSave("github")}
              data-testid="github-credential-save-button"
            >
              {isSavingLocalSettings ? "Saving..." : "Save GitHub Credential"}
            </button>
          </div>
        </div>
      </div>

      {/* FleetDB Redis */}
      <div className={styles.panel} data-testid="fleetdb-redis-panel">
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>FleetDB Redis</h3>
        </div>
        <div className={styles.panelContent}>
          <div className={styles.formGroup}>
            <label className={styles.checkboxLabel}>
              <input
                type="checkbox"
                checked={redisForm.enabled}
                onChange={(e) =>
                  setRedisForm((current) => ({
                    ...current,
                    enabled: e.target.checked,
                  }))
                }
                data-testid="redis-enabled-checkbox"
              />
              Use external Redis for FleetDB
            </label>
            <p className={styles.description}>
              Stores workspace, task, repo, and agent state in a managed Redis
              instance instead of local embedded Redis.
            </p>
          </div>
          {redisForm.enabled && (
            <>
              <div className={styles.formGroup}>
                <label className={styles.label} htmlFor="redis-url-input">
                  Redis URL
                </label>
                <p className={styles.description}>
                  Paste a redis://, rediss://, or redis-cli -u command. The
                  saved password is never returned to the browser.
                </p>
                <input
                  id="redis-url-input"
                  type="password"
                  className={styles.input}
                  value={redisForm.url}
                  onChange={(e) => handleRedisUrlChange(e.target.value)}
                  placeholder="redis://default:password@host:6379"
                  data-testid="redis-url-input"
                />
              </div>
              <div className={styles.fieldGrid}>
                <div className={styles.formGroup}>
                  <label className={styles.label} htmlFor="redis-addr-input">
                    Address
                  </label>
                  <input
                    id="redis-addr-input"
                    type="text"
                    className={styles.input}
                    value={redisForm.addr}
                    onChange={(e) =>
                      setRedisForm((current) => ({
                        ...current,
                        addr: e.target.value,
                      }))
                    }
                    placeholder="host:6379"
                    data-testid="redis-addr-input"
                  />
                </div>
                <div className={styles.formGroup}>
                  <label className={styles.label} htmlFor="redis-db-input">
                    Database index
                  </label>
                  <input
                    id="redis-db-input"
                    type="number"
                    min={0}
                    className={styles.input}
                    value={redisForm.db}
                    onChange={(e) =>
                      setRedisForm((current) => ({
                        ...current,
                        db: e.target.value,
                      }))
                    }
                    data-testid="redis-db-input"
                  />
                </div>
                <div className={styles.formGroup}>
                  <label
                    className={styles.label}
                    htmlFor="redis-password-input"
                  >
                    Password
                  </label>
                  <input
                    id="redis-password-input"
                    type="password"
                    className={styles.input}
                    value={redisForm.password}
                    onChange={(e) =>
                      setRedisForm((current) => ({
                        ...current,
                        password: e.target.value,
                      }))
                    }
                    placeholder={
                      redisSettings?.password_set
                        ? "Saved password unchanged"
                        : "Optional"
                    }
                    data-testid="redis-password-input"
                  />
                </div>
              </div>
              <div className={styles.formGroup}>
                <label className={styles.checkboxLabel}>
                  <input
                    type="checkbox"
                    checked={redisForm.tls}
                    onChange={(e) =>
                      setRedisForm((current) => ({
                        ...current,
                        tls: e.target.checked,
                      }))
                    }
                    data-testid="redis-tls-checkbox"
                  />
                  Use TLS
                </label>
              </div>
            </>
          )}
          {redisSettings?.enabled && (
            <p className={styles.description} data-testid="redis-current">
              Current: {redisSettings.addr || "not configured"} | database{" "}
              {redisSettings.db} | {redisSettings.tls ? "TLS" : "no TLS"} |{" "}
              {redisSettings.password_set ? "password saved" : "no password"}
            </p>
          )}
          {localSettingsError && (
            <p className={styles.errorText} data-testid="redis-settings-error">
              {localSettingsError}
            </p>
          )}
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={isSavingLocalSettings}
              onClick={handleRedisSave}
              data-testid="redis-save-button"
            >
              {isSavingLocalSettings ? "Saving..." : "Save Redis Settings"}
            </button>
            <p className={styles.restartNote}>
              Restart the local runtime after changing Redis settings.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
