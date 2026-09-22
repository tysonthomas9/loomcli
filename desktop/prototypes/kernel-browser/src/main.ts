import "./styles.css";

type AppId = "app-a" | "app-b";

type BrowserApp = {
  id: AppId;
  name: string;
  liveUrl: string;
  cdpPort: number;
  epoch: number;
};

const controlOrigin = "http://127.0.0.1:61300";
const apps: BrowserApp[] = [
  { id: "app-a", name: "Research", liveUrl: "http://127.0.0.1:61080/?embed=1&readOnly=true", cdpPort: 61222, epoch: 0 },
  { id: "app-b", name: "Operations", liveUrl: "http://127.0.0.1:62080/?embed=1&readOnly=true", cdpPort: 62222, epoch: 0 },
];

let activeId: AppId = "app-a";
let queuedEpoch = 0;

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
      ${apps.map((app) => `<button class="tab" data-app="${app.id}" aria-selected="${app.id === activeId}">${app.name}<span>${app.id}</span></button>`).join("")}
    </nav>
    <section class="workspace">
      <aside class="controls" aria-labelledby="controls-title">
        <div>
          <p class="section-label">Control handoff</p>
          <h2 id="controls-title">Lead + human</h2>
          <p class="helper">Taking control increments this page's epoch. Any queued lead action from an older epoch is rejected.</p>
        </div>
        <dl class="facts">
          <div><dt>Active app</dt><dd id="active-name">Research</dd></div>
          <div><dt>Control epoch</dt><dd id="epoch">0</dd></div>
          <div><dt>CDP</dt><dd id="cdp">:61222</dd></div>
        </dl>
        <button class="button primary" id="take-control">Take human control</button>
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
          <span class="browser-url">Kernel live view · same page as CDP</span>
        </div>
        ${apps.map((app) => `<iframe class="live-view${app.id === activeId ? " active" : ""}" data-frame="${app.id}" title="${app.name} live browser"${app.id === activeId ? ` src="${app.liveUrl}"` : ""} referrerpolicy="strict-origin-when-cross-origin" allow="clipboard-read; clipboard-write; autoplay"></iframe>`).join("")}
      </section>
    </section>
  </main>
`;

function activeApp(): BrowserApp {
  return apps.find((app) => app.id === activeId)!;
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
  document.querySelectorAll<HTMLIFrameElement>(".live-view").forEach((frame) => {
    const selected = frame.dataset.frame === activeId;
    frame.classList.toggle("active", selected);
    if (selected && (!frame.src || frame.src === "about:blank")) frame.src = app.liveUrl;
    if (!selected && frame.src && frame.src !== "about:blank") frame.src = "about:blank";
  });
  document.querySelector("#active-name")!.textContent = app.name;
  document.querySelector("#epoch")!.textContent = String(app.epoch);
  document.querySelector("#cdp")!.textContent = `:${app.cdpPort}`;
  document.querySelector("#browser-title")!.textContent = app.name;
  queuedEpoch = app.epoch;
  announce(`${app.name} browser selected.`);
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

document.querySelectorAll<HTMLButtonElement>(".tab").forEach((button) => {
  button.addEventListener("click", () => {
    activeId = button.dataset.app as AppId;
    updateActiveView();
  });
});

window.addEventListener("message", (event) => {
  const app = apps.find((candidate) => {
    const frame = document.querySelector<HTMLIFrameElement>(`[data-frame="${candidate.id}"]`);
    return frame?.contentWindow === event.source && event.origin === new URL(candidate.liveUrl).origin;
  });
  if (!app || typeof event.data !== "object" || event.data === null) return;
  const type = String((event.data as { type?: unknown }).type || "");
  if (!["KERNEL_CONNECTED", "KERNEL_PLAYING", "KERNEL_READ_ONLY_CHANGED"].includes(type)) return;
  void api(`/api/embed-event/${app.id}`, {
    type,
    readOnly: (event.data as { readOnly?: unknown }).readOnly,
    userAgent: navigator.userAgent,
    href: location.href,
    at: new Date().toISOString(),
  }).catch(() => undefined);
});

document.querySelector<HTMLButtonElement>("#take-control")!.addEventListener("click", async () => {
  try {
    const app = activeApp();
    const result = await api(`/api/epoch/${app.id}/bump`, {});
    app.epoch = result.epoch;
    document.querySelector("#epoch")!.textContent = String(app.epoch);
    const frame = document.querySelector<HTMLIFrameElement>(`[data-frame="${app.id}"]`)!;
    frame.contentWindow?.postMessage({ type: "KERNEL_SET_READ_ONLY", readOnly: false, requestId: crypto.randomUUID() }, new URL(app.liveUrl).origin);
    announce(`Human control active. Epoch is now ${app.epoch}; older lead actions are stale.`);
  } catch (error) {
    announce((error as Error).message, "error");
  }
});

document.querySelector<HTMLButtonElement>("#queue-action")!.addEventListener("click", () => {
  queuedEpoch = activeApp().epoch;
  announce(`Lead action queued at epoch ${queuedEpoch}. Take human control before running it to prove preemption.`);
});

document.querySelector<HTMLButtonElement>("#run-action")!.addEventListener("click", async () => {
  try {
    const app = activeApp();
    await api(`/api/action/${app.id}`, { expectedEpoch: queuedEpoch });
    announce(`Lead action ran in ${app.name} at epoch ${queuedEpoch}.`);
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

updateActiveView();
