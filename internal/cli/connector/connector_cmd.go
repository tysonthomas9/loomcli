// Package connector implements the `loom connector` command tree: the
// operator surface for the step-7 vault lifecycle. It registers per-source
// connectors (inbound secret + outbound credential supplied once over stdin,
// sealed client-side-of-store before any write), manages deny-by-default
// egress grants, rotates secrets with the dual-secret HMAC window, and lists
// the connector-call audit journal.
//
// Secrets NEVER travel on argv (process-listing leak) and are NEVER echoed
// back: the store layer redacts every Get/List/Create/Rotate response
// (domain.Connector.Redacted), and this package only ever prints those
// redacted results.
package connector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	vault "github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

var connectorCmd = &cobra.Command{
	Use:     "connector",
	Short:   "Manage per-source connectors: sealed credentials, egress grants, and the call audit trail",
	GroupID: "workspace",
}

// --- create ---

var (
	createID          string
	createSource      string
	createName        string
	createEndpoint    string
	createSecretStdin bool
	createCredStdin   bool
	createDisabled    bool
	createJSON        bool
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Register a connector; secrets are read from stdin and sealed before the store write",
	Long: "Registers a named connector for one source kind. The inbound webhook secret and the\n" +
		"outbound credential are read from stdin (first and second line respectively when both\n" +
		"flags are set) — never from argv, which leaks through process listings. The outbound\n" +
		"credential is sealed with the connector vault before the store ever sees it,\n" +
		"and the printed result is always redacted.",
	Args: cobra.NoArgs,
	RunE: runCreate,
}

// createParams carries the validated create inputs into the testable core.
type createParams struct {
	connectorID string
	source      domain.ConnectorSourceKind
	displayName string
	endpoint    string
	secretStdin bool
	credStdin   bool
	disabled    bool
	jsonOut     bool
}

func runCreate(cmd *cobra.Command, _ []string) error {
	p := createParams{
		connectorID: strings.TrimSpace(createID),
		source:      domain.ConnectorSourceKind(strings.TrimSpace(createSource)),
		displayName: strings.TrimSpace(createName),
		endpoint:    strings.TrimSpace(createEndpoint),
		secretStdin: createSecretStdin,
		credStdin:   createCredStdin,
		disabled:    createDisabled,
		jsonOut:     createJSON,
	}
	if err := p.validate(); err != nil {
		return err
	}
	return cmdstore.WithActiveConnectorManagement(func(
		ctx context.Context,
		_ *bootstrap.StoreHandle,
		management connectorsmodule.Management,
		ws string,
	) error {
		return createConnector(ctx, management, ws, p, cmd.InOrStdin(), cmd.OutOrStdout())
	})
}

func (p *createParams) validate() error {
	if p.source == "" {
		return fmt.Errorf("--source is required")
	}
	if !p.source.Valid() {
		return fmt.Errorf("--source %q is invalid: must be one of github, slack, datadog, internal", p.source)
	}
	if p.endpoint != "" && !strings.HasPrefix(p.endpoint, "/") {
		return fmt.Errorf("--endpoint-path must start with /")
	}
	return nil
}

// createConnector is the store-facing core: it reads any stdin-supplied
// secrets, seals the outbound credential client-side-of-store (on the direct
// store path the CLI is the control plane), creates the connector, and prints
// the redacted result.
func createConnector(
	ctx context.Context,
	management connectorsmodule.Management,
	ws string,
	p createParams,
	stdin io.Reader,
	out io.Writer,
) error {
	id := p.connectorID
	if id == "" {
		id = string(p.source)
	}
	in := connectorsmodule.CreateConnectorCommand{
		WorkspaceKey:        ws,
		ConnectorID:         id,
		SourceKind:          connectorsmodule.ConnectorSourceKind(p.source),
		DisplayName:         p.displayName,
		InboundEndpointPath: p.endpoint,
	}
	if p.disabled {
		in.Status = connectorsmodule.ConnectorStatusDisabled
	}
	r := bufio.NewReader(stdin)
	if p.secretStdin {
		secret, err := readSecretLine(r, "inbound secret")
		if err != nil {
			return err
		}
		in.InboundSecret = secret
	}
	if p.credStdin {
		cred, err := readSecretLine(r, "outbound credential")
		if err != nil {
			return err
		}
		sealed, err := sealCredential([]byte(cred), ws, id)
		if err != nil {
			return err
		}
		in.OutboundCredentialSealed = sealed
	}
	conn, err := management.CreateConnector(ctx, in)
	if err != nil {
		return fmt.Errorf("create connector: %w", err)
	}
	if p.jsonOut {
		return writeJSON(out, conn)
	}
	_, _ = fmt.Fprintf(out, "Created connector %s (source %s, status %s, inbound-secret=%s, outbound-credential=%s)\n",
		conn.ConnectorID, conn.SourceKind, conn.Status, setOrNone(p.secretStdin), sealedOrNone(p.credStdin))
	return nil
}

