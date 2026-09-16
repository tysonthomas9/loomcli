// Package tsruntime invokes an agent through loom's TypeScript runtime.
//
// Invoker wraps an existing cli.AgentInvoker and returns one that prefers the
// TypeScript runtime, falling back to the supplied invoker when that runtime
// is unavailable. Shaping it as a decorator keeps the choice of runtime out of
// the call sites that launch agents — they hold an AgentInvoker and do not
// need to know which implementation answered.
package tsruntime
