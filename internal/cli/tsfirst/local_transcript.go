package tsfirst

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
)

type localTurn struct {
	Timestamp         string              `json:"timestamp"`
	OperationID       string              `json:"operation_id,omitempty"`
	Operation         *connectOperation   `json:"operation,omitempty"`
	ErrorClass        string              `json:"error_class,omitempty"`
	ErrorMessage      string              `json:"error_message,omitempty"`
	Agent             string              `json:"agent"`
	Instance          string              `json:"instance"`
	Session           string              `json:"session"`
	Backend           string              `json:"backend,omitempty"`
	Model             string              `json:"model,omitempty"`
	ProviderModel     string              `json:"provider_model,omitempty"`
	ProviderSessionID string              `json:"provider_session_id,omitempty"`
	ProviderMetadata  map[string]any      `json:"provider_metadata,omitempty"`
	DefinitionVersion string              `json:"definition_version,omitempty"`
	Message           string              `json:"message"`
	Response          string              `json:"response,omitempty"`
	DurationMS        int64               `json:"duration_ms,omitempty"`
	Usage             *connectUsage       `json:"usage,omitempty"`
	PromptHash        string              `json:"prompt_hash,omitempty"`
	ToolRuntime       *connectToolRuntime `json:"tool_runtime,omitempty"`
	ToolCalls         []connectToolCall   `json:"tool_calls,omitempty"`
	Resume            *connectResume      `json:"resume,omitempty"`
}

func readLocalTurns(path string) ([]localTurn, error) {
	f, err := os.Open(path) //nolint:gosec // local transcript path under the selected project root.
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local transcript %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []localTurn
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		var turn localTurn
		if err := json.Unmarshal(scanner.Bytes(), &turn); err != nil {
			return nil, fmt.Errorf("parse local transcript %s: %w", path, err)
		}
		out = append(out, turn)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read local transcript %s: %w", path, err)
	}
	return out, nil
}

func appendLocalTurn(path string, turn localTurn) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create local session dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // transcript file under selected project root.
	if err != nil {
		return fmt.Errorf("open local transcript %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := json.Marshal(turn)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write local transcript %s: %w", path, err)
	}
	return nil
}

func localConnectPrompt(plan *defspkg.Plan, agent defspkg.AgentModule, instance, session, message string, history []localTurn) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the TypeScript-defined Loom agent %q.\n", agent.Name)
	if agent.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", agent.Description)
	}
	if agent.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", agent.Model)
	}
	fmt.Fprintf(&b, "Instance: %s\nSession: %s\nProject root: %s\n", instance, session, plan.Root)
	if len(agent.Repos) > 0 {
		fmt.Fprintf(&b, "Runtime repos: %s\n", strings.Join(agent.Repos, ", "))
	}
	if len(agent.AllowedCommands) > 0 || len(agent.DeniedCommands) > 0 {
		fmt.Fprintf(&b, "Allowed commands: %s\nDenied commands: %s\n", strings.Join(agent.AllowedCommands, ", "), strings.Join(agent.DeniedCommands, ", "))
	}
	if len(agent.Skills) > 0 {
		fmt.Fprintf(&b, "Registered skills: %s\n", strings.Join(agent.Skills, ", "))
	}
	if strings.TrimSpace(agent.Instructions) != "" {
		fmt.Fprintf(&b, "\nAgent instructions:\n%s\n", strings.TrimSpace(agent.Instructions))
	}
	appendLocalConnectTypedToolContract(&b, plan, agent)
	if len(history) > 0 {
		fmt.Fprintf(&b, "\nRecent local session history:\n")
		start := 0
		if len(history) > 6 {
			start = len(history) - 6
		}
		for _, turn := range history[start:] {
			fmt.Fprintf(&b, "User: %s\nAgent: %s\n", turn.Message, turn.Response)
		}
		appendLocalConnectTypedToolResultFeed(&b, history[start:])
	}
	fmt.Fprintf(&b, "\nUser message:\n%s\n", message)
	return b.String()
}