// --- list ---

var (
	listSource string
	listStatus string
	listJSON   bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List connectors (always redacted: secrets and sealed credentials never appear)",
	Args:  cobra.NoArgs,
	RunE:  runList,
}

func runList(cmd *cobra.Command, _ []string) error {
	filter := connectorsmodule.ConnectorFilter{
		SourceKind: connectorsmodule.ConnectorSourceKind(strings.TrimSpace(listSource)),
		Status:     connectorsmodule.ConnectorStatus(strings.TrimSpace(listStatus)),
	}
	return cmdstore.WithActiveConnectorManagement(func(
		ctx context.Context,
		_ *bootstrap.StoreHandle,
		management connectorsmodule.Management,
		ws string,
	) error {
		return listConnectors(ctx, management, ws, filter, listJSON, cmd.OutOrStdout())
	})
}

func listConnectors(
	ctx context.Context,
	management connectorsmodule.Management,
	ws string,
	filter connectorsmodule.ConnectorFilter,
	jsonOut bool,
	out io.Writer,
) error {
	conns, err := management.ListConnectors(ctx, connectorsmodule.ListConnectorsQuery{WorkspaceKey: ws, Filter: filter})
	if err != nil {
		return fmt.Errorf("list connectors: %w", err)
	}
	if jsonOut {
		return writeJSON(out, conns)
	}
	renderConnectorsList(out, conns)
	return nil
}

// renderConnectorsList writes the human-readable connector listing. Pure
// helper so tests can exercise it directly.
func renderConnectorsList(w io.Writer, conns []*connectorsmodule.Connector) {
	if len(conns) == 0 {
		_, _ = fmt.Fprintln(w, "No connectors.")
		return
	}
	for _, c := range conns {
		_, _ = fmt.Fprintln(w, formatConnectorRow(c))
	}
}

func formatConnectorRow(c *connectorsmodule.Connector) string {
	row := fmt.Sprintf("%-24s source=%-9s status=%-9s", c.ConnectorID, c.SourceKind, c.Status)
	if c.DisplayName != "" {
		row += fmt.Sprintf(" name=%q", c.DisplayName)
	}
	if c.InboundEndpointPath != "" {
		row += " endpoint=" + c.InboundEndpointPath
	}
	if c.RotatedAt != nil {
		row += " rotated=" + c.RotatedAt.UTC().Format(time.RFC3339)
	}
	return row
}

// --- rotate ---

var (
	rotateSecretStdin bool
	rotateCredStdin   bool
	rotateWindow      time.Duration
	rotateJSON        bool
)

var rotateCmd = &cobra.Command{
	Use:   "rotate <connector-id>",
	Short: "Rotate the inbound secret (dual-secret window) and optionally re-seal the outbound credential",
	Args:  cobra.ExactArgs(1),
	RunE:  runRotate,
}

type rotateParams struct {
	connectorID string
	credStdin   bool
	window      time.Duration
	jsonOut     bool
}

