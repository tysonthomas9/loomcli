import "./styles.css";
import { createQueuedActionState } from "../scripts/frontend-state.mjs";

type AppId = string;

type BrowserApp = {
  id: AppId;
  name: string;
  cdpPort: number;
  epoch: number;
};

const controlOrigin = import.meta.env.VITE_LOOM_KERNEL_CONTROL_ORIGIN || "http://127.0.0.1:61300";
const apps: BrowserApp[] = [
  { id: "app-a", name: "Research", cdpPort: Number(import.meta.env.VITE_LOOM_KERNEL_APP_A_CDP_PORT || 61222), epoch: 0 },
  { id: "app-b", name: "Operations", cdpPort: Number(import.meta.env.VITE_LOOM_KERNEL_APP_B_CDP_PORT || 62222), epoch: 0 },
];

let activeId: AppId = "app-a";
const queuedActions = createQueuedActionState(activeId);
let inputQueue: Promise<unknown> = Promise.resolve();
let pendingMove: { surfaceX: number; surfaceY: number; surfaceWidth: number; surfaceHeight: number } | null = null;
let latestMoveEvent: PointerEvent | null = null;
let moveQueued = false;
let pressedButton = "none";
let frameGeneration = 0;
let frameFailureAnnounced = false;

const root = document.querySelector<HTMLDivElement>("#app");
if (!root) throw new Error("missing app root");

root.innerHTML = `
  <main class="shell">
    <header class="topbar">
      <div>
        <p class="eyebrow">Lead agent · local browser POC</p>
        <h1>Shared browser workspace</h1>
      </div>
      <div class="runtime" role="status"><span class="dot"></span> Task-owned runtime</div>
    </header>
    <nav class="tabs" aria-label="Browser apps">
      <div class="tab-list" id="browser-tabs"></div>
      <button class="add-tab" id="add-browser" type="button">+ New browser</button>
    </nav>
    <section class="workspace">
      <aside class="controls" aria-labelledby="controls-title">
        <div>
          <p class="section-label">Control handoff</p>
          <h2 id="controls-title">Lead + human</h2>
          <p class="helper">Human input is always enabled. A click, wheel, or keypress advances this page's epoch so older queued lead actions are rejected.</p>
        </div>
        <dl class="facts">
          <div><dt>Active app</dt><dd id="active-name">Research</dd></div>
          <div><dt>Control epoch</dt><dd id="epoch">0</dd></div>
          <div><dt>CDP</dt><dd id="cdp">:61222</dd></div>
        </dl>
        <div class="control-ready" role="status"><span class="dot"></span> Human control always available</div>
        <button class="button" id="queue-action">Queue lead action</button>
        <button class="button" id="run-action">Run queued action</button>
        <hr />
        <div>
          <p class="section-label">DOM annotation</p>
          <label for="annotation-note">Note</label>
          <textarea id="annotation-note" rows="3">Check this element before continuing</textarea>
        </div>
        <button class="button" id="arm-annotation">Select in browser</button>
        <button class="button" id="read-annotation">Capture selection</button>
        <div class="notice" id="notice" role="status" aria-live="polite">Runtime ready.</div>
      </aside>
      <section class="browser-panel" aria-label="Live browser">
        <div class="browser-chrome">
          <span class="browser-title" id="browser-title">Research</span>
          <span class="browser-url">CDP frame view · same page as agent control</span>
        </div>
        <div class="browser-viewport">
          <img class="live-view" id="live-view" alt="Research live browser" />
          <div class="human-input active" id="human-input" tabindex="0" aria-label="Human browser control surface"></div>
        </div>
      </section>
    </section>
  </main>
`;

function activeApp(): BrowserApp {
  return apps.find((app) => app.id === activeId)!;
}

function renderTabs() {
  const tabList = document.querySelector<HTMLDivElement>("#browser-tabs")!;
  tabList.replaceChildren(...apps.map((app) => {
    const button = document.createElement("button");
    button.className = "tab";
    button.dataset.app = app.id;
    button.setAttribute("aria-selected", String(app.id === activeId));
    button.textContent = app.name;
    const id = document.createElement("span");
    id.textContent = app.id;
    button.append(id);
    button.addEventListener("click", () => {
      activeId = app.id;
      updateActiveView();
    });
    return button;
  }));
}

function announce(message: string, kind: "normal" | "error" = "normal") {
  const notice = document.querySelector<HTMLDivElement>("#notice")!;
  notice.textContent = message;
  notice.dataset.kind = kind;
}

function updateActiveView() {
  const app = activeApp();
  document.querySelectorAll<HTMLButtonElement>(".tab").forEach((button) => {
    const selected = button.dataset.app === activeId;
    button.setAttribute("aria-selected", String(selected));
  });
  document.querySelector<HTMLImageElement>("#live-view")!.alt = `${app.name} live browser`;
  document.querySelector("#active-name")!.textContent = app.name;
  document.querySelector("#epoch")!.textContent = String(app.epoch);
  document.querySelector("#cdp")!.textContent = `:${app.cdpPort}`;
  document.querySelector("#browser-title")!.textContent = app.name;
  queuedActions.select(app.id);
  restartFrameLoop();
  announce(`${app.name} browser selected.`);
}