func appendLocalConnectTypedToolContract(b *strings.Builder, plan *defspkg.Plan, agent defspkg.AgentModule) {
	runtime := localConnectToolRuntime(plan, agent)
	if runtime == nil || len(runtime.TypedTools) == 0 {
		return
	}
	contract := make([]map[string]any, 0, len(runtime.TypedTools))
	for _, tool := range runtime.TypedTools {
		entry := map[string]any{
			"name":       tool.Name,
			"parameters": tool.Parameters,
			"read_only":  tool.ReadOnly,
		}
		if tool.Description != "" {
			entry["description"] = tool.Description
		}
		if tool.Timeout != "" {
			entry["timeout"] = tool.Timeout
		}
		if tool.Cancellable {
			entry["cancellable"] = true
		}
		contract = append(contract, entry)
	}
	data, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return
	}
	fmt.Fprintf(b, "\nReviewed TypeScript model tools:\n%s\n", string(data))
	fmt.Fprintln(b, "To call one reviewed tool, emit exactly one JSON line and no surrounding prose:")
	fmt.Fprintln(b, `{"type":"loom.typed_tool.call","call_id":"stable-call-id","name":"tool_name","arguments":{}}`)
	fmt.Fprintln(b, "Loom executes matching reviewed tools through the trusted TypeScript handler boundary and records the result in local-connect evidence.")
}

func appendLocalConnectTypedToolResultFeed(b *strings.Builder, history []localTurn) {
	var entries []map[string]any
	for _, turn := range history {
		entries = append(entries, localConnectTypedToolResultEntries(turn.OperationID, turn.ToolCalls)...)
	}
	appendLocalConnectTypedToolResultEntries(b, "Recent typed tool results", entries)
}

func appendLocalConnectTypedToolResultEntries(b *strings.Builder, heading string, entries []map[string]any) {
	if len(entries) == 0 {
		return
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	fmt.Fprintf(b, "\n%s:\n%s\n", heading, string(data))
}

func localConnectTypedToolResultEntries(operationID string, calls []connectToolCall) []map[string]any {
	entries := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		entry := map[string]any{
			"call_id":              call.CallID,
			"name":                 call.Name,
			"status":               call.Status,
			"authorization_status": call.AuthorizationStatus,
			"redacted":             call.Redacted,
		}
		if operationID != "" {
			entry["operation_id"] = operationID
		}
		if call.Error != "" {
			entry["error"] = call.Error
		}
		if !call.Redacted && call.Result != nil {
			entry["result"] = call.Result
		}
		entries = append(entries, entry)
	}
	return entries
}

func lastProviderSessionID(history []localTurn) string {
	for i := len(history) - 1; i >= 0; i-- {
		if id := strings.TrimSpace(history[i].ProviderSessionID); id != "" {
			return id
		}
	}
	return ""
}

func localOperationID(agent, instance, session string, at time.Time) string {
	seed := strings.Join([]string{agent, instance, session, at.Format(time.RFC3339Nano)}, "\x00")
	sum := hashText(seed)
	if len(sum) > 16 {
		sum = sum[:16]
	}
	return "lc_" + sum
}

func stdinMessages() ([]string, bool, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return nil, true, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, false, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, false, nil
}

func localWorkDir(root string, agent defspkg.AgentModule) string {
	for _, repo := range agent.Repos {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		if repo == "." {
			return root
		}
		if filepath.IsAbs(repo) {
			return repo
		}
		return filepath.Join(root, repo)
	}
	return root
}

func localTranscriptPath(root, agent, instance, session string) string {
	return filepath.Join(root, ".loom", "local-sessions", safePathSegment(agent), safePathSegment(instance), safePathSegment(session)+".jsonl")
}

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func actorName() string {
	if actor := strings.TrimSpace(os.Getenv("LOOM_ACTOR")); actor != "" {
		return actor
	}
	if actor := strings.TrimSpace(os.Getenv("USER")); actor != "" {
		return actor
	}
	return "loom"
}

func compactStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func fallback(v, fallbackValue string) string {
	if strings.TrimSpace(v) == "" {
		return fallbackValue
	}
	return v
}

func importName(name string) string {
	parts := strings.Split(strings.TrimSpace(name), "-")
	if len(parts) == 0 {
		return "skill"
	}
	out := parts[0]
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		out += strings.ToUpper(part[:1]) + part[1:]
	}
	return out + "Skill"
}