func runRotate(cmd *cobra.Command, args []string) error {
	if !rotateSecretStdin {
		return fmt.Errorf("pass --inbound-secret-stdin and provide the new inbound secret on stdin (secrets never travel on argv)")
	}
	if rotateWindow < 0 {
		return fmt.Errorf("--inbound-window must be non-negative")
	}
	p := rotateParams{
		connectorID: strings.TrimSpace(args[0]),
		credStdin:   rotateCredStdin,
		window:      rotateWindow,
		jsonOut:     rotateJSON,
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		var (
			management connectorsmodule.Management
			err        error
		)
		if p.credStdin {
			sealer, sealErr := newConnectorVault()
			if sealErr != nil {
				return fmt.Errorf("connector vault: %w", sealErr)
			}
			management, err = cmdstore.ConnectorManagementWithSecrets(h, sealer)
		} else {
			management, err = cmdstore.ConnectorManagement(h)
		}
		if err != nil {
			return err
		}
		return rotateConnector(ctx, management, ws, p, cmd.InOrStdin(), cmd.OutOrStdout())
	})
}

// rotateConnector drives the CV13 rotation ceremony (connector.Rotate): the
// orchestration computes the dual-secret window (zero window applies the 15m
// default, explicit windows are capped at domain.MaxConnectorSecretOverlap),
// seals any replacement outbound credential through the vault seam before the
// single store write, and journals a rotation record in the connector-call
// audit trail.
func rotateConnector(
	ctx context.Context,
	management connectorsmodule.Management,
	ws string,
	p rotateParams,
	stdin io.Reader,
	out io.Writer,
) error {
	r := bufio.NewReader(stdin)
	secret, err := readSecretLine(r, "new inbound secret")
	if err != nil {
		return err
	}
	req := connectorsmodule.RotateConnectorCommand{
		WorkspaceKey:     ws,
		ConnectorID:      p.connectorID,
		NewInboundSecret: secret,
		InboundWindow:    p.window,
	}
	if p.credStdin {
		cred, err := readSecretLine(r, "new outbound credential")
		if err != nil {
			return err
		}
		req.NewCredential = []byte(cred)
	}
	conn, err := management.RotateConnector(ctx, req)
	if err != nil {
		return fmt.Errorf("rotate connector secrets: %w", err)
	}
	if p.jsonOut {
		return writeJSON(out, conn)
	}
	msg := fmt.Sprintf("Rotated connector %s secrets", conn.ConnectorID)
	if conn.PreviousSecretValidUntil != nil {
		msg += fmt.Sprintf(" (previous inbound secret valid until %s)", conn.PreviousSecretValidUntil.UTC().Format(time.RFC3339))
	}
	_, _ = fmt.Fprintln(out, msg)
	return nil
}

// --- grant ---

var grantCmd = &cobra.Command{
	Use:   "grant",
	Short: "Manage deny-by-default egress grants (binding × action × resource)",
}

var (
	grantCreateID        string
	grantCreateConnector string
	grantCreateBinding   string
	grantCreateAction    string
	grantCreateResource  string
	grantCreateJSON      bool

	grantListBinding   string
	grantListConnector string
	grantListJSON      bool
)

var grantCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Grant one {action, resource pattern} to a binding against a connector",
	Args:  cobra.NoArgs,
	RunE:  runGrantCreate,
}

var grantRevokeCmd = &cobra.Command{
	Use:   "revoke <grant-id>",
	Short: "Revoke a grant; revoked grants never authorize egress again",
	Args:  cobra.ExactArgs(1),
	RunE:  runGrantRevoke,
}

var grantListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active grants for a binding or a connector (revoked grants are filtered)",
	Args:  cobra.NoArgs,
	RunE:  runGrantList,
}

func newGrantCreateInput(ws string) (connectorsmodule.CreateGrantCommand, error) {
	in := connectorsmodule.CreateGrantCommand{
		WorkspaceKey:    ws,
		GrantID:         strings.TrimSpace(grantCreateID),
		ConnectorID:     strings.TrimSpace(grantCreateConnector),
		BindingID:       strings.TrimSpace(grantCreateBinding),
		Action:          strings.TrimSpace(grantCreateAction),
		ResourcePattern: strings.TrimSpace(grantCreateResource),
	}
	if in.ConnectorID == "" || in.BindingID == "" || in.Action == "" || in.ResourcePattern == "" {
		return in, fmt.Errorf("--connector, --binding, --action and --resource are all required")
	}
	if err := domain.ValidateConnectorAction(in.Action); err != nil {
		return in, fmt.Errorf("--action: %w", err)
	}
	if in.GrantID == "" {
		in.GrantID = defaultGrantID(in.BindingID, in.Action)
	}
	return in, nil
}

