# All SSE framing goes through realtime.Writer

Five production streams (workspace realtime, driver epic watch, workflow runs,
log streaming, PR review) encoded Server-Sent Events with a mix of hand-rolled
framing and partial use of a shared writer, and the resulting drift produced
silently dropped write errors and a bypassed `connected` frame. We
consolidated every production SSE frame — resumable and non-resumable events,
retry directives, and comments (see `CONTEXT.md`, Streaming Language) — behind
`internal/webui/server/realtime.Writer`, made its underlying writer/flusher
private, and guarded the seam with an AST-based test that rejects SSE
field-prefix string literals in production code outside `writer.go` (a
tripwire for honest mistakes, not an enforcement proof). Stream handlers keep everything that is not framing: auth,
cursors, polling, heartbeat cadence, headers, deadlines, and payloads.

## Considered Options

- **Transport interface around the writer** — rejected: there is one production
  adapter (`http.ResponseWriter`), and tests already have a second real one in
  `httptest.ResponseRecorder`. An interface would be a mock-only port.
- **Generic `Frame` type or stream-runner abstraction** — rejected: callers
  should speak protocol concepts (event, retry, comment), not assemble frame
  structs; a runner would couple unrelated stream lifecycles.
- **JSON marshaling inside the writer** — rejected: marshaling is a
  domain-payload concern with per-stream failure policies (the realtime handler
  deliberately skips a mutation that fails to marshal; the workflow stream
  terminates).
- **PR #182's shallower consolidation** — reference only: it shared the writer
  but left the writer/flusher fields public and continued discarding write
  errors, so bypass and silent drops remained possible.
