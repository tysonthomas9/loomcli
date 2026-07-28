package leadcontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/olesho/harness-wrapper/pkg/chat"
	harnesstranscript "github.com/olesho/harness-wrapper/pkg/transcript"
	transcriptclaude "github.com/olesho/harness-wrapper/pkg/transcript/claudecode"
	transcriptcodex "github.com/olesho/harness-wrapper/pkg/transcript/codex"
	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

type boundedHarnessHistory struct {
	turns       []chat.Turn
	handled     bool
	truncated   bool
	limitBytes  int
	sourceCause string
}

// harnessConversation is the slice of chat.Conversation (plus its underlying
// wrapper.Session) that lead delivery and the lead runtime need. It exists as
// an interface so tests can inject fakes, mirroring dialCodexAppServerClient.
type harnessConversation interface {
	AcquireControl(ctx context.Context) (release func(), err error)
	Send(ctx context.Context, text string) (turnID string, err error)
	WriteStdin(p []byte) (int, error)
	AttachOutput(w io.Writer) func()
	Resize(cols, rows uint16) error
	Snapshot() wrapper.Snapshot
	PID() int
	ChatSessionID() string
	HarnessSessionID() string
	History(ctx context.Context) ([]chat.Turn, error)
	HistoryWithinRawLimit(
		ctx context.Context,
		harnessSessionID string,
		limitBytes int,
	) (boundedHarnessHistory, error)
	Events() <-chan chat.TurnEvent
	Wait() (wrapper.Result, error)
	Close(ctx context.Context) error
}

