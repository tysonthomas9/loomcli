package backends

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/flue"
)

// flueInvokeLead is the mockable seam for FlueBackend.InvokeLead.
var flueInvokeLead = defaultFlueInvokeLead

// errFlueTurnDone is a sentinel signaling the agent finished its turn (idle).
var errFlueTurnDone = errors.New("flue: turn done")

// defaultFlueInvokeLead runs the interactive lead session against a long-lived
// flue server. It starts (and owns) the server for the session, seeds the lead
// system prompt, then reads user input line-by-line, streaming each turn's
// output. Conversation state persists in the server across turns and across
// web-PTY reconnects (the owning process stays alive); the server is stopped
// when the session ends.
func defaultFlueInvokeLead(workDir, prompt string) error {
	ctx := context.Background()
	model := resolveFlueModel()

	srv, err := flue.DefaultManager().StartServer(ctx, slog.Default(), workDir, model)
	if err != nil {
		return err
	}
	defer srv.Stop()

	// Tear the server down on interrupt so Ctrl-C never leaves an orphaned node
	// process. In a terminal the whole foreground group receives SIGINT; this
	// also covers a signal delivered to the loom process alone, where Go's
	// default handling would otherwise exit without running deferred Stop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	// Close sigCh after stopping delivery so the watcher goroutine unblocks and
	// exits on a normal session end (otherwise it leaks, blocked on <-sigCh).
	defer func() {
		signal.Stop(sigCh)
		close(sigCh)
	}()
	go func() {
		if _, ok := <-sigCh; !ok {
			return
		}
		srv.Stop()
		os.Exit(130)
	}()

	instanceID := leadInstanceID(workDir)

	// Seed the rendered lead system prompt as the first message.
	if err := flueLeadPrompt(ctx, srv.URL(), instanceID, prompt, os.Stdout); err != nil {
		return fmt.Errorf("flue lead: initial turn: %w", err)
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\n> ")
		line, readErr := reader.ReadString('\n')
		if msg := strings.TrimSpace(line); msg != "" {
			if err := flueLeadPrompt(ctx, srv.URL(), instanceID, msg, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "\n[lead] error: %v\n", err)
			}
		}
		if readErr != nil {
			// EOF (Ctrl-D) or read error ends the session.
			fmt.Println()
			return nil
		}
	}
}

// leadInstanceID derives a stable, short agent-instance id from the workspace
// path. Stable so reconnects resume the same conversation; short so the codex
// provider's prompt_cache_key affinity stays within its 64-char cap.
func leadInstanceID(workDir string) string {
	sum := sha256.Sum256([]byte(workDir))
	return "ws-" + hex.EncodeToString(sum[:4])
}

// flueSSEEvent is the subset of flue agent SSE event fields the lead REPL uses.
type flueSSEEvent struct {
	Type          string `json:"type"`
	Text          string `json:"text"`
	Delta         string `json:"delta"`
	OperationKind string `json:"operationKind"`
	ToolName      string `json:"toolName"`
	Result        *struct {
		Text string `json:"text"`
	} `json:"result"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// flueLeadPrompt sends one message to the lead agent instance and streams the
// turn's output to out, returning when the turn goes idle.
func flueLeadPrompt(ctx context.Context, baseURL, instanceID, message string, out io.Writer) error {
	body, err := json.Marshal(map[string]string{"message": message, "session": "default"})
	if err != nil {
		return err
	}
	endpoint := baseURL + "/agents/lead/" + url.PathEscape(instanceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// No client timeout: an agent turn streams for an arbitrary duration.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("lead server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return streamFlueSSE(resp.Body, out)
}

// leadStream tracks per-turn streaming state while parsing the SSE response.
type leadStream struct {
	out       io.Writer
	streamed  bool
	finalText string
}

// handle processes one decoded SSE event. It returns errFlueTurnDone on idle.
func (s *leadStream) handle(payload string) error {
	var ev flueSSEEvent
	if json.Unmarshal([]byte(payload), &ev) != nil {
		return nil // ignore unparseable frames
	}
	switch ev.Type {
	case "text_delta":
		t := ev.Text
		if t == "" {
			t = ev.Delta
		}
		if t != "" {
			_, _ = fmt.Fprint(s.out, t)
			s.streamed = true
		}
	case "tool_start":
		if ev.ToolName != "" {
			_, _ = fmt.Fprintf(s.out, "\n  · %s\n", ev.ToolName)
		}
	case "operation":
		if ev.OperationKind == "prompt" && ev.Result != nil {
			s.finalText = ev.Result.Text
		}
	case "error":
		return flueEventError(ev)
	case "idle":
		// If nothing streamed live, fall back to the final answer text.
		if !s.streamed && s.finalText != "" {
			_, _ = fmt.Fprint(s.out, s.finalText)
		}
		_, _ = fmt.Fprintln(s.out)
		return errFlueTurnDone
	}
	return nil
}

func flueEventError(ev flueSSEEvent) error {
	if ev.Error != nil {
		return fmt.Errorf("%s: %s", ev.Error.Type, ev.Error.Message)
	}
	return errors.New("lead agent error")
}

// flueErrorEnvelope parses a raw (non-SSE) JSON error body, e.g. the
// "agent not registered" response. Returns nil if the line isn't an envelope.
func flueErrorEnvelope(line string) error {
	var env struct {
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(line), &env) == nil && env.Error != nil {
		return fmt.Errorf("%s: %s", env.Error.Type, env.Error.Message)
	}
	return nil
}

// streamFlueSSE parses the agent SSE stream, streaming assistant text to out
// and surfacing tool activity. Returns nil on idle (turn complete) and an error
// on an agent error event or a non-SSE error envelope.
func streamFlueSSE(r io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	st := &leadStream{out: out}
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if data.Len() == 0 {
				continue
			}
			payload := data.String()
			data.Reset()
			if err := st.handle(payload); err != nil {
				if errors.Is(err, errFlueTurnDone) {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, "data:"):
			// SSE joins multiple data: lines in one event with a newline.
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.HasPrefix(line, "{"):
			if err := flueErrorEnvelope(line); err != nil {
				return err
			}
		}
		// other SSE fields (event:, id:) are ignored
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out)
	return nil
}
