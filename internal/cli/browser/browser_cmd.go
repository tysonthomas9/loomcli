// Package browser implements `loom browser`, the interactive agent's view of
// its own durable browsers.
//
// The command authenticates with the agent-session bearer Loom injected into
// this agent's terminal at spawn (LOOM_AGENT_BROWSER_SESSION). The Loom
// server derives the owner from its own record of that binding; nothing this
// command sends — flags, LOOM_AGENT_NAME, X-Actor — can choose another owner.
package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

const defaultBrowserName = "Browser"

var (
	createName      string
	createRequestID string
	createJSON      bool
	stateJSON       bool

	// httpClient is swapped in tests.
	httpClient = &http.Client{Timeout: 15 * time.Second}
	getenv     = os.Getenv
	sleep      = time.Sleep
)

var browserCmd = &cobra.Command{
	Use:     "browser",
	Short:   "Manage this interactive agent's durable browsers",
	GroupID: "agents",
	Long: `Create and inspect browsers owned by this interactive agent.

These commands only work inside a Loom-launched interactive agent terminal,
which carries an agent-session credential bound to that agent. A browser is
created in the Starting state; this version does not launch a live page.`,
}

var createCmd = &cobra.Command{
	Use:           "create",
	Short:         "Create a browser owned by this agent (idempotent per --request-id)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runCreate(cmd.Context(), cmd.OutOrStdout())
	},
}

var stateCmd = &cobra.Command{
	Use:           "state",
	Short:         "List every browser owned by this agent",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runState(cmd.Context(), cmd.OutOrStdout())
	},
}

func init() {
	createCmd.Flags().StringVar(&createName, "name", defaultBrowserName, "Display name (not an identity key)")
	createCmd.Flags().StringVar(&createRequestID, "request-id", "", "Idempotency key; reuse it to retry safely (default: a new UUID)")
	createCmd.Flags().BoolVar(&createJSON, "json", false, "Print the browser as JSON")
	stateCmd.Flags().BoolVar(&stateJSON, "json", false, "Print JSON")
	browserCmd.AddCommand(createCmd, stateCmd)
	cli.RegisterCommand(browserCmd)
}

// State is the `loom browser state --json` document.
type State struct {
	Workspace    string           `json:"workspace"`
	OwnerAgentID string           `json:"owner_agent_id"`
	Browsers     []domain.Browser `json:"browsers"`
}

// APIError is a non-2xx response from the Loom browser routes.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("loom browser: %s (%s, HTTP %d)", e.Message, e.Code, e.Status)
	}
	return fmt.Sprintf("loom browser: %s (HTTP %d)", e.Message, e.Status)
}

// errNoAgentSession is returned when this process was not launched as a
// Loom interactive agent terminal. There is no fallback identity.
var errNoAgentSession = errors.New("loom browser: no Loom agent session in this terminal; run it from a Loom-launched interactive agent (the owner is never taken from LOOM_AGENT_NAME or flags)")

func runCreate(ctx context.Context, out io.Writer) error {
	req := domain.BrowserCreate{Name: createName, RequestID: createRequestID}
	if strings.TrimSpace(req.RequestID) == "" {
		req.RequestID = uuid.NewString()
	}
	if err := req.Normalize(); err != nil {
		return fmt.Errorf("loom browser create: %w", err)
	}
	var created domain.Browser
	// Retries reuse the same request ID, so a create that committed before a
	// transport failure is replayed, never duplicated.
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		err = call(ctx, http.MethodPost, "", req, &created)
		if !retryable(err) {
			break
		}
	}
	if err != nil {
		return err
	}
	if createJSON {
		return writeJSON(out, created)
	}
	_, err = fmt.Fprintf(out, "Browser %q %s (id %s, request %s)\n", created.Name, statusLabel(created.Status), created.ID, created.RequestID)
	return err
}

func runState(ctx context.Context, out io.Writer) error {
	var st State
	if err := call(ctx, http.MethodGet, "", nil, &st); err != nil {
		return err
	}
	if st.Browsers == nil {
		st.Browsers = []domain.Browser{}
	}
	if stateJSON {
		return writeJSON(out, st)
	}
	if len(st.Browsers) == 0 {
		_, err := fmt.Fprintf(out, "No browsers for %s.\n", st.OwnerAgentID)
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SELECTED\tNAME\tSTATUS\tID\tCREATED BY")
	for _, b := range st.Browsers {
		mark := ""
		if b.Selected {
			mark = "*"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", mark, b.Name, statusLabel(b.Status), b.ID, b.CreatedBy)
	}
	return tw.Flush()
}

func call(ctx context.Context, method, suffix string, body, out any) error {
	req, err := newRequest(ctx, method, suffix, body)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("loom browser: Loom server unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeResponse(resp, out)
}

// newRequest builds an agent-session request from this terminal's injected
// credentials. There is no fallback identity.
func newRequest(ctx context.Context, method, suffix string, body any) (*http.Request, error) {
	token := strings.TrimSpace(getenv(browserauth.EnvAgentSessionToken))
	base := strings.TrimRight(strings.TrimSpace(getenv(browserauth.EnvAgentBrowserURL)), "/")
	if token == "" || base == "" {
		return nil, errNoAgentSession
	}
	u, err := url.Parse(base + "/api/agent/browsers" + suffix)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("loom browser: invalid %s", browserauth.EnvAgentBrowserURL)
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set(browserauth.AgentSessionHeader, token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func decodeResponse(resp *http.Response, out any) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("loom browser: read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		var envelope struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.Error == "" {
			envelope.Error = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Code: envelope.Code, Message: envelope.Error}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("loom browser: decode response: %w", err)
	}
	return nil
}

// retryable reports transport failures and 5xx. 4xx (including 409 for a
// reused request ID) are final.
func retryable(err error) bool {
	if err == nil || errors.Is(err, errNoAgentSession) {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status >= 500 && apiErr.Status != http.StatusNotImplemented
	}
	return true
}

func statusLabel(status string) string {
	switch status {
	case domain.BrowserStatusStarting:
		return "Starting"
	case domain.BrowserStatusFailed:
		return "Failed"
	case domain.BrowserStatusReady:
		return "Ready"
	case "":
		return "Unknown"
	default:
		return "Unknown (" + status + ")"
	}
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
