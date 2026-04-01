// Package notify provides an in-process pub/sub bus with workspace-scoped
// subscriptions. It serves as the internal event backbone for loomcli's V2
// service architecture, decoupling event producers (service layer, daemon
// subscribers) from consumers (SSE hub, audit log, metrics exporters).
//
// This is a leaf package: it has zero imports of other internal packages,
// relying only on the Go standard library. The bus is purely in-memory with
// no persistence or replay capability.
package notify