// openHarnessConversation is the production factory; tests replace it.
var openHarnessConversation = func(ctx context.Context, opts chat.Options) (harnessConversation, error) {
	conv, err := chat.Open(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &chatHarnessConversation{conv: conv, store: opts.Store, opts: opts}, nil
}

type chatHarnessConversation struct {
	conv *chat.Conversation
	// store is retained because Conversation does not expose the harness
	// session ID (claude --resume UUID) directly; it is backfilled onto the
	// chat.Session record by the adapter when extracted.
	store chat.Store
	opts  chat.Options
}

func (c *chatHarnessConversation) AcquireControl(ctx context.Context) (func(), error) {
	return c.conv.AcquireControl(ctx)
}

func (c *chatHarnessConversation) Send(ctx context.Context, text string) (string, error) {
	return c.conv.Send(ctx, text)
}

func (c *chatHarnessConversation) WriteStdin(p []byte) (int, error) {
	return c.conv.Wrapper().WriteStdin(p)
}

func (c *chatHarnessConversation) AttachOutput(w io.Writer) func() {
	return c.conv.Wrapper().AttachOutput(w)
}

func (c *chatHarnessConversation) Resize(cols, rows uint16) error {
	return c.conv.Wrapper().Resize(cols, rows)
}

func (c *chatHarnessConversation) Snapshot() wrapper.Snapshot {
	return c.conv.Wrapper().Snapshot()
}

func (c *chatHarnessConversation) PID() int { return c.conv.Wrapper().PID() }

func (c *chatHarnessConversation) ChatSessionID() string { return c.conv.SessionID() }

func (c *chatHarnessConversation) HarnessSessionID() string {
	if c.store == nil {
		return ""
	}
	sess, err := c.store.GetSession(context.Background(), c.conv.SessionID())
	if err != nil {
		return ""
	}
	return sess.HarnessSessionID
}

func (c *chatHarnessConversation) History(ctx context.Context) ([]chat.Turn, error) {
	return c.conv.History(ctx)
}

// HistoryWithinRawLimit bypasses Conversation.History for native Codex and
// Claude transcripts. harness-wrapper currently reads those JSONL files with
// os.ReadFile, so applying Loom's canonical output limit afterward is too late
// to prevent an arbitrarily large allocation. Gemini's reader is also
// unbounded across the complete file; when an external session ID would make
// that native path reachable, fail closed until Loom has a bounded reader.
// Generic and session-ID-less store-backed histories remain unhandled so the
// caller can preserve Conversation.History's in-memory fallback.
func (c *chatHarnessConversation) HistoryWithinRawLimit(
	ctx context.Context,
	harnessSessionID string,
	limitBytes int,
) (boundedHarnessHistory, error) {
	harnessName := strings.TrimSpace(c.opts.Harness)
	if harnessName != HarnessNameCodex &&
		harnessName != HarnessNameClaudeCode &&
		harnessName != HarnessNameGemini {
		return boundedHarnessHistory{}, nil
	}
	harnessSessionID = strings.TrimSpace(harnessSessionID)
	if harnessSessionID == "" {
		harnessSessionID = strings.TrimSpace(c.HarnessSessionID())
	}
	if harnessSessionID == "" {
		return boundedHarnessHistory{}, nil
	}
	if harnessName == HarnessNameGemini {
		return boundedHarnessHistory{handled: true},
			errors.New("bounded gemini native transcript capture is unavailable")
	}
	turns, truncated, err := readBoundedNativeHarnessHistory(
		ctx,
		harnessName,
		harnessSessionID,
		c.opts.WorkingDir,
		c.opts.Env,
		limitBytes,
	)
	result := boundedHarnessHistory{
		turns:     turns,
		handled:   true,
		truncated: truncated,
	}
	if truncated {
		result.limitBytes = limitBytes
		result.sourceCause = transcriptSourceCauseHarnessNative
	}
	return result, err
}

func readBoundedNativeHarnessHistory(
	ctx context.Context,
	harnessName string,
	harnessSessionID string,
	workingDir string,
	env []string,
	limitBytes int,
) ([]chat.Turn, bool, error) {
	if err := validateBoundedNativeHarnessHistory(ctx, harnessSessionID, limitBytes); err != nil {
		return nil, false, err
	}
	path, err := nativeHarnessTranscriptPath(
		ctx,
		harnessName,
		harnessSessionID,
		workingDir,
		env,
	)
	if err != nil {
		return nil, false, err
	}
	data, truncated, err := readFileWithinLimit(path, limitBytes)
	if err != nil || truncated {
		return nil, truncated, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	turns, err := parseBoundedNativeHarnessHistory(harnessName, data)
	return turns, false, err
}

func validateBoundedNativeHarnessHistory(
	ctx context.Context,
	harnessSessionID string,
	limitBytes int,
) error {
	if limitBytes <= 0 {
		return errors.New("harness native transcript limit must be positive")
	}
	if strings.TrimSpace(harnessSessionID) == "" ||
		filepath.Base(harnessSessionID) != harnessSessionID {
		return errors.New("harness native transcript session id is invalid")
	}
	return ctx.Err()
}

func parseBoundedNativeHarnessHistory(harnessName string, data []byte) ([]chat.Turn, error) {
	events, err := parseNativeHarnessEvents(harnessName, data)
	if err != nil {
		return nil, fmt.Errorf("parse bounded %s transcript: %w", harnessName, err)
	}
	nativeTurns := harnesstranscript.TurnsFromEvents(events)
	turns := make([]chat.Turn, 0, len(nativeTurns))
	for _, turn := range nativeTurns {
		turns = append(turns, chat.Turn{
			Role:        chat.Role(turn.Role),
			State:       chat.TurnStateComplete,
			Text:        turn.Text,
			StartedAt:   turn.Timestamp,
			CompletedAt: turn.Timestamp,
		})
	}
	return turns, nil
}

func parseNativeHarnessEvents(harnessName string, data []byte) ([]harnesstranscript.Event, error) {
	switch harnessName {
	case HarnessNameCodex:
		return transcriptcodex.Events(data)
	case HarnessNameClaudeCode:
		return transcriptclaude.Events(data)
	default:
		return nil, fmt.Errorf("bounded native transcript unsupported for harness %q", harnessName)
	}
}

func nativeHarnessTranscriptPath(
	ctx context.Context,
	harnessName string,
	harnessSessionID string,
	workingDir string,
	env []string,
) (string, error) {
	switch harnessName {
	case HarnessNameClaudeCode:
		return claudeNativeHarnessTranscriptPath(harnessSessionID, workingDir, env)
	case HarnessNameCodex:
		return codexNativeHarnessTranscriptPath(ctx, harnessSessionID, env)
	default:
		return "", fmt.Errorf("bounded native transcript unsupported for harness %q", harnessName)
	}
}

func claudeNativeHarnessTranscriptPath(
	harnessSessionID string,
	workingDir string,
	env []string,
) (string, error) {
	if strings.TrimSpace(workingDir) == "" {
		return "", errors.New("claude native transcript working directory is empty")
	}
	configDir, err := claudeHarnessConfigDir(env)
	if err != nil {
		return "", err
	}
	path := filepath.Join(
		configDir,
		"projects",
		transcriptclaude.EncodedCWD(workingDir),
		harnessSessionID+".jsonl",
	)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("locate claude native transcript %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("claude native transcript %s is not a regular file", path)
	}
	return path, nil
}

func claudeHarnessConfigDir(env []string) (string, error) {
	if override, ok := harnessEnvValue(env, "CLAUDE_CONFIG_DIR"); ok &&
		strings.TrimSpace(override) != "" {
		return override, nil
	}
	home, err := harnessHomeDir(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func codexNativeHarnessTranscriptPath(
	ctx context.Context,
	harnessSessionID string,
	env []string,
) (string, error) {
	root, err := codexHarnessSessionsRoot(env)
	if err != nil {
		return "", err
	}
	suffix := "-" + harnessSessionID + ".jsonl"
	var found string
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), suffix) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("locate codex native transcript under %s: %w", root, walkErr)
	}
	if found == "" {
		return "", fmt.Errorf("locate codex native transcript for %s under %s: %w",
			harnessSessionID, root, os.ErrNotExist)
	}
	return found, nil
}

func codexHarnessSessionsRoot(env []string) (string, error) {
	if override, ok := harnessEnvValue(env, "CODEX_HOME"); ok &&
		strings.TrimSpace(override) != "" {
		return filepath.Join(override, "sessions"), nil
	}
	home, err := harnessHomeDir(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

func readFileWithinLimit(path string, limitBytes int) ([]byte, bool, error) {
	file, err := os.Open(path) //nolint:gosec // resolved under the selected harness config root
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, int64(limitBytes)+1))
	if err != nil {
		return nil, false, fmt.Errorf("read native transcript %s: %w", path, err)
	}
	if len(data) > limitBytes {
		return nil, true, nil
	}
	return data, false, nil
}

func harnessHomeDir(env []string) (string, error) {
	if home, ok := harnessEnvValue(env, "HOME"); ok && strings.TrimSpace(home) != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve harness home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("resolve harness home directory: empty home")
	}
	return home, nil
}

func harnessEnvValue(env []string, key string) (string, bool) {
	for index := len(env) - 1; index >= 0; index-- {
		name, value, ok := strings.Cut(env[index], "=")
		if ok && name == key {
			return value, true
		}
	}
	if env == nil {
		return os.LookupEnv(key)
	}
	return "", false
}

func (c *chatHarnessConversation) Events() <-chan chat.TurnEvent { return c.conv.Events() }

func (c *chatHarnessConversation) Wait() (wrapper.Result, error) { return c.conv.Wrapper().Wait() }

func (c *chatHarnessConversation) Close(ctx context.Context) error { return c.conv.Close(ctx) }
