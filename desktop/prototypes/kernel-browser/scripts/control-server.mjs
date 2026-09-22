import { createServer } from "node:http";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { keyParams, pointerParams } from "./input-bridge.mjs";
import { chooseVisiblePage, createPendingCommands } from "./cdp-client.mjs";
import { annotationGeometry } from "./annotation-geometry.mjs";
import { allowedOrigins, originAllowed } from "./request-policy.mjs";

const host = process.env.LOOM_KERNEL_CONTROL_HOST || "127.0.0.1";
const port = Number(process.env.LOOM_KERNEL_CONTROL_PORT || 61300);
const cdpTimeoutMs = Number(process.env.LOOM_KERNEL_CDP_TIMEOUT_MS || 5_000);
const prototypeDir = dirname(dirname(fileURLToPath(import.meta.url)));
const artifactDir = join(prototypeDir, "evidence", "artifacts");
const browsers = {
  "app-a": { cdpPort: Number(process.env.LOOM_KERNEL_APP_A_CDP_PORT || 61222), epoch: 0 },
  "app-b": { cdpPort: Number(process.env.LOOM_KERNEL_APP_B_CDP_PORT || 62222), epoch: 0 },
};
const embedEvents = [];

function browser(appId) {
  const item = browsers[appId];
  if (!item) throw Object.assign(new Error(`unknown browser app: ${appId}`), { status: 404 });
  return item;
}

async function body(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  if (chunks.length === 0) return {};
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

async function browserSocket(cdpPort) {
  const response = await fetch(`http://127.0.0.1:${cdpPort}/json/version`, { signal: AbortSignal.timeout(cdpTimeoutMs) });
  if (!response.ok) throw new Error(`CDP version failed: ${response.status}`);
  const version = await response.json();
  if (!version.webSocketDebuggerUrl) throw new Error("no CDP browser socket is ready");
  return version.webSocketDebuggerUrl;
}

async function cdp(cdpPort, operations) {
  const socket = new WebSocket(await browserSocket(cdpPort));
  const waiting = createPendingCommands();
  let nextId = 1;

  socket.addEventListener("message", (event) => {
    const message = JSON.parse(String(event.data));
    waiting.settle(message.id, message.error ? new Error(message.error.message) : null, message.result);
  });

  const rejectDisconnected = () => waiting.rejectAll(new Error("CDP websocket disconnected"));
  socket.addEventListener("close", rejectDisconnected);
  socket.addEventListener("error", rejectDisconnected);

  try {
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("CDP websocket open timed out")), cdpTimeoutMs);
      socket.addEventListener("open", () => { clearTimeout(timer); resolve(); }, { once: true });
      socket.addEventListener("error", () => { clearTimeout(timer); reject(new Error("CDP websocket failed")); }, { once: true });
    });
  } catch (error) {
    socket.close();
    throw error;
  }

  const sendRaw = (method, params = {}, sessionId) => {
    const id = nextId++;
    const result = waiting.add(id, cdpTimeoutMs);
    try {
      socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    } catch (error) {
      waiting.settle(id, error);
    }
    return result;
  };

  try {
    const { targetInfos } = await sendRaw("Target.getTargets");
    const sessions = new Map();
    const target = await chooseVisiblePage(targetInfos, async (candidate) => {
      const attached = await sendRaw("Target.attachToTarget", { targetId: candidate.targetId, flatten: true });
      sessions.set(candidate.targetId, attached.sessionId);
      const visibility = await sendRaw("Runtime.evaluate", { expression: "document.visibilityState", returnByValue: true }, attached.sessionId);
      return visibility.result.value === "visible";
    });
    if (!target) throw new Error("no CDP page target is ready");
    for (const [targetId, sessionId] of sessions) {
      if (targetId !== target.targetId) await sendRaw("Target.detachFromTarget", { sessionId });
    }
    const sessionId = sessions.get(target.targetId)
      || (await sendRaw("Target.attachToTarget", { targetId: target.targetId, flatten: true })).sessionId;
    const send = (method, params = {}) => sendRaw(method, params, sessionId);
    return await operations(send);
  } finally {
    waiting.rejectAll(new Error("CDP connection closed"));
    socket.close();
  }
}

