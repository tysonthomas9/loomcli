package exe

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// labelTagSeparator joins a label key and value into one flat tag.
//
// exe.dev tags are FLAT STRINGS, not key/value pairs, so loom's labels have to
// be encoded into them and decoded back. The separator must be legal in the
// tag grammar (lowercase alphanumeric plus . _ -), which rules out "=".
const labelTagSeparator = "__"

// labelsToTags encodes labels as flat tags, dropping any pair that cannot be
// expressed in the tag grammar.
//
// Dropping rather than erroring is deliberate and narrow: labels are how
// reconciliation and orphan reaping find a sandbox, and a create that FAILS on
// an unencodable cosmetic label would be worse than one that proceeds. The
// labels that matter -- placement id, deployment id, workspace, agent -- are
// broker-generated and always encodable; a dropped one shows up as a sandbox
// that does not match its filter, which the reaper surfaces.
func labelsToTags(labels map[string]string) []string {
	tags := make([]string, 0, len(labels))
	for _, key := range sortedKeys(labels) {
		tag := strings.ToLower(key + labelTagSeparator + labels[key])
		if reTag.MatchString(tag) {
			tags = append(tags, tag)
		}
	}
	return tags
}

// tagsToLabels is the inverse. A tag with no separator has no label form and
// is skipped rather than guessed at.
func tagsToLabels(tags []string) map[string]string {
	out := make(map[string]string, len(tags))
	for _, tag := range tags {
		key, value, ok := strings.Cut(tag, labelTagSeparator)
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}

// matchesLabels reports whether every wanted label is present with the same
// value. Comparison is lowercase because the tag encoding is lossy that way,
// and pretending otherwise would silently fail to match.
func matchesLabels(have, want map[string]string) bool {
	for key, value := range want {
		if !strings.EqualFold(have[key], value) {
			return false
		}
	}
	return true
}

// neutralState maps an exe.dev status onto the broker's reconciliation state.
//
// The default is ABSENT-free on purpose: an unrecognized status must not be
// read as "gone", because absence drives release, and releasing a live sandbox
// severs the only record of something billing. Unknown statuses report as
// running, which is the conservative direction -- worst case the broker keeps
// a record it did not need.
func neutralState(status string) placement.ProviderSandboxState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "active", "ready":
		return placement.ProviderSandboxRunning
	case "stopped", "paused", "suspended":
		return placement.ProviderSandboxStopped
	case "deleted", "destroyed", "terminated":
		return placement.ProviderSandboxAbsent
	default:
		return placement.ProviderSandboxRunning
	}
}

// rawState preserves the provider's own lifecycle word where the neutral state
// loses it, so attach readiness can be judged on what the platform said.
func rawState(status string) placement.ProviderSandboxRawState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "active", "ready":
		return placement.ProviderSandboxRawStarted
	case "stopped", "paused", "suspended":
		return placement.ProviderSandboxRawStopped
	case "deleted", "destroyed", "terminated":
		return placement.ProviderSandboxRawDestroyed
	case "error", "failed":
		return placement.ProviderSandboxRawError
	default:
		return placement.ProviderSandboxRawStarted
	}
}

func toProviderSandbox(v vm) placement.ProviderSandbox {
	return placement.ProviderSandbox{
		ID:        v.Name,
		Labels:    tagsToLabels(v.Tags),
		State:     neutralState(v.Status),
		RawState:  rawState(v.Status),
		CreatedAt: parseCreatedAt(v.CreatedAt),
	}
}

// parseCreatedAt returns the zero time when the timestamp is missing or
// unparseable. The reaper treats a zero CreatedAt as "too young to reap", so
// failing to parse errs toward NOT deleting.
func parseCreatedAt(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

// writeFile installs a file atomically (write-then-rename) via base64 over
// SSH, so content never has to survive shell quoting and can contain
// credentials without appearing in a command line.
func writeFile(client sshRunner, file placement.SandboxFile) error {
	path := strings.TrimSpace(file.Path)
	if path == "" {
		return fmt.Errorf("exe: sandbox file path required")
	}
	mode := strings.TrimSpace(file.Mode)
	if mode == "" {
		mode = "644"
	}
	tmp := path + ".loom-tmp"
	// Content is base64'd and fed on STDIN, never interpolated into the command.
	// Seeded files carry credentials (the codex auth.json rides this path), and
	// sshd runs a remote command as `sh -c '<string>'` -- so a command-line
	// payload is readable from the VM's process list by every process in the VM.
	//
	// umask 077 covers the window between creation and chmod; without it the
	// temp file exists world-readable for as long as the decode takes.
	cmd := fmt.Sprintf(
		"umask 077 && mkdir -p %s && base64 -d > %s && chmod %s %s && mv -f %s %s",
		shellQuote(dirOf(path)),
		shellQuote(tmp),
		shellQuote(mode),
		shellQuote(tmp),
		shellQuote(tmp),
		shellQuote(path),
	)
	encoded := base64.StdEncoding.EncodeToString(file.Content)
	if _, err := client.RunStdin(cmd, []byte(encoded)); err != nil {
		// The output is deliberately dropped: base64 -d echoes offending input
		// on a decode error, which would put file content into the error.
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

func dirOf(path string) string {
	if idx := strings.LastIndex(path, "/"); idx > 0 {
		return path[:idx]
	}
	return "."
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}
