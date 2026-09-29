// Package realtime owns loom's push transports: Server-Sent Events and the
// WebSocket bridge to terminal PTYs.
//
// Writer is the single seam through which every production SSE frame is
// encoded — resumable and non-resumable events, retry directives, and comments
// alike. Its underlying writer and flusher are deliberately private, and an
// AST-based test rejects SSE field-prefix literals in production code outside
// writer.go, so a handler cannot quietly hand-roll a frame again. See
// docs/adr/0001-sse-framing-single-writer-seam.md for why, and CONTEXT.md
// (Streaming Language) for the vocabulary. Stream handlers keep everything
// that is not framing: authentication, filtering, and what to send.
//
// Hub fans events out to connected clients. Delivery is filtered per client
// rather than per event source — MatchesWorkspaceFilter and
// MatchesSourceRepoFilter decide whether a given subscriber should see a
// mutation, so one stream can serve clients scoped differently.
//
// Resumption is client-driven: a reconnecting client sends its last event
// position, parsed by ParseLastSince and ParseLastSinceMillis, and NextEventID
// supplies the monotonic IDs that make that possible. RetryMs tells the
// browser how long to wait before reconnecting.
//
// Streams are authenticated with short-lived tokens from TokenStore, separate
// from ordinary session auth because an EventSource cannot set headers;
// TokenExpiry and TerminalTokenExpiry keep that window small.
//
// TruncateUTF8 bounds frame payloads on rune boundaries, since splitting a
// multi-byte character across frames produces output no client can decode.
package realtime