const annotationScript = (note) => `(() => {
  const previous = window.__loomAnnotationCleanup;
  if (typeof previous === 'function') previous();
  window.__loomAnnotation = null;
  window.__loomAnnotationElement = null;
  const handler = (event) => {
    const element = event.target;
    if (!(element instanceof Element)) return;
    event.preventDefault();
    event.stopPropagation();
    const rect = element.getBoundingClientRect();
    const oldOutline = element.style.outline;
    const oldOffset = element.style.outlineOffset;
    element.style.outline = '3px solid #ff7a00';
    element.style.outlineOffset = '2px';
    const selector = element.id
      ? '#' + CSS.escape(element.id)
      : element.tagName.toLowerCase() + Array.from(element.classList).slice(0, 2).map((name) => '.' + CSS.escape(name)).join('');
    window.__loomAnnotation = {
      selector,
      tag: element.tagName.toLowerCase(),
      id: element.id || null,
      classes: Array.from(element.classList),
      role: element.getAttribute('role'),
      ariaLabel: element.getAttribute('aria-label'),
      text: (element.innerText || element.textContent || '').trim().slice(0, 240),
      url: location.href,
      title: document.title,
      note: ${JSON.stringify(note)},
      bounds: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
      viewport: { width: innerWidth, height: innerHeight, deviceScaleFactor: devicePixelRatio },
      capturedAt: new Date().toISOString()
    };
    window.__loomAnnotationElement = element;
    document.removeEventListener('click', handler, true);
    window.__loomAnnotationCleanup = () => {
      element.style.outline = oldOutline;
      element.style.outlineOffset = oldOffset;
      document.removeEventListener('click', handler, true);
    };
  };
  document.addEventListener('click', handler, true);
  window.__loomAnnotationCleanup = () => document.removeEventListener('click', handler, true);
  return { armed: true, url: location.href };
})()`;

async function handleApi(req, url) {
  const parts = url.pathname.split("/").filter(Boolean);

  if (parts[1] === "embed-events" && req.method === "GET") {
    return { events: embedEvents };
  }

  const appId = parts[2];
  const selected = browser(appId);

  if (parts[1] === "embed-event" && req.method === "POST") {
    const payload = await body(req);
    const type = String(payload.type || "");
    if (!["KERNEL_CONNECTED", "KERNEL_PLAYING", "KERNEL_READ_ONLY_CHANGED"].includes(type)) {
      throw Object.assign(new Error("unsupported embed event"), { status: 400 });
    }
    embedEvents.push({
      appId,
      type,
      readOnly: typeof payload.readOnly === "boolean" ? payload.readOnly : null,
      userAgent: String(payload.userAgent || "").slice(0, 300),
      href: String(payload.href || "").slice(0, 500),
      at: String(payload.at || new Date().toISOString()),
    });
    if (embedEvents.length > 100) embedEvents.splice(0, embedEvents.length - 100);
    return { accepted: true };
  }

  if (parts[1] === "epoch" && parts[3] === "bump" && req.method === "POST") {
    selected.epoch += 1;
    return { epoch: selected.epoch };
  }

  if (parts[1] === "input" && parts[3] === "pointer" && req.method === "POST") {
    const payload = await body(req);
    const pagePoint = await cdp(selected.cdpPort, async (send) => {
      const geometryResult = await send("Runtime.evaluate", {
        expression: "({ screen: { width: screen.width, height: screen.height }, window: { outerWidth, outerHeight, innerWidth, innerHeight, screenX, screenY } })",
        returnByValue: true,
      });
      const params = pointerParams(payload, geometryResult.result.value);
      if (!params) return null;
      await send("Input.dispatchMouseEvent", params);
      return { x: params.x, y: params.y };
    });
    return { accepted: pagePoint !== null, pagePoint };
  }

  if (parts[1] === "input" && parts[3] === "key" && req.method === "POST") {
    const payload = await body(req);
    await cdp(selected.cdpPort, (send) => send("Input.dispatchKeyEvent", keyParams(payload)));
    return { accepted: true };
  }

  if (parts[1] === "action" && req.method === "POST") {
    const payload = await body(req);
    if (payload.expectedEpoch !== selected.epoch) {
      throw Object.assign(new Error(`stale epoch ${payload.expectedEpoch}; current epoch is ${selected.epoch}`), { status: 409 });
    }
    const result = await cdp(selected.cdpPort, (send) => {
      if (payload.expectedEpoch !== selected.epoch) {
        throw Object.assign(new Error(`stale epoch ${payload.expectedEpoch}; current epoch is ${selected.epoch}`), { status: 409 });
      }
      const dispatchEpoch = selected.epoch;
      return send("Runtime.evaluate", {
      expression: `(() => {
        let banner = document.querySelector('[data-loom-lead-action]');
        if (!banner) {
          banner = document.createElement('div');
          banner.dataset.loomLeadAction = 'true';
          Object.assign(banner.style, { position: 'fixed', zIndex: '2147483647', top: '12px', right: '12px', padding: '10px 14px', background: '#173f33', color: 'white', font: '600 14px system-ui', borderRadius: '4px' });
          document.body.appendChild(banner);
        }
        banner.textContent = 'Lead action accepted · epoch ${dispatchEpoch}';
        return { title: document.title, url: location.href };
      })()`,
      returnByValue: true,
      });
    });
    return { epoch: payload.expectedEpoch, page: result.result.value };
  }

  if (parts[1] === "annotation" && parts[3] === "arm" && req.method === "POST") {
    const payload = await body(req);
    const result = await cdp(selected.cdpPort, (send) => send("Runtime.evaluate", {
      expression: annotationScript(String(payload.note || "")),
      returnByValue: true,
    }));
    return result.result.value;
  }

  if (parts[1] === "annotation" && parts[3] === "capture" && req.method === "POST") {
    const result = await cdp(selected.cdpPort, async (send) => {
      const evaluation = await send("Runtime.evaluate", {
        expression: `(() => {
          const annotation = window.__loomAnnotation;
          const element = window.__loomAnnotationElement;
          if (!annotation || !(element instanceof Element) || !element.isConnected) return null;
          const rect = element.getBoundingClientRect();
          const pageLeft = window.visualViewport?.pageLeft ?? window.scrollX;
          const pageTop = window.visualViewport?.pageTop ?? window.scrollY;
          const geometry = (${annotationGeometry.toString()})(rect, { pageLeft, pageTop });
          annotation.bounds = geometry.bounds;
          annotation.documentBounds = geometry.documentBounds;
          annotation.viewport = { width: innerWidth, height: innerHeight, deviceScaleFactor: devicePixelRatio };
          annotation.capturedAt = new Date().toISOString();
          return annotation;
        })()`,
        returnByValue: true,
      });
      const annotation = evaluation.result.value;
      if (!annotation) throw Object.assign(new Error("selected element is no longer available; select it again"), { status: 409 });
      const { x, y, width, height } = annotation.documentBounds;
      const screenshot = await send("Page.captureScreenshot", {
        format: "png",
        captureBeyondViewport: true,
        clip: { x: Math.max(0, x), y: Math.max(0, y), width: Math.max(1, width), height: Math.max(1, height), scale: 1 },
      });
      return { annotation, screenshot: screenshot.data };
    });
    await mkdir(artifactDir, { recursive: true });
    const stem = `${appId}-${Date.now()}`;
    const screenshotPath = join(artifactDir, `${stem}.png`);
    const metadataPath = join(artifactDir, `${stem}.json`);
    await writeFile(screenshotPath, Buffer.from(result.screenshot, "base64"));
    await writeFile(metadataPath, `${JSON.stringify(result.annotation, null, 2)}\n`);
    return { annotation: result.annotation, screenshotPath, metadataPath };
  }

  if (parts[1] === "navigate" && req.method === "POST") {
    const payload = await body(req);
    const destination = String(payload.url || "");
    if (!destination.startsWith("http://host.lima.internal:61300/fixture")) {
      throw Object.assign(new Error("prototype navigation only permits its task-owned host fixture"), { status: 400 });
    }
    await cdp(selected.cdpPort, (send) => send("Page.navigate", { url: destination }));
    return { url: destination };
  }

  if (parts[1] === "status") {
    const version = await fetch(`http://127.0.0.1:${selected.cdpPort}/json/version`).then((response) => response.json());
    return { appId, epoch: selected.epoch, cdpPort: selected.cdpPort, browser: version.Browser };
  }

  throw Object.assign(new Error("unknown API route"), { status: 404 });
}

