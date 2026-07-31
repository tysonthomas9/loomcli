package interactionchat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"
	"github.com/olesho/harness-wrapper/pkg/transcript/claudecode"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const harnessReadRetryDelay = 75 * time.Millisecond

const (
	maxHarnessTranscriptBytes      = 8 << 20
	maxHarnessTranscriptEvents     = 10000
	maxHarnessTranscriptEventBytes = 4 << 20
)

var errHarnessTranscriptLimit = errors.New(
	"harness transcript exceeds Interaction read budget",
)

type harnessTranscriptReader interface {
	Read(
		context.Context,
		string,
		string,
	) ([]hwtranscript.Event, error)
}

func defaultHarnessReaders() map[string]harnessTranscriptReader {
	return map[string]harnessTranscriptReader{
		"claude": newBoundedClaudeReader(""),
		"gemini": newBoundedGeminiReader(""),
	}
}

type boundedClaudeReader struct {
	projectsRoot string
	maxBytes     int64
}

func newBoundedClaudeReader(projectsRoot string) *boundedClaudeReader {
	return &boundedClaudeReader{
		projectsRoot: strings.TrimSpace(projectsRoot),
		maxBytes:     maxHarnessTranscriptBytes,
	}
}

//nolint:funlen // Keep the byte/event limits, parser state, and transcript normalization in one bounded provider read.
func (reader *boundedClaudeReader) Read(
	ctx context.Context,
	sessionID,
	worktree string,
) ([]hwtranscript.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, interaction.ErrUnavailable
	}
	path, err := reader.locate(sessionID, worktree)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path) //nolint:gosec // path is confined to Claude's transcript root.
	if err != nil {
		return nil, fmt.Errorf(
			"claudecode transcript: open %s: %w",
			path,
			err,
		)
	}
	defer func() { _ = file.Close() }()

	limit := reader.maxBytes
	if limit <= 0 {
		limit = maxHarnessTranscriptBytes
	}
	if info, statErr := file.Stat(); statErr == nil &&
		info.Size() > limit {
		return nil, fmt.Errorf(
			"claudecode transcript %s is %d bytes, limit %d: %w",
			path,
			info.Size(),
			limit,
			errHarnessTranscriptLimit,
		)
	}
	var content bytes.Buffer
	content.Grow(int(min(limit, 64<<10)))
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			total += int64(count)
			if total > limit {
				return nil, fmt.Errorf(
					"claudecode transcript %s exceeds %d bytes: %w",
					path,
					limit,
					errHarnessTranscriptLimit,
				)
			}
			_, _ = content.Write(buffer[:count])
		}
		switch {
		case errors.Is(readErr, io.EOF):
			events, parseErr := claudecode.Events(content.Bytes())
			if parseErr != nil {
				return nil, parseErr
			}
			return boundedHarnessEvents(events)
		case readErr != nil:
			return nil, fmt.Errorf(
				"claudecode transcript: read %s: %w",
				path,
				readErr,
			)
		}
	}
}

