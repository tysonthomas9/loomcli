package daytona

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	apiclient "github.com/daytonaio/daytona/libs/api-client-go"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/placement"
)

func (p *Provider) prepareLeadCheckout(ctx context.Context, sandbox *apiclient.Sandbox, prep placement.LeadBootPrep) error {
	repo := prep.Repo
	checkout := strings.TrimSpace(repo.Checkout)
	if checkout == "" || !strings.HasPrefix(checkout, "/") {
		return fmt.Errorf("lead checkout path must be absolute: %w", domain.ErrInvalid)
	}
	remoteURL, host, err := placement.NormalizeRepoCloneRemote(repo.RemoteURL)
	if err != nil {
		return err
	}

	state, err := p.leadCheckoutState(ctx, sandbox, checkout)
	if err != nil {
		return err
	}
	if state == leadCheckoutPresentMarker {
		return nil
	}
	token, encoded, err := resolveLeadGitToken(prep.GitToken)
	if err != nil {
		return err
	}
	// POC: a shallow single-branch clone is enough for lead boot. The lead
	// holds no git credential (ticket 12), so it cannot deepen or fetch later;
	// full clone is the post-POC upgrade and needs async exec. Do not use
	// --filter=blob:none because lazy blob fetches would need mid-session auth.
	cloneCmd := leadCloneCommand(remoteURL, host, strings.TrimSpace(repo.Ref), checkout, encoded)
	if _, err := p.execLeadPrep(ctx, sandbox, cloneCmd, token, encoded); err != nil {
		return err
	}
	return p.assertLeadRemoteURL(ctx, sandbox, checkout, remoteURL, token, encoded)
}

func (p *Provider) leadCheckoutState(ctx context.Context, sandbox *apiclient.Sandbox, checkout string) (string, error) {
	// An existing but EMPTY directory counts as absent: git clone into an
	// empty directory succeeds, and the revive path may find one left by an
	// interrupted earlier prep.
	quoted := shellQuote(checkout)
	cmd := "if [ -e " + quoted + " ]; then " +
		"if git -C " + quoted + " rev-parse --is-inside-work-tree >/dev/null 2>&1; then " +
		"printf %s " + shellQuote(leadCheckoutPresentMarker) + "; " +
		"elif [ -z \"$(ls -A " + quoted + " 2>/dev/null)\" ]; then " +
		"printf %s " + shellQuote(leadCheckoutAbsentMarker) + "; " +
		"else exit 42; fi; " +
		"else printf %s " + shellQuote(leadCheckoutAbsentMarker) + "; fi"
	result, err := p.execLeadPrep(ctx, sandbox, cmd)
	if err != nil {
		var execErr *leadPrepExecError
		if errors.As(err, &execErr) && execErr.exitCode == leadCheckoutInvalidExitCode {
			return "", fmt.Errorf("lead checkout path %q exists but is not a git work tree: %w", checkout, domain.ErrInvalid)
		}
		return "", err
	}
	state := strings.TrimSpace(result.outputText())
	switch state {
	case leadCheckoutPresentMarker, leadCheckoutAbsentMarker:
		return state, nil
	default:
		return "", fmt.Errorf("lead checkout state probe returned %q: %w", state, domain.ErrInvalid)
	}
}

func resolveLeadGitToken(callback func() (string, error)) (token string, encoded string, err error) {
	if callback == nil {
		return "", "", nil
	}
	token, err = callback()
	if err != nil {
		return "", "", fmt.Errorf("resolve git token for lead boot: credential callback failed")
	}
	if token == "" {
		return "", "", nil
	}
	encoded = base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return token, encoded, nil
}

func leadCloneCommand(remoteURL, host, ref, checkout, encodedToken string) string {
	parts := []string{"git"}
	if encodedToken != "" {
		key := "http.https://" + host + "/.extraheader=AUTHORIZATION: basic " + encodedToken
		parts = append(parts, "-c", shellQuote(key))
	}
	parts = append(parts, "clone", "--depth", "1", "--single-branch")
	if ref != "" {
		parts = append(parts, "--branch", shellQuote(ref))
	}
	// The clone stages into a sibling .partial path and renames into place on
	// success, so a clone killed mid-transfer (context budget, network drop)
	// leaves NOTHING at the checkout path -- a partial directory there would
	// wedge every future resume as "exists but is not a git work tree". The
	// rm -rf targets only the staging path, never the checkout (which may
	// hold the lead's work).
	partial := checkout + ".partial"
	parts = append(parts, shellQuote(remoteURL), shellQuote(partial))
	return "rm -rf " + shellQuote(partial) +
		" && " + strings.Join(parts, " ") +
		" && mv " + shellQuote(partial) + " " + shellQuote(checkout)
}