func runGrantCreate(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithActiveConnectorManagement(func(
		ctx context.Context,
		_ *bootstrap.StoreHandle,
		management connectorsmodule.Management,
		ws string,
	) error {
		in, err := newGrantCreateInput(ws)
		if err != nil {
			return err
		}
		return createGrant(ctx, management, in, grantCreateJSON, cmd.OutOrStdout())
	})
}

func createGrant(
	ctx context.Context,
	management connectorsmodule.Management,
	in connectorsmodule.CreateGrantCommand,
	jsonOut bool,
	out io.Writer,
) error {
	grant, err := management.CreateGrant(ctx, in)
	if err != nil {
		return fmt.Errorf("create connector grant: %w", err)
	}
	if jsonOut {
		return writeJSON(out, grant)
	}
	_, _ = fmt.Fprintf(out, "Created grant %s (connector %s, binding %s, action %s, resource %s)\n",
		grant.GrantID, grant.ConnectorID, grant.BindingID, grant.Action, grant.ResourcePattern)
	return nil
}

func runGrantRevoke(cmd *cobra.Command, args []string) error {
	return cmdstore.WithActiveConnectorManagement(func(
		ctx context.Context,
		_ *bootstrap.StoreHandle,
		management connectorsmodule.Management,
		ws string,
	) error {
		return revokeGrant(ctx, management, ws, strings.TrimSpace(args[0]), cmd.OutOrStdout())
	})
}

func revokeGrant(ctx context.Context, management connectorsmodule.Management, ws, grantID string, out io.Writer) error {
	if err := management.RevokeGrant(ctx, connectorsmodule.RevokeGrantCommand{WorkspaceKey: ws, GrantID: grantID}); err != nil {
		return fmt.Errorf("revoke connector grant: %w", err)
	}
	_, _ = fmt.Fprintf(out, "Revoked grant %s\n", grantID)
	return nil
}

func runGrantList(cmd *cobra.Command, _ []string) error {
	binding := strings.TrimSpace(grantListBinding)
	connectorID := strings.TrimSpace(grantListConnector)
	return cmdstore.WithActiveConnectorManagement(func(
		ctx context.Context,
		_ *bootstrap.StoreHandle,
		management connectorsmodule.Management,
		ws string,
	) error {
		return listGrants(ctx, management, ws, binding, connectorID, grantListJSON, cmd.OutOrStdout())
	})
}

func listGrants(
	ctx context.Context,
	management connectorsmodule.Management,
	ws,
	bindingID,
	connectorID string,
	jsonOut bool,
	out io.Writer,
) error {
	switch {
	case bindingID != "" && connectorID != "":
		return fmt.Errorf("pass exactly one of --binding or --connector, not both")
	case bindingID == "" && connectorID == "":
		return fmt.Errorf("one of --binding or --connector is required")
	}
	grants, err := management.ListGrants(ctx, connectorsmodule.ListGrantsQuery{
		WorkspaceKey: ws, BindingID: bindingID, ConnectorID: connectorID,
	})
	if err != nil {
		return fmt.Errorf("list connector grants: %w", err)
	}
	if jsonOut {
		return writeJSON(out, grants)
	}
	renderGrantsList(out, grants)
	return nil
}

// renderGrantsList writes the human-readable grant listing. Pure helper so
// tests can exercise it directly.
func renderGrantsList(w io.Writer, grants []*connectorsmodule.ConnectorGrant) {
	if len(grants) == 0 {
		_, _ = fmt.Fprintln(w, "No active grants (egress is deny-by-default).")
		return
	}
	for _, g := range grants {
		_, _ = fmt.Fprintf(w, "%-28s connector=%-16s binding=%-24s action=%-24s resource=%s\n",
			g.GrantID, g.ConnectorID, g.BindingID, g.Action, g.ResourcePattern)
	}
}

// --- audit ---

var (
	auditRun      string
	auditBinding  string
	auditDecision string
	auditLimit    int
	auditJSON     bool
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "List the connector-call audit journal for a run or a binding",
	Args:  cobra.NoArgs,
	RunE:  runAudit,
}

type auditParams struct {
	runID     string
	bindingID string
	decision  connectorsmodule.ConnectorCallDecision
	limit     int
	jsonOut   bool
}

