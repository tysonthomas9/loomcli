// Passive request metadata only; never records tokens, bodies or auth headers.
(() => {
 const original = window.fetch;
 window.__sseBrowserProof = [];
 window.fetch = async function(input, init) {
  const url = new URL(typeof input === 'string' ? input : input.url || String(input), location.href);
  const relevant = /^\/api\/workspaces\/[^/]+\/(events|issues)/.test(url.pathname);
  const entry = relevant ? { path: url.pathname, method: init?.method || (input instanceof Request ? input.method : 'GET'), since: url.searchParams.get('since'), lastEventId: new Headers(init?.headers || (input instanceof Request ? input.headers : undefined)).get('Last-Event-ID'), started: performance.now() } : null;
  if (entry) window.__sseBrowserProof.push(entry);
  try {
   const response = await original.apply(this, arguments);
   if (entry) entry.status = response.status;
   return response;
  } catch(error) {
   if (entry) entry.error = error.name;
   throw error;
  }
 };
})();
