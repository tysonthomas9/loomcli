package local

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
)

// browserSessionCmd is the native-shell client for local desktop operator
// sessions. The Tauri shell runs it as a sidecar and hands the bearer to its
// WebView over IPC. The bearer is only ever written to stdout (issue) or read
// from stdin (refresh/revoke) — never argv, env, or logs.
var browserSessionCmd = &cobra.Command{
	Use:   "browser-session <issue|refresh|revoke>",
	Short: "Issue, refresh or revoke a local desktop browser operator session",
	Long: `Talk to the local runtime's per-user browser operator socket.

issue    prints {"ok":true,"token":...} for --workspace
refresh  reads the bearer from stdin and extends its idle deadline
revoke   reads the bearer from stdin and ends the session

Output is exactly one JSON object on stdout. Any process running as this OS
user can use this command: the local desktop trust boundary is the OS account.`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runBrowserSession,
}

var browserSessionWorkspace string

func init() {
	browserSessionCmd.Flags().StringVar(&browserSessionWorkspace, "workspace", "", "Workspace key the session is bound to (issue)")
	browserSessionCmd.Flags().Bool("json", true, "Print JSON (always on; accepted for symmetry)")
	localCmd.AddCommand(browserSessionCmd)
}

// errBrowserSessionFailed marks a failure already reported as JSON on stdout.
var errBrowserSessionFailed = errors.New("browser session request failed")

func runBrowserSession(cmd *cobra.Command, args []string) error {
	resp := browserSessionRequest(cmd.Context(), args[0], browserSessionWorkspace, dataDirFlag, cmd.InOrStdin())
	out := cmd.OutOrStdout()
	if err := json.NewEncoder(out).Encode(resp); err != nil {
		return err
	}
	if !resp.OK {
		return errBrowserSessionFailed
	}
	return nil
}

func browserSessionRequest(ctx context.Context, action, workspace, dataDirOverride string, stdin io.Reader) browserauth.SocketResponse {
	fail := func(code, msg string) browserauth.SocketResponse {
		return browserauth.SocketResponse{OK: false, Code: code, Error: msg}
	}
	req := browserauth.SocketRequest{Op: action}
	switch action {
	case browserauth.OpIssue:
		workspace = strings.TrimSpace(workspace)
		if workspace == "" {
			return fail("bad_request", "--workspace is required")
		}
		req.Workspace = workspace
	case browserauth.OpRefresh, browserauth.OpRevoke:
		token, err := readBearer(stdin)
		if err != nil {
			return fail("bad_request", err.Error())
		}
		req.Token = token
	default:
		return fail("bad_request", "action must be issue, refresh or revoke")
	}
	dataDir, err := resolveDataDir(dataDirOverride)
	if err != nil {
		return fail("runtime_unavailable", err.Error())
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := browserauth.CallOperatorSocket(ctx, browserauth.OperatorSocketPath(dataDir), req)
	if err != nil {
		return fail("bridge_unavailable", "local browser operator bridge unavailable; is the Loom local runtime running? ("+err.Error()+")")
	}
	return resp
}

// readBearer reads a single bearer line from stdin.
func readBearer(r io.Reader) (string, error) {
	if r == nil {
		r = os.Stdin
	}
	line, err := bufio.NewReader(io.LimitReader(r, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read session token from stdin: %w", err)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", errors.New("session token must be provided on stdin")
	}
	return token, nil
}
