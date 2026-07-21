import { invoke } from "@tauri-apps/api/core";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { Command } from "@tauri-apps/plugin-shell";
import "./styles.css";

type RuntimeInfo = {
  status?: string;
  pid?: number;
  serve_pid?: number;
  data_dir?: string;
  url?: string;
  port?: number;
  claims_paused?: boolean;
  started_at?: string;
  updated_at?: string;
  error?: string;
};

type RuntimeStatus = {
  runtime?: RuntimeInfo;
  healthy: boolean;
  error?: string;
};

type WorkspaceRecovery = {
  route: string;
};

type StageMode = "starting" | "ready" | "error";

const SIDECAR = "binaries/loom";
const RUNTIME_TIMEOUT_MS = 45_000;
const RUNTIME_POLL_MS = 500;
const OPEN_ADDITIONAL_WORKSPACE_WINDOW = Boolean(
  (window as unknown as Record<string, unknown>)
    .__LOOM_OPEN_ADDITIONAL_WORKSPACE_WINDOW__,
);

const app = document.querySelector<HTMLDivElement>("#app");
if (!app) {
  throw new Error("missing #app");
}

app.innerHTML = `
  <main class="launcher" aria-live="polite">
    <section class="status">
      <div id="spinner" class="spinner" aria-hidden="true"></div>
      <div class="statusText">
        <h1 id="stageTitle">Starting Loom</h1>
        <p id="stageDetail">Preparing the local workspace runtime.</p>
      </div>
    </section>

    <section id="actions" class="actions" hidden>
      <button id="retryBtn" type="button">Retry</button>
      <button id="openWorkspaceBtn" type="button" disabled>Open Workspace</button>
    </section>

    <details id="details" class="details">
      <summary>Runtime details</summary>
      <dl>
        <div>
          <dt>Status</dt>
          <dd id="runtimeStatus">-</dd>
        </div>
        <div>
          <dt>URL</dt>
          <dd id="runtimeUrl">-</dd>
        </div>
        <div>
          <dt>Service PID</dt>
          <dd id="runtimePid">-</dd>
        </div>
        <div>
          <dt>Serve PID</dt>
          <dd id="servePid">-</dd>
        </div>
        <div>
          <dt>Data</dt>
          <dd id="dataDir">-</dd>
        </div>
      </dl>
      <pre id="output" class="output"></pre>
    </details>
  </main>
`;

const stageTitle = document.querySelector<HTMLHeadingElement>("#stageTitle")!;
const stageDetail =
  document.querySelector<HTMLParagraphElement>("#stageDetail")!;
const actions = document.querySelector<HTMLElement>("#actions")!;
const details = document.querySelector<HTMLDetailsElement>("#details")!;
const output = document.querySelector<HTMLPreElement>("#output")!;
const openWorkspaceBtn =
  document.querySelector<HTMLButtonElement>("#openWorkspaceBtn")!;
const retryBtn = document.querySelector<HTMLButtonElement>("#retryBtn")!;

let bootInFlight = false;
let startupRelocationChecked = false;
let lastRuntimeStatus: RuntimeStatus | null = null;
let lastRuntimeUrl = "";
let pendingRecovery: WorkspaceRecovery | null = null;

function setStage(mode: StageMode, title: string, detail: string) {
  document.body.dataset.mode = mode;
  stageTitle.textContent = title;
  stageDetail.textContent = detail;
}

function setText(id: string, value: string | number | boolean | undefined) {
  const el = document.querySelector<HTMLElement>(`#${id}`);
  if (!el) return;
  el.textContent = value === undefined || value === "" ? "-" : String(value);
}

function renderRuntime(status: RuntimeStatus | null, log = "") {
  const runtime = status?.runtime;
  lastRuntimeStatus = status;
  lastRuntimeUrl = runtime?.url || lastRuntimeUrl;
  openWorkspaceBtn.disabled = !lastRuntimeUrl;

  setText("runtimeStatus", status?.healthy ? "running" : runtime?.status);
  setText("runtimeUrl", runtime?.url);
  setText("runtimePid", runtime?.pid);
  setText("servePid", runtime?.serve_pid);
  setText("dataDir", runtime?.data_dir);

  const payload = status ? JSON.stringify(status, null, 2) : "";
  output.textContent = [payload, log.trim()].filter(Boolean).join("\n\n");
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : String(err);
}