async function refreshFrame(generation: number, appId: AppId) {
  try {
    const result = await api(`/api/frame/${appId}`);
    if (generation !== frameGeneration || appId !== activeId) return;
    const frame = document.querySelector<HTMLImageElement>("#live-view")!;
    frame.src = `data:${result.mimeType};base64,${result.image}`;
    frame.dataset.ready = "true";
    applyEpoch(appId, result);
    if (frameFailureAnnounced) {
      frameFailureAnnounced = false;
      announce("Live browser frame restored.");
    }
  } catch (error) {
    if (generation === frameGeneration && !frameFailureAnnounced) {
      frameFailureAnnounced = true;
      announce(`Live frame failed: ${(error as Error).message}`, "error");
    }
  } finally {
    if (generation === frameGeneration) window.setTimeout(() => void refreshFrame(generation, appId), 250);
  }
}

function restartFrameLoop() {
  const generation = ++frameGeneration;
  frameFailureAnnounced = false;
  const frame = document.querySelector<HTMLImageElement>("#live-view")!;
  delete frame.dataset.ready;
  frame.removeAttribute("src");
  void refreshFrame(generation, activeId);
}

function applyEpoch(appId: AppId, result: { epoch?: number }) {
  if (typeof result.epoch !== "number") return;
  const app = apps.find((candidate) => candidate.id === appId)!;
  app.epoch = result.epoch;
  queuedActions.setEpoch(appId, result.epoch);
  if (appId === activeId) document.querySelector("#epoch")!.textContent = String(result.epoch);
}