function fixture() {
  return `<!doctype html>
  <html lang="en"><head><meta charset="utf-8"><title>Loom host reachability fixture</title>
  <style>body{font:18px system-ui;margin:0;background:#f0f2ed;color:#17201d}main{max-width:760px;margin:8vh auto;padding:40px;background:white;border:1px solid #bbc3bc}h1{font-size:34px;margin-top:0}button{font:inherit;padding:12px 18px;background:#256f55;color:white;border:0;border-radius:4px}.target{margin-top:28px;padding:24px;border:2px dashed #819087}</style>
  </head><body><main><p>Task-owned host fixture</p><h1>One browser, shared by lead and human</h1><button id="confirm-target" aria-label="Confirm selected workflow" onclick="document.querySelector('#action-status').textContent='Workflow confirmed by lead agent'">Confirm workflow</button><p id="action-status" role="status">Waiting for lead action</p><section class="target" role="region" aria-label="Annotation target"><strong>Annotation target</strong><p>Select this panel from the live view. Loom will resolve the click to DOM context and capture its bounds.</p></section></main></body></html>`;
}

const server = createServer(async (req, res) => {
  const origin = req.headers.origin;
  if (!originAllowed(origin)) {
    res.writeHead(403, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "origin is not allowed" }));
    return;
  }
  if (origin && allowedOrigins.has(origin)) {
    res.setHeader("access-control-allow-origin", origin);
    res.setHeader("vary", "origin");
  }
  res.setHeader("access-control-allow-headers", "content-type");
  res.setHeader("access-control-allow-methods", "GET, POST, OPTIONS");
  if (req.method === "OPTIONS") { res.writeHead(204); res.end(); return; }
  try {
    const url = new URL(req.url, `http://${req.headers.host}`);
    if (url.pathname === "/fixture") {
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(fixture());
      return;
    }
    if (url.pathname === "/healthz") {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ ok: true, browsers }));
      return;
    }
    if (url.pathname.startsWith("/api/")) {
      const payload = await handleApi(req, url);
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify(payload));
      return;
    }
    res.writeHead(404, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "not found" }));
  } catch (error) {
    const status = error.status || 500;
    res.writeHead(status, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: error.message }));
  }
});

server.listen(port, host, () => {
  console.log(`loom kernel control listening on http://${host}:${port}`);
});
