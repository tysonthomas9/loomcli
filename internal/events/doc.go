// Package events is loom's typed domain event log.
//
// Every observable thing the daemon does — an agent starting, stopping, or
// restarting, a circuit opening, an epic being assigned or exhausted, a
// config reload — has a named EventType and a matching payload struct.
// NewEvent pairs a type with its payload and the agent, role, and epic it
// concerns, so consumers can filter without decoding the payload first.
//
// Bus appends events as JSONL under eventsDir; ReadEventsFromJSONL replays
// them subject to EventReadOpts, and DefaultRetention bounds how far back the
// log is kept. Emitter is the narrow interface producers depend on, which
// keeps them testable without a real bus behind them.
//
// The package variable Now and SetContextProvider exist as seams for tests and
// for callers that need ambient context at emit time; production code should
// not reassign them.
package events
