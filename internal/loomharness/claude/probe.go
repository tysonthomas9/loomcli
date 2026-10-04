// The capability probe follows T3 Code
// apps/server/src/provider/Layers/ClaudeProvider.ts (probeClaudeCapabilities,
// claudeAuthMetadata, dedupeSlashCommands) and Drivers/ClaudeDriver.ts (the
// 5-minute refresh) at commit 2daff8c25.
//
// Copyright (c) 2026 T3 Tools Inc. Licensed under the MIT License; the full
// notice is in THIRD_PARTY_NOTICES.md.

package claude

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

const (
	probeTimeout = 25 * time.Second // Bedrock's init is slow (T3)
	probeEvery   = 5 * time.Minute  // T3's provider health refresh
)

var _ loomharness.CapabilityReporter = (*Adapter)(nil)

// Capabilities is the last good probe result; ok is false before one.
func (a *Adapter) Capabilities() (loomharness.Capabilities, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.caps, a.probed
}

// StartProbe probes now and then every 5 minutes until ctx ends, in the
// background. A failed probe keeps the last good result and logs a warning.
func (a *Adapter) StartProbe(ctx context.Context) {
	go a.probeLoop(ctx, probeEvery, a.probeOnce)
}

func (a *Adapter) probeLoop(ctx context.Context, every time.Duration, probe func(context.Context) (loomharness.Capabilities, error)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		pctx, cancel := context.WithTimeout(ctx, probeTimeout)
		caps, err := probe(pctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("claude capability probe failed; keeping the last result", "error", err)
		} else {
			a.mu.Lock()
			a.caps, a.probed = caps, true
			a.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// probeOnce starts claude on the stream-json protocol and sends only the
// initialize control request, never a prompt, so nothing reaches the API. As
// T3's probe it runs no hooks and no MCP servers and saves no session. Only
// user settings are read: the probe has no project directory.
func (a *Adapter) probeOnce(ctx context.Context) (loomharness.Capabilities, error) {
	cmd := exec.CommandContext(ctx, a.cfg.Bin, "-p", "--input-format", "stream-json", "--output-format", "stream-json", //nolint:gosec // G204: the configured claude binary.
		"--verbose", "--setting-sources", "user", "--settings", `{"disableAllHooks":true}`, "--strict-mcp-config",
		"--no-session-persistence")
	cmd.Env = append(NewProcess(a.cfg, ProcessSpec{}).env(), "ENABLE_CLAUDEAI_MCP_SERVERS=false")
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return loomharness.Capabilities{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return loomharness.Capabilities{}, err
	}
	if err := cmd.Start(); err != nil {
		return loomharness.Capabilities{}, fmt.Errorf("claude: start: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if _, err := stdin.Write([]byte(`{"type":"control_request","request_id":"loom-probe","request":{"subtype":"initialize"}}` + "\n")); err != nil {
		return loomharness.Capabilities{}, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var f struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Response  json.RawMessage `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(sc.Bytes(), &f) != nil || f.Type != "control_response" || f.Response.RequestID != "loom-probe" {
			continue
		}
		if f.Response.Subtype != "success" {
			// The CLI's error text is never kept: it could carry a credential.
			return loomharness.Capabilities{}, errors.New("claude refused the initialize request")
		}
		return parseInit(f.Response.Response, time.Now())
	}
	if err := ctx.Err(); err != nil {
		return loomharness.Capabilities{}, err
	}
	return loomharness.Capabilities{}, errors.New("claude exited before answering initialize")
}

// parseInit reads the initialize response: the account's kind and label
// (never its email) and the slash commands, with T3's built-in compact first.
func parseInit(raw json.RawMessage, at time.Time) (loomharness.Capabilities, error) {
	var init struct {
		Account *struct {
			SubscriptionType string `json:"subscriptionType"`
			TokenSource      string `json:"tokenSource"`
			APIProvider      string `json:"apiProvider"`
		} `json:"account"`
		Commands []struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			ArgumentHint string `json:"argumentHint"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		return loomharness.Capabilities{}, fmt.Errorf("claude initialize response: %w", err)
	}
	caps := loomharness.Capabilities{AccountKind: "unknown", ProbedAt: at}
	if acc := init.Account; acc != nil {
		switch {
		case isAPIKey(acc.TokenSource):
			caps.AccountKind, caps.AccountLabel = "api_key", "Claude API Key"
		case acc.SubscriptionType != "":
			caps.AccountKind, caps.AccountLabel = "subscription", subscriptionAuthLabel(acc.SubscriptionType)
		case acc.APIProvider == "bedrock":
			caps.AccountKind, caps.AccountLabel = "bedrock", "Amazon Bedrock"
		}
	}
	cmds := []loomharness.SlashCommand{{Name: "compact", Description: "Summarize the conversation and reduce context usage"}}
	for _, c := range init.Commands {
		cmds = append(cmds, loomharness.SlashCommand{Name: strings.TrimSpace(c.Name),
			Description: strings.TrimSpace(c.Description), ArgumentHint: strings.TrimSpace(c.ArgumentHint)})
	}
	caps.SlashCommands = dedupeCommands(cmds)
	return caps, nil
}

// dedupeCommands drops unnamed commands and merges names case-insensitively,
// the first keeping its fields and taking a missing description or hint
// from a later one.
func dedupeCommands(cmds []loomharness.SlashCommand) []loomharness.SlashCommand {
	var out []loomharness.SlashCommand
	at := map[string]int{}
	for _, c := range cmds {
		if c.Name == "" {
			continue
		}
		i, ok := at[strings.ToLower(c.Name)]
		if !ok {
			at[strings.ToLower(c.Name)] = len(out)
			out = append(out, c)
			continue
		}
		out[i].Description = cmp.Or(out[i].Description, c.Description)
		out[i].ArgumentHint = cmp.Or(out[i].ArgumentHint, c.ArgumentHint)
	}
	return out
}

func squash(s string) string {
	return strings.NewReplacer(" ", "", "_", "", "-", "", "\t", "").Replace(strings.ToLower(s))
}

func isAPIKey(tokenSource string) bool {
	switch squash(tokenSource) {
	case "apikey", "anthropicapikey", "anthropicauthtoken":
		return true
	}
	return false
}

var subscriptionLabels = map[string]string{
	"claudemaxsubscription": "Max", "claudemax5xsubscription": "Max 5x", "claudemax20xsubscription": "Max 20x",
	"claudeenterprisesubscription": "Enterprise", "claudeteamsubscription": "Team", "claudeprosubscription": "Pro",
	"claudefreesubscription": "Free", "max": "Max", "maxplan": "Max", "max5": "Max 5x", "max20": "Max 20x",
	"enterprise": "Enterprise", "team": "Team", "pro": "Pro", "free": "Free",
}

func titleWords(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '_' || r == '-' || r == '\t' })
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
	}
	return strings.Join(parts, " ")
}

// subscriptionAuthLabel is T3's formatClaudeSubscriptionAuthLabel, e.g.
// "max" → "Claude Max Subscription".
func subscriptionAuthLabel(subscriptionType string) string {
	label, ok := subscriptionLabels[squash(subscriptionType)]
	if !ok {
		label = titleWords(subscriptionType)
	}
	n := squash(label)
	switch {
	case strings.HasPrefix(n, "claude") && strings.HasSuffix(n, "subscription"):
		return label
	case strings.HasPrefix(n, "claude"):
		return label + " Subscription"
	case strings.HasSuffix(n, "subscription"):
		return "Claude " + label
	}
	return "Claude " + label + " Subscription"
}