func (p *auditParams) validate() error {
	if (p.runID == "") == (p.bindingID == "") {
		return fmt.Errorf("pass exactly one of --run or --binding")
	}
	if p.decision != "" && !p.decision.Valid() {
		return fmt.Errorf("--decision %q is invalid: must be one of granted, denied, stale_subject, precondition_required, upstream_error", p.decision)
	}
	return nil
}

func runAudit(cmd *cobra.Command, _ []string) error {
	p := auditParams{
		runID:     strings.TrimSpace(auditRun),
		bindingID: strings.TrimSpace(auditBinding),
		decision:  connectorsmodule.ConnectorCallDecision(strings.TrimSpace(auditDecision)),
		limit:     auditLimit,
		jsonOut:   auditJSON,
	}
	if err := p.validate(); err != nil {
		return err
	}
	return cmdstore.WithActiveConnectorManagement(func(
		ctx context.Context,
		_ *bootstrap.StoreHandle,
		management connectorsmodule.Management,
		ws string,
	) error {
		return listAudit(ctx, management, ws, p, cmd.OutOrStdout())
	})
}

func listAudit(ctx context.Context, management connectorsmodule.Management, ws string, p auditParams, out io.Writer) error {
	records, err := management.ListCalls(ctx, connectorsmodule.ListCallsQuery{
		WorkspaceKey: ws, RunID: p.runID, BindingID: p.bindingID,
		Filter: connectorsmodule.ConnectorCallFilter{Decision: p.decision, Limit: p.limit},
	})
	if err != nil {
		return fmt.Errorf("list connector calls: %w", err)
	}
	if p.jsonOut {
		return writeJSON(out, records)
	}
	renderCallsList(out, records)
	return nil
}

// renderCallsList writes the human-readable audit listing. Pure helper so
// tests can exercise it directly.
func renderCallsList(w io.Writer, records []*connectorsmodule.ConnectorCallRecord) {
	if len(records) == 0 {
		_, _ = fmt.Fprintln(w, "No connector calls.")
		return
	}
	for _, r := range records {
		_, _ = fmt.Fprintln(w, formatCallRow(r))
	}
}

func formatCallRow(r *connectorsmodule.ConnectorCallRecord) string {
	row := fmt.Sprintf("%-40s decision=%-21s action=%-24s binding=%s", r.CallID, r.Decision, r.Action, r.BindingID)
	if r.Resource != "" {
		row += " resource=" + r.Resource
	}
	if r.UpstreamStatus != 0 {
		row += fmt.Sprintf(" upstream=%d", r.UpstreamStatus)
	}
	if r.ErrorClass != "" {
		row += " error=" + r.ErrorClass
	}
	return row
}

// --- shared helpers ---

// readSecretLine reads one newline-terminated secret value from r. Secrets
// arrive over stdin only — never argv, which any user can read from the
// process listing.
func readSecretLine(r *bufio.Reader, what string) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read %s from stdin: %w", what, err)
	}
	secret := strings.TrimRight(line, "\r\n")
	if secret == "" {
		return "", fmt.Errorf("%s from stdin is empty", what)
	}
	return secret, nil
}

// sealCredential seals a plaintext outbound credential client-side-of-store:
// on the direct store path the CLI process is the control plane. The CLI and
// serve share bootstrap.LoomDir as the fallback key directory, and stores only
// ever see ciphertext.
func sealCredential(plaintext []byte, ws, connectorID string) ([]byte, error) {
	sealer, err := newConnectorVault()
	if err != nil {
		return nil, fmt.Errorf("connector vault: %w", err)
	}
	sealed, err := sealer.Seal(plaintext, vault.CredentialAAD(ws, connectorID))
	if err != nil {
		return nil, fmt.Errorf("seal outbound credential: %w", err)
	}
	return sealed, nil
}

func newConnectorVault() (*vault.Vault, error) {
	return vault.NewVaultFromEnvOrKeyFile(bootstrap.LoomDir())
}

// writeJSON encodes v as indented JSON to w. Unlike cmdstore.WriteJSON it
// takes the writer so tests can capture command output and assert secrets
// never reach it.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}

