package wrapper

import (
	"strings"
	"time"

	claudeharness "github.com/tysonthomas9/loomcli/internal/harness/wrapper/internal/harness/claude"
	codexharness "github.com/tysonthomas9/loomcli/internal/harness/wrapper/internal/harness/codex"
	geminiharness "github.com/tysonthomas9/loomcli/internal/harness/wrapper/internal/harness/gemini"
)

// ClassifierInput is the snapshot a Classifier inspects when deciding
// whether to escalate the wrapper's status. It is rebuilt each time
// the wrapper polls the classifier; classifiers are stateless.
type ClassifierInput struct {
	// RecentOutput is the tail of the harness PTY output (last ~64KB),
	// ANSI escapes intact. Classifiers that grep should strip escapes.
	RecentOutput string

	// SinceLastOutput is the duration since the harness last produced a
	// byte. Classifiers can use this to distinguish "actively producing"
	// from "paused at a prompt".
	SinceLastOutput time.Duration

	// Quiet is true once SinceLastOutput >= IdleQuiet.
	Quiet bool

	// Idle is true once SinceLastOutput >= IdleClassify. The wrapper's
	// default behavior at this threshold is to classify the run as
	// idle; a Classifier can override by returning a non-zero
	// Classification.
	Idle bool
}

// Classification is a Classifier's verdict for a single ClassifierInput.
type Classification struct {
	// Status is the actionable status the classifier matched. The zero
	// value (empty string) means "no classification".
	Status Status

	// Reason is a short human-readable description that surfaces in the
	// Result and any emitted events.
	Reason string

	// Terminal indicates the wrapper should terminate the harness
	// process to make progress. Set true for blocked_by_cost and
	// retry_later; leave false for waiting_for_input where the harness
	// is alive and just paused at a prompt.
	Terminal bool

	// HTTPCode is the upstream API's HTTP status code when Status is
	// StatusAPIError and the harness surfaced a numeric code. Zero for
	// transport errors (e.g. socket closed) and for all non-api_error
	// classifications.
	HTTPCode int

	// RetryAfter is the wait duration the harness suggested (e.g.
	// "Retry after 30 seconds"). Zero when the message contained no
	// parseable hint.
	RetryAfter time.Duration
}

// Classifier inspects recent harness output and reports actionable
// status classifications. Implementations must be safe for concurrent
// use.
//
// Classifiers are stateless: the wrapper rebuilds ClassifierInput on
// each poll. Returning the same Classification across consecutive
// polls is fine; the wrapper de-duplicates emitted events.
type Classifier interface {
	Classify(input ClassifierInput) Classification
}

// ClassifierFunc adapts a function to the Classifier interface.
type ClassifierFunc func(input ClassifierInput) Classification

// Classify calls f.
func (f ClassifierFunc) Classify(input ClassifierInput) Classification { return f(input) }

// resolveClassifier picks the Classifier for a config. Order:
//  1. cfg.Classifier if set.
//  2. A per-harness classifier matching cfg.Harness.
//  3. A generic default that detects cost/quota patterns.
func resolveClassifier(cfg Config) Classifier {
	if cfg.Classifier != nil {
		return cfg.Classifier
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Harness)) {
	case "claude", "claude-code":
		return harnessAdapter{patterns: claudeharness.Patterns}
	case "codex":
		return harnessAdapter{patterns: codexharness.Patterns}
	case "gemini":
		return harnessAdapter{patterns: geminiharness.Patterns}
	}
	return defaultClassifier{}
}

// defaultClassifier is the built-in fallback. It only escalates to
// blocked_by_cost when recent output matches a known cost/quota
// fingerprint, and only after the wrapper has decided the run looks
// idle. This preserves the Phase 1 behavior of isCostOrQuotaLimited.
type defaultClassifier struct{}

// Classify returns blocked_by_cost when the harness has been quiet for
// the classify threshold and the recent output looks like a quota or
// rate-limit message. Otherwise it returns the zero Classification,
// letting the wrapper apply its default idle outcome.
func (defaultClassifier) Classify(input ClassifierInput) Classification {
	if !input.Idle {
		return Classification{}
	}
	if isCostOrQuotaLimited(input.RecentOutput) {
		return Classification{
			Status:   StatusBlockedByCost,
			Reason:   "cost, quota, or rate limit detected",
			Terminal: true,
		}
	}
	return Classification{}
}