func (p *Provider) assertLeadRemoteURL(
	ctx context.Context,
	sandbox *apiclient.Sandbox,
	checkout string,
	want string,
	redactions ...string,
) error {
	cmd := "git -C " + shellQuote(checkout) + " config --get remote.origin.url"
	result, err := p.execLeadPrep(ctx, sandbox, cmd, redactions...)
	if err != nil {
		return err
	}
	got := strings.TrimSpace(result.outputText())
	if strings.Contains(got, "@") || strings.Contains(strings.ToLower(got), "x-access-token") {
		return fmt.Errorf("lead clone persisted a credential-bearing remote URL")
	}
	if got != want {
		return fmt.Errorf("lead clone remote URL = %q, want %q", got, want)
	}
	return nil
}

func (p *Provider) writeLeadPrompt(ctx context.Context, sandbox *apiclient.Sandbox, promptPath, promptText string) error {
	promptPath = strings.TrimSpace(promptPath)
	if promptPath == "" || !strings.HasPrefix(promptPath, "/") {
		return fmt.Errorf("lead prompt path must be absolute when prompt text is provided: %w", domain.ErrInvalid)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(promptText))
	// Write-then-rename: `>` truncates before base64 writes, and a lead booted
	// by a previous Provision may be inside its one startup read of this file.
	// mv within one filesystem is atomic, so a reader sees the old prompt or
	// the new one, never a partial.
	cmd := "mkdir -p " + shellQuote(path.Dir(promptPath)) +
		" && printf %s " + shellQuote(encoded) + " | base64 -d > " + shellQuote(promptPath+".tmp") +
		" && mv -f " + shellQuote(promptPath+".tmp") + " " + shellQuote(promptPath)
	_, err := p.execLeadPrep(ctx, sandbox, cmd)
	return err
}

func (p *Provider) execLeadPrep(
	ctx context.Context,
	sandbox *apiclient.Sandbox,
	command string,
	redactions ...string,
) (toolboxExecuteResponse, error) {
	var out toolboxExecuteResponse
	req := toolboxExecuteRequest{
		Command: command,
		// The toolbox default is 10 seconds -- far too short for a clone --
		// so the timeout is always sent explicitly, derived from the caller's
		// context deadline (the broker's prep budget).
		Timeout: leadPrepExecTimeoutSeconds(ctx),
	}
	err := p.doToolboxWithClient(ctx, p.prepClient(), sandbox, http.MethodPost, "/process/execute", req, &out)
	if err != nil {
		return out, classifyLeadPrepTransportError(err)
	}
	if out.ExitCode == nil {
		return out, fmt.Errorf("daytona lead prep exec returned no exitCode")
	}
	if *out.ExitCode != 0 {
		return out, &leadPrepExecError{
			exitCode: *out.ExitCode,
			message:  prepDiagnostic(out, command, redactions...),
		}
	}
	return out, nil
}

// leadPrepExecTimeoutSeconds converts the context's remaining budget into the
// toolbox exec timeout field, so the sandbox-side command never outlives the
// caller. Floor of 1 second; the provider default when the context is
// unbounded.
func leadPrepExecTimeoutSeconds(ctx context.Context) int {
	deadline, ok := ctx.Deadline()
	if !ok {
		return int(defaultLeadBootPrepTimeout / time.Second)
	}
	remaining := int(time.Until(deadline) / time.Second)
	if remaining < 1 {
		return 1
	}
	return remaining
}

// classifyLeadPrepTransportError preserves error classification (the
// not-found sentinel and the HTTP status) while discarding response bodies,
// which can echo the credential-bearing exec command.
func classifyLeadPrepTransportError(err error) error {
	if isDaytonaNotFound(err) {
		return fmt.Errorf("daytona lead prep exec: %w", placement.ErrSandboxNotFound)
	}
	if status, _, ok := daytonaStatusAndMessage(err); ok && status != 0 {
		return fmt.Errorf("daytona lead prep exec request failed: http status %d", status)
	}
	return fmt.Errorf("daytona lead prep exec request failed")
}

type toolboxExecuteRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type toolboxExecuteResponse struct {
	ExitCode *int   `json:"exitCode"`
	Result   string `json:"result"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Error    string `json:"error"`
}

func (r toolboxExecuteResponse) outputText() string {
	for _, value := range []string{r.Result, r.Stdout} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type leadPrepExecError struct {
	exitCode int
	message  string
}

func (e *leadPrepExecError) Error() string {
	if strings.TrimSpace(e.message) == "" {
		return fmt.Sprintf("daytona lead prep exec exit code %d", e.exitCode)
	}
	return fmt.Sprintf("daytona lead prep exec exit code %d: %s", e.exitCode, e.message)
}

func prepDiagnostic(out toolboxExecuteResponse, command string, redactions ...string) string {
	text := strings.Join([]string{out.Stderr, out.Error, out.Result, out.Stdout}, "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, command, "[redacted command]")
	for _, secret := range redactions {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	text = basicAuthHeaderRe.ReplaceAllString(text, "AUTHORIZATION: basic [redacted]")
	text = xAccessTokenURLRe.ReplaceAllString(text, "x-access-token:***@")
	if len(text) > maxPrepDiagnosticBytes {
		text = text[:maxPrepDiagnosticBytes] + "..."
	}
	return text
}