function delay(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

async function runLoom(args: string[]) {
  return Command.sidecar(SIDECAR, args).execute();
}

async function readRuntimeStatus() {
  const result = await runLoom(["local", "status", "--json"]);
  if (result.code !== 0) {
    throw new Error(
      result.stderr || result.stdout || `loom exited ${result.code}`,
    );
  }
  const status = JSON.parse(result.stdout) as RuntimeStatus;
  status.healthy = Boolean(status.healthy);
  return status;
}

async function ensureRuntime() {
  setStage("starting", "Starting Loom", "Checking the local runtime.");
  details.open = false;

  const initial = await readRuntimeStatus().catch((err: unknown) => {
    const status: RuntimeStatus = { healthy: false, error: errorMessage(err) };
    renderRuntime(status);
    return status;
  });
  renderRuntime(initial);

  setStage(
    "starting",
    "Starting Loom",
    "Ensuring the local runtime matches this app.",
  );
  const start = await runLoom(["local", "start"]);
  renderRuntime(initial, `${start.stdout}\n${start.stderr}`);
  if (start.code !== 0) {
    throw new Error(
      start.stderr || start.stdout || `loom exited ${start.code}`,
    );
  }

  return waitForHealthyRuntime();
}

async function waitForHealthyRuntime() {
  const startedAt = Date.now();
  let lastError = "";

  while (Date.now() - startedAt < RUNTIME_TIMEOUT_MS) {
    setStage("starting", "Starting Loom", "Waiting for the workspace runtime.");
    try {
      const status = await readRuntimeStatus();
      renderRuntime(status);
      if (status.healthy && status.runtime?.url) {
        return status;
      }
      lastError = status.error || status.runtime?.error || "";
    } catch (err) {
      lastError = errorMessage(err);
      renderRuntime({ healthy: false, error: lastError });
    }
    await delay(RUNTIME_POLL_MS);
  }

  throw new Error(lastError || "local runtime did not become healthy");
}

function workspaceEntryUrl(runtimeUrl: string, route = "/") {
  const base = runtimeUrl.replace(/\/+$/, "");
  if (!route || route === "/") {
    return `${base}/`;
  }
  return route.startsWith("/") ? `${base}${route}` : `${base}/${route}`;
}

function localWorkspaceEntry(
  runtimeUrl: string,
  route = "/",
) {
  const entry = new URL(workspaceEntryUrl(runtimeUrl, route));
  if (entry.origin !== new URL(runtimeUrl).origin) {
    throw new Error("The local runtime changed while opening the workspace.");
  }
  return entry.toString();
}

async function openWorkspaceWindow(
  runtimeUrl: string,
  options: { forceNew?: boolean; recovery?: WorkspaceRecovery } = {},
) {
  setStage("starting", "Opening Workspace", "Loading the workspace window.");
  const entry = localWorkspaceEntry(runtimeUrl, options.recovery?.route);
  await invoke("open_workspace_window", {
    runtimeUrl: entry,
    forceNew: Boolean(options.forceNew),
  });
}

async function readPendingRecovery() {
  if (pendingRecovery) {
    return pendingRecovery;
  }
  const recovery = await invoke<string | null>("take_workspace_recovery");
  if (recovery) {
    pendingRecovery = { route: recovery };
  }
  return pendingRecovery;
}

async function focusExistingWorkspaceWindow() {
  if (lastRuntimeUrl) {
    await openWorkspaceWindow(lastRuntimeUrl, {
      forceNew: OPEN_ADDITIONAL_WORKSPACE_WINDOW,
    });
    return;
  }
  await boot();
}

function showFailure(err: unknown) {
  const message = errorMessage(err);
  setStage("error", "Could Not Open Workspace", message);
  actions.hidden = false;
  details.open = true;
  renderRuntime(lastRuntimeStatus, message);
}

// macOS runs a quarantined app from a read-only, randomized location (App
// Translocation) when it is opened from a disk image or a download instead of
// from /Applications. The bundled loom sidecar cannot start from there, so show
// actionable guidance rather than a confusing "Could Not Open Workspace" error.
async function needsRelocation() {
  if (
    Boolean(
      (window as unknown as Record<string, unknown>).__LOOM_NEEDS_RELOCATION__,
    )
  ) {
    return true;
  }
  return Boolean(await invoke<boolean>("needs_relocation"));
}

function showRelocationNotice() {
  lastRuntimeUrl = "";
  openWorkspaceBtn.disabled = true;
  setStage(
    "error",
    "Move Loom to Applications",
    'Loom can\'t run from a disk image or your Downloads folder. Drag "Loom Agents" into your Applications folder, then open it from there.',
  );
  actions.hidden = true;
  details.open = false;
}

async function boot(
  options: { forceNew?: boolean } = {
    forceNew: OPEN_ADDITIONAL_WORKSPACE_WINDOW,
  },
) {
  if (bootInFlight) return;
  bootInFlight = true;

  try {
    if (await needsRelocation()) {
      showRelocationNotice();
      return;
    }

    actions.hidden = true;
    openWorkspaceBtn.disabled = !lastRuntimeUrl;
    // Fresh additional launchers have no pending entry; recovered additional
    // windows do. Always ask native state so every window retains its binding.
    const recovery = await readPendingRecovery();

    const status = await ensureRuntime();
    const runtimeUrl = status.runtime?.url;
    if (!runtimeUrl) {
      throw new Error("local runtime URL is missing");
    }
    setStage("ready", "Opening Workspace", "Loom is ready.");
    await openWorkspaceWindow(runtimeUrl, {
      ...options,
      recovery: recovery ?? undefined,
    });
    if (!options.forceNew) {
      pendingRecovery = null;
    }
  } catch (err) {
    showFailure(err);
  } finally {
    bootInFlight = false;
  }
}

retryBtn.addEventListener("click", () => {
  void boot();
});

openWorkspaceBtn.addEventListener("click", () => {
  if (!lastRuntimeUrl) return;
  void openWorkspaceWindow(lastRuntimeUrl, {
    forceNew: OPEN_ADDITIONAL_WORKSPACE_WINDOW,
  }).catch(showFailure);
});

void getCurrentWindow().onFocusChanged(({ payload }) => {
  if (
    payload &&
    startupRelocationChecked &&
    !bootInFlight &&
    document.body.dataset.mode !== "error"
  ) {
    void focusExistingWorkspaceWindow().catch(showFailure);
  }
});

async function startLauncher() {
  startupRelocationChecked = true;
  await boot({ forceNew: OPEN_ADDITIONAL_WORKSPACE_WINDOW });
}

void startLauncher().catch(showFailure);