async function api(path: string, body?: unknown) {
  const response = await fetch(`${controlOrigin}${path}`, {
    method: body === undefined ? "GET" : "POST",
    headers: body === undefined ? undefined : { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const payload = await response.json();
  if (!response.ok) throw Object.assign(new Error(payload.error || `HTTP ${response.status}`), { status: response.status, payload });
  return payload;
}

function surfacePointer(event: PointerEvent | WheelEvent) {
  const rect = document.querySelector<HTMLDivElement>("#human-input")!.getBoundingClientRect();
  return {
    coordinateSpace: "page",
    surfaceX: Math.max(0, Math.min(rect.width, event.clientX - rect.left)),
    surfaceY: Math.max(0, Math.min(rect.height, event.clientY - rect.top)),
    surfaceWidth: rect.width,
    surfaceHeight: rect.height,
  };
}

function mouseButton(button: number) {
  if (button === 0) return "left";
  if (button === 1) return "middle";
  if (button === 2) return "right";
  if (button === 3) return "back";
  if (button === 4) return "forward";
  return "none";
}

function enqueuePointer(payload: object) {
  const appId = activeId;
  inputQueue = inputQueue
    .then(() => api(`/api/input/${appId}/pointer`, payload))
    .then((result) => applyEpoch(appId, result))
    .catch((error) => announce((error as Error).message, "error"));
}

function keyboardModifiers(event: KeyboardEvent) {
  return (event.altKey ? 1 : 0) | (event.ctrlKey ? 2 : 0) | (event.metaKey ? 4 : 0) | (event.shiftKey ? 8 : 0);
}

function pointerModifiers(event: PointerEvent) {
  return (event.altKey ? 1 : 0) | (event.ctrlKey ? 2 : 0) | (event.metaKey ? 4 : 0) | (event.shiftKey ? 8 : 0);
}

function enqueueKey(event: KeyboardEvent) {
  event.preventDefault();
  const type = event.type === "keydown" ? "keyDown" : "keyUp";
  const text = type === "keyDown" && event.key.length === 1 ? event.key : undefined;
  const appId = activeId;
  inputQueue = inputQueue
    .then(() => api(`/api/input/${appId}/key`, {
      type,
      key: event.key,
      code: event.code,
      ...(text === undefined ? {} : { text }),
      modifiers: keyboardModifiers(event),
    }))
    .then((result) => applyEpoch(appId, result))
    .catch((error) => announce((error as Error).message, "error"));
}

function scheduleMove() {
  if (moveQueued || !pendingMove) return;
  moveQueued = true;
  const appId = activeId;
  inputQueue = inputQueue.then(() => {
    const move = pendingMove;
    pendingMove = null;
    if (move && activeId === appId) {
      const event = latestMoveEvent;
      return api(`/api/input/${appId}/pointer`, {
        type: "mouseMoved",
        ...move,
        button: event && event.buttons !== 0 ? pressedButton : "none",
        buttons: event?.buttons ?? 0,
        modifiers: event ? pointerModifiers(event) : 0,
      });
    }
    return undefined;
  }).catch((error) => announce((error as Error).message, "error")).finally(() => {
    moveQueued = false;
    scheduleMove();
  });
}

function queueMove(event: PointerEvent) {
  pendingMove = surfacePointer(event);
  latestMoveEvent = event;
  scheduleMove();
}

document.querySelector<HTMLButtonElement>("#add-browser")!.addEventListener("click", async (event) => {
  const button = event.currentTarget as HTMLButtonElement;
  button.disabled = true;
  button.setAttribute("aria-busy", "true");
  button.textContent = "Starting browser…";
  announce("Starting a new isolated browser. This can take up to a minute.");
  try {
    const result = await api("/api/browsers", {});
    const created = result.browser as BrowserApp;
    if (!apps.some((app) => app.id === created.id)) apps.push(created);
    activeId = created.id;
    renderTabs();
    updateActiveView();
    announce(`${created.name} is ready and selected.`);
  } catch (error) {
    announce(`Could not start browser: ${(error as Error).message}`, "error");
  } finally {
    button.disabled = false;
    button.removeAttribute("aria-busy");
    button.textContent = "+ New browser";
  }
});

const humanInput = document.querySelector<HTMLDivElement>("#human-input")!;
humanInput.addEventListener("pointermove", (event) => queueMove(event));
humanInput.addEventListener("pointerdown", (event) => {
  event.preventDefault();
  humanInput.focus();
  humanInput.setPointerCapture(event.pointerId);
  pressedButton = mouseButton(event.button);
  enqueuePointer({
    type: "mousePressed",
    ...surfacePointer(event),
    button: pressedButton,
    buttons: event.buttons,
    modifiers: pointerModifiers(event),
    clickCount: event.detail || 1,
  });
});
humanInput.addEventListener("pointerup", (event) => {
  event.preventDefault();
  enqueuePointer({
    type: "mouseReleased",
    ...surfacePointer(event),
    button: mouseButton(event.button),
    buttons: event.buttons,
    modifiers: pointerModifiers(event),
    clickCount: event.detail || 1,
  });
  pressedButton = "none";
  if (humanInput.hasPointerCapture(event.pointerId)) humanInput.releasePointerCapture(event.pointerId);
});
humanInput.addEventListener("pointercancel", (event) => {
  enqueuePointer({
    type: "mouseReleased",
    ...surfacePointer(event),
    button: pressedButton,
    buttons: 0,
    modifiers: pointerModifiers(event),
    clickCount: 0,
  });
  pressedButton = "none";
});
humanInput.addEventListener("wheel", (event) => {
  event.preventDefault();
  enqueuePointer({ type: "mouseWheel", ...surfacePointer(event), deltaX: event.deltaX, deltaY: event.deltaY });
}, { passive: false });
humanInput.addEventListener("contextmenu", (event) => event.preventDefault());
humanInput.addEventListener("keydown", enqueueKey);
humanInput.addEventListener("keyup", enqueueKey);

document.querySelector<HTMLButtonElement>("#queue-action")!.addEventListener("click", () => {
  const app = activeApp();
  const queued = queuedActions.queue(app.id, app.epoch);
  announce(`Lead action queued for ${app.name} at epoch ${queued.epoch}. Take human control before running it to prove preemption.`);
});

document.querySelector<HTMLButtonElement>("#run-action")!.addEventListener("click", async () => {
  try {
    const queued = queuedActions.current();
    if (!queued) throw new Error("Queue a lead action before running it.");
    const app = apps.find((candidate) => candidate.id === queued.appId)!;
    await api(`/api/action/${queued.appId}`, { expectedEpoch: queued.epoch });
    announce(`Lead action ran in ${app.name} at epoch ${queued.epoch}.`);
  } catch (error) {
    const status = (error as { status?: number }).status;
    announce(status === 409 ? "Stale lead action rejected after human control changed." : (error as Error).message, status === 409 ? "normal" : "error");
  }
});

document.querySelector<HTMLButtonElement>("#arm-annotation")!.addEventListener("click", async () => {
  try {
    const app = activeApp();
    const note = document.querySelector<HTMLTextAreaElement>("#annotation-note")!.value;
    await api(`/api/annotation/${app.id}/arm`, { note });
    announce("Annotation mode armed. Click an element inside the live browser, then capture selection.");
  } catch (error) {
    announce((error as Error).message, "error");
  }
});

document.querySelector<HTMLButtonElement>("#read-annotation")!.addEventListener("click", async () => {
  try {
    const app = activeApp();
    const result = await api(`/api/annotation/${app.id}/capture`, {});
    announce(`Captured ${result.annotation.selector} with DOM bounds and cropped screenshot.`);
  } catch (error) {
    announce((error as Error).message, "error");
  }
});

renderTabs();
updateActiveView();
