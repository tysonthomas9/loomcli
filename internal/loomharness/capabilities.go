package loomharness

import "time"

// CapabilityReporter is an optional port: a harness that probes its own
// account and commands in the background reports the last good result for
// dir, a repo clone whose project settings count, or "" for the harness
// alone. ok is false before the first good probe. It never blocks on a probe.
type CapabilityReporter interface {
	Capabilities(dir string) (caps Capabilities, ok bool)
}

// Capabilities is one probe result. It carries the account's kind and label
// only, never an email, key or token.
type Capabilities struct {
	AccountKind   string // subscription | api_key | bedrock | unknown
	AccountLabel  string // e.g. "Claude Max Subscription"; empty when unknown
	SlashCommands []SlashCommand
	ProbedAt      time.Time
}

// SlashCommand is one command the harness offers.
type SlashCommand struct {
	Name         string
	Description  string
	ArgumentHint string
}