func setOrNone(set bool) string {
	if set {
		return "set"
	}
	return "none"
}

func sealedOrNone(set bool) string {
	if set {
		return "sealed"
	}
	return "none"
}

func defaultGrantID(bindingID, action string) string {
	return "grant-" + bindingID + "-" + strings.ReplaceAll(action, ".", "-")
}

func registerConnectorFlags() {
	createCmd.Flags().StringVar(&createID, "id", "", "connector id (default: the source kind, e.g. \"github\")")
	createCmd.Flags().StringVar(&createSource, "source", "", "source kind: github|slack|datadog|internal")
	createCmd.Flags().StringVar(&createName, "name", "", "connector display name")
	createCmd.Flags().StringVar(&createEndpoint, "endpoint-path", "", "workspace-relative inbound webhook path (must start with /)")
	createCmd.Flags().BoolVar(&createSecretStdin, "inbound-secret-stdin", false, "read the inbound webhook secret from stdin (first line)")
	createCmd.Flags().BoolVar(&createCredStdin, "credential-stdin", false, "read the outbound credential from stdin (line after the inbound secret) and seal it")
	createCmd.Flags().BoolVar(&createDisabled, "disabled", false, "create the connector disabled")
	createCmd.Flags().BoolVar(&createJSON, "json", false, "JSON output (always redacted)")

	listCmd.Flags().StringVar(&listSource, "source-kind", "", "filter by source kind")
	listCmd.Flags().StringVar(&listStatus, "status", "", "filter by status: active|disabled")
	listCmd.Flags().BoolVar(&listJSON, "json", false, "JSON output (always redacted)")

	rotateCmd.Flags().BoolVar(&rotateSecretStdin, "inbound-secret-stdin", false, "read the new inbound secret from stdin (first line; required)")
	rotateCmd.Flags().BoolVar(&rotateCredStdin, "credential-stdin", false, "read a new outbound credential from stdin (line after the secret) and re-seal it")
	rotateCmd.Flags().DurationVar(&rotateWindow, "inbound-window", 0, "how long the previous inbound secret keeps verifying (default 15m, capped at 24h)")
	rotateCmd.Flags().BoolVar(&rotateJSON, "json", false, "JSON output (always redacted)")
}

func registerGrantAndAuditFlags() {
	grantCreateCmd.Flags().StringVar(&grantCreateID, "grant-id", "", "grant id (default: derived from binding and action)")
	grantCreateCmd.Flags().StringVar(&grantCreateConnector, "connector", "", "connector id the grant authorizes egress through")
	grantCreateCmd.Flags().StringVar(&grantCreateBinding, "binding", "", "trigger binding id the grant is scoped to")
	grantCreateCmd.Flags().StringVar(&grantCreateAction, "action", "", "dotted action verb, e.g. github.merge")
	grantCreateCmd.Flags().StringVar(&grantCreateResource, "resource", "", "resource pattern, e.g. repo:octocat/hello")
	grantCreateCmd.Flags().BoolVar(&grantCreateJSON, "json", false, "JSON output")

	grantListCmd.Flags().StringVar(&grantListBinding, "binding", "", "list active grants for this binding id")
	grantListCmd.Flags().StringVar(&grantListConnector, "connector", "", "list active grants for this connector id")
	grantListCmd.Flags().BoolVar(&grantListJSON, "json", false, "JSON output")

	auditCmd.Flags().StringVar(&auditRun, "run", "", "list calls for this driver run id")
	auditCmd.Flags().StringVar(&auditBinding, "binding", "", "list calls for this binding id")
	auditCmd.Flags().StringVar(&auditDecision, "decision", "", "filter by decision: granted|denied|stale_subject|precondition_required|upstream_error")
	auditCmd.Flags().IntVar(&auditLimit, "limit", 0, "max results")
	auditCmd.Flags().BoolVar(&auditJSON, "json", false, "JSON output")
}

func init() {
	registerConnectorFlags()
	registerGrantAndAuditFlags()
	grantCmd.AddCommand(grantCreateCmd, grantRevokeCmd, grantListCmd)
	connectorCmd.AddCommand(createCmd, listCmd, rotateCmd, grantCmd, auditCmd)
	cli.RegisterCommand(connectorCmd)
}
