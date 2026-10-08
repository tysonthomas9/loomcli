(() => {
  const workspace = __WS_JSON__;
  const run = __RUN_JSON__;
  const expectedPath = __PATH_JSON__;
  const chatPrefix = __CHAT_PREFIX_JSON__;
  const chatRoute = agentId => chatPrefix.endsWith("/") ? chatPrefix + agentId : chatPrefix;
  if (location.pathname !== expectedPath || Object.hasOwn(window, "__aftChatVisualArrival"))
    throw Error("arrival-prime-route-or-duplicate");
  const NativeEventSource = window.EventSource;
  const eventPath = `/api/workspaces/${encodeURIComponent(workspace)}/v1/events`;
  const sources = [];
  const arrivals = [];
  const completions = [];
  let bound = null;
  let closed = false;
  let error = null;
  let phase = 0;
  const mark = () => ({ mono: performance.now(), at: Date.now(), phase: ++phase });
  class ObservedEventSource extends NativeEventSource {
    constructor(url, options) {
      super(url, options);
      const target = new URL(String(url), location.href);
      if (target.origin !== location.origin || target.pathname !== eventPath ||
          target.searchParams.get("deltas") !== "true") return;
      const agents = (target.searchParams.get("agents") || "").split(",");
      const source = { id: sources.length + 1, ...mark(), agents };
      if (sources.length >= 20) { error = "too-many-event-sources"; return; }
      sources.push(source);
      this.addEventListener("event", event => {
        if (!bound || closed || error || !source.agents.includes(bound.agentId)) return;
        let body;
        try { body = JSON.parse(event.data); }
        catch { error = "invalid-event-json"; return; }
        if (body.agent_id !== bound.agentId) return;
        if ((body.kind === "item.completed" && body.payload?.itemKind === "message") ||
            body.kind === "agent.turn_completed") {
          if (completions.length >= 100) { error = "too-many-completions"; return; }
          completions.push({ ...mark(), sourceId: source.id, agentId: body.agent_id,
            turnId: body.turn_id, kind: body.kind, seq: body.seq,
            eventId: body.event_id, itemId: body.payload?.itemId ?? null });
          return;
        }
        if (body.seq !== 0 || body.kind !== "delta" ||
            body.payload?.itemKind !== "message") return;
        const itemId = body.payload.itemId;
        const text = body.payload.text;
        if (typeof itemId !== "string" || !itemId || typeof text !== "string" ||
            typeof body.turn_id !== "string" || !body.turn_id) {
          error = "invalid-message-delta";
          return;
        }
        if (arrivals.length >= 5000) { error = "too-many-message-deltas"; return; }
        arrivals.push({ ...mark(), sourceId: source.id, agentId: body.agent_id,
          turnId: body.turn_id, itemId, text });
      });
    }
  }
  window.EventSource = ObservedEventSource;
  const observer = Object.freeze({
    version: 1, workspace, run, installedPath: expectedPath,
    constructor: ObservedEventSource,
    ready(agentId) {
      return !closed && !error && window.EventSource === ObservedEventSource &&
        sources.some(source => source.agents.length === 1 && source.agents[0] === agentId);
    },
    bind(agentId, route) {
      if (closed || bound || error || !/^[A-Za-z0-9_-]+$/.test(agentId) ||
          route !== chatRoute(agentId) ||
          location.pathname !== route ||
          window.EventSource !== ObservedEventSource ||
          !sources.some(source => source.agents.length === 1 && source.agents[0] === agentId))
        throw Error("arrival-bind-identity-or-source");
      bound = { agentId, route, ...mark() };
      return { version: 1, workspace, run, agentId, route, sourceCount: sources.length };
    },
    snapshot() {
      if (!bound || closed || location.pathname !== bound.route ||
          window.EventSource !== ObservedEventSource)
        throw Error("arrival-snapshot-identity");
      return { version: 1, workspace, run, bound: { ...bound },
        sources: sources.map(source => ({ ...source, agents: [...source.agents] })),
        arrivals: arrivals.map(arrival => ({ ...arrival })),
        completions: completions.map(completion => ({ ...completion })),
        phase, error };
    },
    markFrame() {
      if (closed || !bound || location.pathname !== bound.route ||
          window.EventSource !== ObservedEventSource)
        throw Error("arrival-frame-identity");
      return mark();
    },
    close() {
      if (closed || window.EventSource !== ObservedEventSource) throw Error("arrival-close-identity");
      closed = true;
      window.EventSource = NativeEventSource;
      return { closed: true, arrivalCount: arrivals.length,
        completionCount: completions.length, phase, error };
    },
  });
  Object.defineProperty(window, "__aftChatVisualArrival", {
    value: observer, enumerable: false, writable: false, configurable: false,
  });
  return { version: 1, workspace, run, path: expectedPath };
})()