func (reader *boundedClaudeReader) locate(
	sessionID,
	worktree string,
) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	worktree = strings.TrimSpace(worktree)
	if sessionID == "" ||
		sessionID != filepath.Base(sessionID) ||
		strings.ContainsAny(sessionID, `/\`) {
		return "", fmt.Errorf("claudecode transcript: invalid session id")
	}
	if worktree == "" {
		return "", fmt.Errorf("claudecode transcript: empty working dir")
	}
	root, err := reader.root()
	if err != nil {
		return "", err
	}
	candidates := make([]string, 0, 2)
	if resolved, resolveErr := filepath.EvalSymlinks(worktree); resolveErr == nil && resolved != worktree {
		candidates = append(candidates, resolved)
	}
	candidates = append(candidates, worktree)

	var firstPath string
	var firstErr error
	for _, candidate := range candidates {
		path := filepath.Join(
			root,
			claudecode.EncodedCWD(candidate),
			sessionID+".jsonl",
		)
		if _, statErr := os.Stat(path); statErr == nil {
			return path, nil
		} else if firstPath == "" {
			firstPath, firstErr = path, statErr
		}
	}
	return "", fmt.Errorf(
		"claudecode transcript: %s: %w",
		firstPath,
		firstErr,
	)
}

func (reader *boundedClaudeReader) root() (string, error) {
	if reader != nil && reader.projectsRoot != "" {
		return reader.projectsRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf(
			"claudecode transcript: resolve home: %w",
			err,
		)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

type boundedGeminiReader struct {
	geminiRoot string
	maxBytes   int64
}

func newBoundedGeminiReader(geminiRoot string) *boundedGeminiReader {
	return &boundedGeminiReader{
		geminiRoot: strings.TrimSpace(geminiRoot),
		maxBytes:   maxHarnessTranscriptBytes,
	}
}

//nolint:cyclop,funlen,gocognit // Keep Gemini's bounded stream parsing and event normalization in one fail-closed provider state machine.
func (reader *boundedGeminiReader) Read(
	ctx context.Context,
	sessionID,
	worktree string,
) ([]hwtranscript.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, interaction.ErrUnavailable
	}
	path, err := reader.locate(ctx, sessionID, worktree)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path) //nolint:gosec // path is confined to Gemini's transcript root.
	if err != nil {
		return nil, fmt.Errorf(
			"gemini transcript: open %s: %w",
			path,
			err,
		)
	}
	defer func() { _ = file.Close() }()

	limit := reader.maxBytes
	if limit <= 0 {
		limit = maxHarnessTranscriptBytes
	}
	if info, statErr := file.Stat(); statErr == nil &&
		info.Size() > limit {
		return nil, fmt.Errorf(
			"gemini transcript %s is %d bytes, limit %d: %w",
			path,
			info.Size(),
			limit,
			errHarnessTranscriptLimit,
		)
	}
	limited := &io.LimitedReader{R: file, N: limit + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(
		make([]byte, 0, int(min(limit+1, 64<<10))),
		int(min(limit+1, 4<<20)),
	)
	events := make([]hwtranscript.Event, 0, 32)
	lineNumber := 0
	eventBytes := 0
	for scanner.Scan() {
		lineNumber++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if limit+1-limited.N > limit {
			return nil, fmt.Errorf(
				"gemini transcript %s exceeds %d bytes: %w",
				path,
				limit,
				errHarnessTranscriptLimit,
			)
		}
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var line geminiTranscriptLine
		if err := json.Unmarshal(raw, &line); err != nil {
			return nil, fmt.Errorf(
				"gemini transcript: parse line %d in %s: %w",
				lineNumber,
				path,
				err,
			)
		}
		if line.Kind != "" && line.Role == "" && line.Type == "" {
			continue
		}
		role := geminiTranscriptRole(line.Role, line.Type)
		if role == "" {
			continue
		}
		text := geminiTranscriptText(&line)
		if text == "" {
			continue
		}
		eventBytes += len(text)
		if len(events) >= maxHarnessTranscriptEvents ||
			eventBytes > maxHarnessTranscriptEventBytes {
			return nil, fmt.Errorf(
				"gemini transcript event budget exceeded: %w",
				errHarnessTranscriptLimit,
			)
		}
		var timestamp time.Time
		if line.Timestamp != "" {
			timestamp, _ = time.Parse(time.RFC3339, line.Timestamp)
		}
		events = append(events, hwtranscript.Event{
			Seq:       len(events),
			Timestamp: timestamp,
			Role:      role,
			Type:      hwtranscript.EventText,
			Text:      text,
			Source:    hwtranscript.SourceFile,
		})
	}
	if limit+1-limited.N > limit {
		return nil, fmt.Errorf(
			"gemini transcript %s exceeds %d bytes: %w",
			path,
			limit,
			errHarnessTranscriptLimit,
		)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf(
			"gemini transcript: scan %s: %w",
			path,
			err,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

//nolint:funlen // Keep canonical path derivation, bounded discovery, ownership checks, and ambiguity rejection in one containment proof.
func (reader *boundedGeminiReader) locate(
	ctx context.Context,
	sessionID,
	worktree string,
) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	worktree = strings.TrimSpace(worktree)
	if sessionID == "" ||
		sessionID != filepath.Base(sessionID) ||
		strings.ContainsAny(sessionID, `/\`) {
		return "", fmt.Errorf("gemini transcript: invalid session id")
	}
	root, err := reader.root()
	if err != nil {
		return "", err
	}
	suffix := "-" + geminiSessionShort(sessionID) + ".jsonl"
	if worktree != "" {
		slug, found, slugErr := geminiProjectSlug(
			ctx,
			root,
			worktree,
		)
		if slugErr != nil {
			return "", slugErr
		}
		if found {
			path, ok, findErr := findGeminiTranscript(
				ctx,
				filepath.Join(root, "tmp", slug, "chats"),
				sessionID,
				suffix,
			)
			if findErr != nil {
				return "", findErr
			}
			if ok {
				return path, nil
			}
		}
	}
	tmpRoot := filepath.Join(root, "tmp")
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		return "", fmt.Errorf(
			"gemini transcript: read %s: %w",
			tmpRoot,
			err,
		)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !entry.IsDir() {
			continue
		}
		path, ok, findErr := findGeminiTranscript(
			ctx,
			filepath.Join(tmpRoot, entry.Name(), "chats"),
			sessionID,
			suffix,
		)
		if findErr != nil {
			return "", findErr
		}
		if ok {
			return path, nil
		}
	}
	return "", fmt.Errorf(
		"gemini transcript: no session file for %s under %s",
		sessionID,
		tmpRoot,
	)
}

func (reader *boundedGeminiReader) root() (string, error) {
	if reader != nil && reader.geminiRoot != "" {
		return reader.geminiRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf(
			"gemini transcript: resolve home: %w",
			err,
		)
	}
	return filepath.Join(home, ".gemini"), nil
}

//nolint:funlen // Keep worktree identity normalization and hash-compatible Gemini project lookup in one deterministic mapping.
func geminiProjectSlug(
	ctx context.Context,
	root,
	worktree string,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	path := filepath.Join(root, "projects.json")
	file, err := os.Open(path) //nolint:gosec // path is confined to Gemini's root.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf(
			"gemini transcript: open projects.json: %w",
			err,
		)
	}
	defer func() { _ = file.Close() }()
	const projectsBudget = 1 << 20
	data, err := io.ReadAll(io.LimitReader(file, projectsBudget+1))
	if err != nil {
		return "", false, fmt.Errorf(
			"gemini transcript: read projects.json: %w",
			err,
		)
	}
	if len(data) > projectsBudget {
		return "", false, fmt.Errorf(
			"gemini transcript projects.json exceeds %d bytes: %w",
			projectsBudget,
			errHarnessTranscriptLimit,
		)
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	var projects struct {
		Projects map[string]string `json:"projects"`
	}
	if err := json.Unmarshal(data, &projects); err != nil {
		return "", false, fmt.Errorf(
			"gemini transcript: parse projects.json: %w",
			err,
		)
	}
	slug := strings.TrimSpace(projects.Projects[worktree])
	if slug == "" {
		return "", false, nil
	}
	if slug != filepath.Base(slug) ||
		strings.ContainsAny(slug, `/\`) {
		return "", false, fmt.Errorf(
			"gemini transcript: invalid project slug",
		)
	}
	return slug, true, nil
}

func findGeminiTranscript(
	ctx context.Context,
	chatsDirectory,
	sessionID,
	suffix string,
) (string, bool, error) {
	entries, err := os.ReadDir(chatsDirectory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf(
			"gemini transcript: read %s: %w",
			chatsDirectory,
			err,
		)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		path := filepath.Join(chatsDirectory, entry.Name())
		if geminiTranscriptHeaderMatches(ctx, path, sessionID) {
			return path, true, nil
		}
	}
	return "", false, nil
}

func geminiTranscriptHeaderMatches(
	ctx context.Context,
	path,
	sessionID string,
) bool {
	if ctx.Err() != nil {
		return false
	}
	file, err := os.Open(path) //nolint:gosec // path is selected from Gemini's transcript directory.
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	if !scanner.Scan() || ctx.Err() != nil {
		return false
	}
	var header struct {
		SessionID string `json:"sessionId"`
	}
	return json.Unmarshal(scanner.Bytes(), &header) == nil &&
		header.SessionID == sessionID
}

func geminiSessionShort(sessionID string) string {
	if index := strings.IndexByte(sessionID, '-'); index >= 0 {
		sessionID = sessionID[:index]
	}
	if len(sessionID) > 8 {
		sessionID = sessionID[:8]
	}
	return sessionID
}

type geminiTranscriptLine struct {
	SessionID   string `json:"sessionId,omitempty"`
	ProjectHash string `json:"projectHash,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Role        string `json:"role,omitempty"`
	Parts       []struct {
		Text string `json:"text,omitempty"`
	} `json:"parts,omitempty"`
	Type      string          `json:"type,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
}

func geminiTranscriptRole(role, eventType string) string {
	switch role {
	case hwtranscript.RoleUser:
		return hwtranscript.RoleUser
	case "model", hwtranscript.RoleAssistant:
		return hwtranscript.RoleAssistant
	case hwtranscript.RoleSystem:
		return hwtranscript.RoleSystem
	}
	switch eventType {
	case hwtranscript.RoleUser:
		return hwtranscript.RoleUser
	case "model", hwtranscript.RoleAssistant:
		return hwtranscript.RoleAssistant
	case hwtranscript.RoleSystem, hwtranscript.RoleTool:
		return hwtranscript.RoleSystem
	default:
		return ""
	}
}

func geminiTranscriptText(line *geminiTranscriptLine) string {
	if line == nil {
		return ""
	}
	if len(line.Parts) > 0 {
		parts := make([]string, 0, len(line.Parts))
		for _, part := range line.Parts {
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	if len(line.Message) == 0 || line.Message[0] != '"' {
		return ""
	}
	var text string
	if json.Unmarshal(line.Message, &text) != nil {
		return ""
	}
	return text
}

func boundedHarnessEvents(
	events []hwtranscript.Event,
) ([]hwtranscript.Event, error) {
	if len(events) > maxHarnessTranscriptEvents {
		return nil, fmt.Errorf(
			"harness transcript has %d events, limit %d: %w",
			len(events),
			maxHarnessTranscriptEvents,
			errHarnessTranscriptLimit,
		)
	}
	total := 0
	for _, event := range events {
		total += len(event.Text) +
			len(event.Output) +
			len(event.ToolInput)
		if total > maxHarnessTranscriptEventBytes {
			return nil, fmt.Errorf(
				"harness transcript events exceed %d bytes: %w",
				maxHarnessTranscriptEventBytes,
				errHarnessTranscriptLimit,
			)
		}
	}
	return events, nil
}

func rememberedAgentWorktree(
	workspace,
	agentID string,
) (string, bool) {
	return localworkspace.RememberedAgentWorktree(workspace, agentID)
}

//nolint:funlen // Keep provider selection, bounded transcript read, canonical mapping, and ownership validation in one conversation read boundary.
func (runtime *Runtime) readHarnessConversation(
	ctx context.Context,
	query interaction.ConversationQuery,
	session *domain.AgentSession,
	provider string,
) (*interaction.Conversation, error) {
	reader, ok := runtime.harnesses[provider]
	if !ok {
		return &interaction.Conversation{
			State: interaction.ConversationUnsupported,
			Detail: "The chat view is not available for the " +
				provider +
				" backend. Use the Terminal tab to talk to the agent.",
		}, nil
	}
	metadata := leadcontrol.HarnessRuntimeMetadataFromSession(session)
	if metadata.HarnessSessionID == "" {
		if provider == "claude" {
			return &interaction.Conversation{
				State: interaction.ConversationStarting,
			}, nil
		}
		return &interaction.Conversation{
			State: interaction.ConversationUnsupported,
			Detail: "The " + provider +
				" backend does not expose its session id, so the chat view cannot read its conversation yet. Use the Terminal tab.",
		}, nil
	}
	worktree, ok := runtime.worktreeFor(
		query.WorkspaceKey,
		query.AgentID,
	)
	if !ok {
		return &interaction.Conversation{
			State:  interaction.ConversationFailed,
			Detail: "The agent's worktree is no longer available. Close and reopen the agent to recreate it.",
		}, nil
	}
	sessionID := metadata.HarnessSessionID
	events, err := runtime.readHarnessTranscript(
		ctx,
		reader,
		sessionID,
		worktree,
	)
	if errors.Is(err, fs.ErrNotExist) && provider == "claude" {
		if rotated, found := newestClaudeSessionSince(
			reader,
			worktree,
			metadata.StartedAt,
		); found {
			sessionID = rotated
			events, err = runtime.readHarnessTranscript(
				ctx,
				reader,
				sessionID,
				worktree,
			)
		}
	}
	state := conversationStateFromRuntimeStatus(metadata.Status)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if errors.Is(err, fs.ErrNotExist) {
			return &interaction.Conversation{State: state}, nil
		}
		if errors.Is(err, errHarnessTranscriptLimit) {
			return &interaction.Conversation{
				State: interaction.ConversationFailed,
				Detail: "The conversation transcript exceeds the " +
					"bounded read limit.",
			}, nil
		}
		return &interaction.Conversation{
			State: interaction.ConversationReconnecting,
		}, nil
	}
	return &interaction.Conversation{
		State:    state,
		Messages: harnessMessages(provider, sessionID, events),
	}, nil
}

func (runtime *Runtime) readHarnessTranscript(
	ctx context.Context,
	reader harnessTranscriptReader,
	sessionID,
	worktree string,
) ([]hwtranscript.Event, error) {
	events, err := reader.Read(ctx, sessionID, worktree)
	if err == nil ||
		errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, errHarnessTranscriptLimit) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return events, err
	}
	delay := runtime.retryDelay
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return reader.Read(ctx, sessionID, worktree)
}

func newestClaudeSessionSince(
	reader harnessTranscriptReader,
	worktree string,
	since time.Time,
) (string, bool) {
	if since.IsZero() {
		return "", false
	}
	root := ""
	if claude, ok := reader.(*boundedClaudeReader); ok {
		root, _ = claude.root()
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		root = filepath.Join(home, ".claude", "projects")
	}
	entries, err := os.ReadDir(
		filepath.Join(root, claudecode.EncodedCWD(worktree)),
	)
	if err != nil {
		return "", false
	}
	best := ""
	bestTime := time.Time{}
	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		if info.ModTime().After(bestTime) {
			best = strings.TrimSuffix(entry.Name(), ".jsonl")
			bestTime = info.ModTime()
		}
	}
	return best, best != ""
}

func conversationStateFromRuntimeStatus(
	status string,
) interaction.ConversationState {
	switch status {
	case leadcontrol.RuntimeStatusActive,
		leadcontrol.RuntimeStatusWaitingApproval:
		return interaction.ConversationRunning
	case leadcontrol.RuntimeStatusIdle,
		leadcontrol.RuntimeStatusWaitingUserInput:
		return interaction.ConversationIdle
	case "", leadcontrol.RuntimeStatusStarting:
		return interaction.ConversationStarting
	case leadcontrol.RuntimeStatusDisconnected:
		return interaction.ConversationReconnecting
	default:
		return interaction.ConversationFailed
	}
}

func harnessMessages(
	provider,
	sessionID string,
	events []hwtranscript.Event,
) []interaction.ConversationMessage {
	prefix := provider + "/" + shortSessionID(sessionID) + "/"
	ordinals := make(map[string]int)
	var out []interaction.ConversationMessage
	for _, event := range events {
		if event.Type != hwtranscript.EventText ||
			(event.Role != hwtranscript.RoleUser &&
				event.Role != hwtranscript.RoleAssistant) ||
			strings.TrimSpace(event.Text) == "" {
			continue
		}
		base := event.ID()
		itemID := prefix + base
		if ordinal := ordinals[base]; ordinal > 0 {
			itemID += "#" + strconv.Itoa(ordinal)
		}
		ordinals[base]++
		turnID := itemID
		if event.UUID != "" {
			turnID = prefix + "msg:" + event.UUID
		}
		out = append(out, interaction.ConversationMessage{
			TurnID: turnID,
			ItemID: itemID,
			Role:   event.Role,
			Text:   event.Text,
		})
	}
	return trimLaunchPreamble(out)
}

func shortSessionID(value string) string {
	if len(value) > 8 {
		return value[:8]
	}
	return value
}
