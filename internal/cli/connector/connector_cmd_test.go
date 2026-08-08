package connector

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorscatalog"
	vault "github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	testWS            = "ws-1"
	testInboundSecret = "whsec-INBOUND-secret-1"
	testCredential    = "ghp-OUTBOUND-token-1"
	testRotatedSecret = "whsec-ROTATED-secret-2"
)

func testConnectorManagement(t *testing.T, st store.Store) connectorsmodule.Management {
	t.Helper()
	adapter, err := connectorscatalog.New(st.Connectors(), st.ConnectorGrants(), st.ConnectorCalls())
	if err != nil {
		t.Fatalf("compose connector adapter: %v", err)
	}
	management, err := connectorsmodule.NewManagement(adapter)
	if err != nil {
		t.Fatalf("compose connector management: %v", err)
	}
	return management
}

func testConnectorSecretManagement(t *testing.T, st store.Store) connectorsmodule.Management {
	t.Helper()
	adapter, err := connectorscatalog.New(st.Connectors(), st.ConnectorGrants(), st.ConnectorCalls())
	if err != nil {
		t.Fatalf("compose connector adapter: %v", err)
	}
	sealer, err := newConnectorVault()
	if err != nil {
		t.Fatalf("compose connector vault: %v", err)
	}
	management, err := connectorsmodule.NewManagementWithSecrets(adapter, sealer, time.Now)
	if err != nil {
		t.Fatalf("compose connector secret management: %v", err)
	}
	return management
}

// setVaultKey installs a valid 32-byte vault key for the test's duration and
// returns the raw key so assertions can unseal what the CLI sealed.
func setVaultKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	t.Setenv(vault.VaultKeyEnvVar, base64.StdEncoding.EncodeToString(key))
	return key
}

// assertNoSecrets fails if any secret material appears in the command output.
func assertNoSecrets(t *testing.T, out string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(out, s) {
			t.Fatalf("output leaked secret %q:\n%s", s, out)
		}
	}
}

func TestCreateThenList_RedactsAndNeverEchoesSecrets(t *testing.T) {
	key := setVaultKey(t)
	st := memstore.New()
	ctx := context.Background()

	tests := []struct {
		name    string
		jsonOut bool
		want    string // substring of human/JSON output
	}{
		{name: "human output", jsonOut: false, want: "Created connector gh-main (source github, status active, inbound-secret=set, outbound-credential=sealed)"},
		{name: "json output", jsonOut: true, want: `"connector_id": "gh-main"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := memstore.New()
			var out bytes.Buffer
			stdin := strings.NewReader(testInboundSecret + "\n" + testCredential + "\n")
			err := createConnector(ctx, testConnectorManagement(t, st), testWS, createParams{
				connectorID: "gh-main",
				source:      domain.ConnectorSourceGitHub,
				displayName: "GitHub main",
				endpoint:    "/hooks/github",
				secretStdin: true,
				credStdin:   true,
				jsonOut:     tt.jsonOut,
			}, stdin, &out)
			if err != nil {
				t.Fatalf("createConnector: %v", err)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("output missing %q:\n%s", tt.want, out.String())
			}
			assertNoSecrets(t, out.String(), testInboundSecret, testCredential)
		})
	}

	// Create once more on the shared store for the list + resolve assertions.
	var out bytes.Buffer
	stdin := strings.NewReader(testInboundSecret + "\n" + testCredential + "\n")
	if err := createConnector(ctx, testConnectorManagement(t, st), testWS, createParams{
		source:      domain.ConnectorSourceGitHub, // connectorID defaults to "github"
		secretStdin: true,
		credStdin:   true,
	}, stdin, &out); err != nil {
		t.Fatalf("createConnector: %v", err)
	}

	out.Reset()
	if err := listConnectors(ctx, testConnectorManagement(t, st), testWS, connectorsmodule.ConnectorFilter{}, true, &out); err != nil {
		t.Fatalf("listConnectors json: %v", err)
	}
	if !strings.Contains(out.String(), `"connector_id": "github"`) {
		t.Fatalf("list output missing connector:\n%s", out.String())
	}
	assertNoSecrets(t, out.String(), testInboundSecret, testCredential)

	out.Reset()
	if err := listConnectors(ctx, testConnectorManagement(t, st), testWS, connectorsmodule.ConnectorFilter{}, false, &out); err != nil {
		t.Fatalf("listConnectors human: %v", err)
	}
	if !strings.Contains(out.String(), "github") || !strings.Contains(out.String(), "status=active") {
		t.Fatalf("human list output unexpected:\n%s", out.String())
	}
	assertNoSecrets(t, out.String(), testInboundSecret, testCredential)

	// The secrets reached the store intact: the privileged resolve paths
	// return the inbound secret, and the sealed credential unseals back to
	// the plaintext under the same key + AAD the CLI sealed with.
	secrets, err := st.Connectors().ResolveInboundSecret(ctx, testWS, "github")
	if err != nil {
		t.Fatalf("ResolveInboundSecret: %v", err)
	}
	if secrets.Current != testInboundSecret {
		t.Fatalf("stored inbound secret = %q, want %q", secrets.Current, testInboundSecret)
	}
	sealed, err := st.Connectors().ResolveOutboundCredentialSealed(ctx, testWS, "github")
	if err != nil {
		t.Fatalf("ResolveOutboundCredentialSealed: %v", err)
	}
	v, err := vault.NewVault(key)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	plain, err := v.Unseal(sealed, vault.CredentialAAD(testWS, "github"))
	if err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	if string(plain) != testCredential {
		t.Fatalf("unsealed credential = %q, want %q", plain, testCredential)
	}
}

func TestCreateConnectorUsesServeVaultFallback(t *testing.T) {
	t.Setenv(vault.VaultKeyEnvVar, "")
	dataDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dataDir)
	st := memstore.New()
	var out bytes.Buffer
	if err := createConnector(context.Background(), testConnectorManagement(t, st), testWS, createParams{
		connectorID: "github",
		source:      domain.ConnectorSourceGitHub,
		credStdin:   true,
	}, strings.NewReader(testCredential+"\n"), &out); err != nil {
		t.Fatalf("createConnector: %v", err)
	}

	sealed, err := st.Connectors().ResolveOutboundCredentialSealed(context.Background(), testWS, "github")
	if err != nil {
		t.Fatalf("ResolveOutboundCredentialSealed: %v", err)
	}
	serveVault, err := vault.NewVaultFromEnvOrKeyFile(dataDir)
	if err != nil {
		t.Fatalf("serve-style vault: %v", err)
	}
	plain, err := serveVault.Unseal(sealed, vault.CredentialAAD(testWS, "github"))
	if err != nil {
		t.Fatalf("serve-style vault could not unseal CLI credential: %v", err)
	}
	if string(plain) != testCredential {
		t.Fatalf("unsealed credential = %q, want %q", plain, testCredential)
	}
}

func TestCreateConnector_ErrorPaths(t *testing.T) {
	setVaultKey(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		params  createParams
		stdin   string
		noKey   bool
		wantErr error  // matched with errors.Is when non-nil
		wantMsg string // substring otherwise
	}{
		{
			name:    "empty inbound secret on stdin",
			params:  createParams{source: domain.ConnectorSourceGitHub, secretStdin: true},
			stdin:   "\n",
			wantMsg: "inbound secret from stdin is empty",
		},
		{
			name:    "missing credential line",
			params:  createParams{source: domain.ConnectorSourceGitHub, secretStdin: true, credStdin: true},
			stdin:   testInboundSecret + "\n",
			wantMsg: "outbound credential from stdin is empty",
		},
		{
			name:    "vault source missing fails closed before any store write",
			params:  createParams{source: domain.ConnectorSourceGitHub, credStdin: true},
			stdin:   testCredential + "\n",
			noKey:   true,
			wantMsg: "vault key",
		},
		{
			name:    "duplicate connector",
			params:  createParams{connectorID: "dup", source: domain.ConnectorSourceSlack},
			wantErr: domain.ErrConnectorExists,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.noKey {
				// Since #152 LoomDir() always yields a writable per-process
				// temp dir under `go test`, so "no vault source" can't be
				// staged by clearing env alone: the key-file fallback would
				// succeed. Point LOOM_CONFIG_DIR below a regular file so the
				// key-file path cannot be created either.
				t.Setenv(vault.VaultKeyEnvVar, "")
				blocker := filepath.Join(t.TempDir(), "blocker")
				if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
					t.Fatalf("write blocker: %v", err)
				}
				t.Setenv("LOOM_CONFIG_DIR", filepath.Join(blocker, "loom"))
			}
			st := memstore.New()
			if tt.params.connectorID == "dup" {
				if _, err := st.Connectors().Create(ctx, store.ConnectorCreate{
					WorkspaceKey: testWS, ConnectorID: "dup", SourceKind: domain.ConnectorSourceSlack,
				}); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			var out bytes.Buffer
			err := createConnector(ctx, testConnectorManagement(t, st), testWS, tt.params, strings.NewReader(tt.stdin), &out)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("createConnector error = %v, want errors.Is %v", err, tt.wantErr)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("createConnector error = %v, want containing %q", err, tt.wantMsg)
			}
			// Failed creates must not leave a connector behind (seal/stdin
			// errors happen before the store write).
			if tt.params.connectorID == "" {
				if _, err := st.Connectors().Get(ctx, testWS, string(tt.params.source)); !errors.Is(err, domain.ErrConnectorNotFound) {
					t.Fatalf("Get after failed create = %v, want ErrConnectorNotFound", err)
				}
			}
			assertNoSecrets(t, out.String(), testInboundSecret, testCredential)
		})
	}
}

func TestCreateParamsValidate(t *testing.T) {
	tests := []struct {
		name    string
		params  createParams
		wantErr string // empty means valid
	}{
		{name: "valid", params: createParams{source: domain.ConnectorSourceGitHub}},
		{name: "missing source", params: createParams{}, wantErr: "--source is required"},
		{name: "unknown source", params: createParams{source: "jira"}, wantErr: `--source "jira" is invalid`},
		{
			name:    "endpoint without slash",
			params:  createParams{source: domain.ConnectorSourceGitHub, endpoint: "hooks"},
			wantErr: "--endpoint-path must start with /",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRotateConnector_SetsPreviousSecretWindow(t *testing.T) {
	key := setVaultKey(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		window    time.Duration
		credStdin bool
		stdin     string
		wantLow   time.Duration // window bounds on PreviousValidUntil - now
		wantHigh  time.Duration
	}{
		{
			name:     "default window is 15m",
			stdin:    testRotatedSecret + "\n",
			wantLow:  14 * time.Minute,
			wantHigh: 16 * time.Minute,
		},
		{
			name:     "explicit window honored",
			window:   30 * time.Minute,
			stdin:    testRotatedSecret + "\n",
			wantLow:  29 * time.Minute,
			wantHigh: 31 * time.Minute,
		},
		{
			name:     "window capped at 24h",
			window:   48 * time.Hour,
			stdin:    testRotatedSecret + "\n",
			wantLow:  23 * time.Hour,
			wantHigh: 24*time.Hour + time.Minute,
		},
		{
			name:      "credential re-sealed alongside",
			credStdin: true,
			stdin:     testRotatedSecret + "\nnew-OUTBOUND-token-2\n",
			wantLow:   14 * time.Minute,
			wantHigh:  16 * time.Minute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := memstore.New()
			if _, err := st.Connectors().Create(ctx, store.ConnectorCreate{
				WorkspaceKey:  testWS,
				ConnectorID:   "gh-main",
				SourceKind:    domain.ConnectorSourceGitHub,
				InboundSecret: testInboundSecret,
			}); err != nil {
				t.Fatalf("seed connector: %v", err)
			}
			var out bytes.Buffer
			management := testConnectorManagement(t, st)
			if tt.credStdin {
				management = testConnectorSecretManagement(t, st)
			}
			err := rotateConnector(ctx, management, testWS, rotateParams{
				connectorID: "gh-main",
				credStdin:   tt.credStdin,
				window:      tt.window,
			}, strings.NewReader(tt.stdin), &out)
			if err != nil {
				t.Fatalf("rotateConnector: %v", err)
			}
			if !strings.Contains(out.String(), "Rotated connector gh-main secrets (previous inbound secret valid until ") {
				t.Fatalf("rotate output unexpected:\n%s", out.String())
			}
			assertNoSecrets(t, out.String(), testInboundSecret, testRotatedSecret, "new-OUTBOUND-token-2")

			secrets, err := st.Connectors().ResolveInboundSecret(ctx, testWS, "gh-main")
			if err != nil {
				t.Fatalf("ResolveInboundSecret: %v", err)
			}
			if secrets.Current != testRotatedSecret || secrets.Previous != testInboundSecret {
				t.Fatalf("secrets after rotate = %+v, want current=rotated previous=original", secrets)
			}
			window := time.Until(secrets.PreviousValidUntil)
			if window < tt.wantLow || window > tt.wantHigh {
				t.Fatalf("previous-secret window = %v, want within [%v, %v]", window, tt.wantLow, tt.wantHigh)
			}

			if tt.credStdin {
				sealed, err := st.Connectors().ResolveOutboundCredentialSealed(ctx, testWS, "gh-main")
				if err != nil {
					t.Fatalf("ResolveOutboundCredentialSealed: %v", err)
				}
				v, err := vault.NewVault(key)
				if err != nil {
					t.Fatalf("NewVault: %v", err)
				}
				plain, err := v.Unseal(sealed, vault.CredentialAAD(testWS, "gh-main"))
				if err != nil {
					t.Fatalf("Unseal re-sealed credential: %v", err)
				}
				if string(plain) != "new-OUTBOUND-token-2" {
					t.Fatalf("re-sealed credential = %q", plain)
				}
			}
		})
	}
}

func TestRotateConnector_JSONOutputRedacted(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Connectors().Create(ctx, store.ConnectorCreate{
		WorkspaceKey:  testWS,
		ConnectorID:   "gh-main",
		SourceKind:    domain.ConnectorSourceGitHub,
		InboundSecret: testInboundSecret,
	}); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	var out bytes.Buffer
	err := rotateConnector(ctx, testConnectorManagement(t, st), testWS, rotateParams{connectorID: "gh-main", jsonOut: true},
		strings.NewReader(testRotatedSecret+"\n"), &out)
	if err != nil {
		t.Fatalf("rotateConnector: %v", err)
	}
	if !strings.Contains(out.String(), `"rotated_at"`) {
		t.Fatalf("JSON rotate output missing rotated_at:\n%s", out.String())
	}
	assertNoSecrets(t, out.String(), testInboundSecret, testRotatedSecret)
}

func TestRotateConnector_NotFound(t *testing.T) {
	var out bytes.Buffer
	st := memstore.New()
	err := rotateConnector(context.Background(), testConnectorManagement(t, st), testWS, rotateParams{connectorID: "nope"},
		strings.NewReader(testRotatedSecret+"\n"), &out)
	if !errors.Is(err, domain.ErrConnectorNotFound) {
		t.Fatalf("rotateConnector = %v, want ErrConnectorNotFound", err)
	}
}

func TestGrantRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	in, ok := grantInputForTest(t, "ws-1")
	if !ok {
		t.Fatal("grant input")
	}

	var out bytes.Buffer
	management := testConnectorManagement(t, st)
	if err := createGrant(ctx, management, in, false, &out); err != nil {
		t.Fatalf("createGrant: %v", err)
	}
	want := "Created grant grant-binding-pr-github-merge (connector gh-main, binding binding-pr, action github.merge, resource repo:octocat/hello)\n"
	if out.String() != want {
		t.Fatalf("createGrant output = %q, want %q", out.String(), want)
	}

	// Listing by binding and by connector both show the active grant.
	for _, sel := range []struct{ binding, connector string }{{binding: "binding-pr"}, {connector: "gh-main"}} {
		out.Reset()
		if err := listGrants(ctx, management, testWS, sel.binding, sel.connector, false, &out); err != nil {
			t.Fatalf("listGrants(%+v): %v", sel, err)
		}
		if !strings.Contains(out.String(), "grant-binding-pr-github-merge") || !strings.Contains(out.String(), "action=github.merge") {
			t.Fatalf("listGrants(%+v) output unexpected:\n%s", sel, out.String())
		}
	}

	// Duplicate create fails with the store sentinel.
	if err := createGrant(ctx, management, in, false, &out); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate createGrant = %v, want ErrAlreadyExists", err)
	}

	out.Reset()
	if err := revokeGrant(ctx, management, testWS, "grant-binding-pr-github-merge", &out); err != nil {
		t.Fatalf("revokeGrant: %v", err)
	}
	if out.String() != "Revoked grant grant-binding-pr-github-merge\n" {
		t.Fatalf("revokeGrant output = %q", out.String())
	}

	// Revoked grants disappear from listings (deny-by-default message shown).
	out.Reset()
	if err := listGrants(ctx, management, testWS, "binding-pr", "", false, &out); err != nil {
		t.Fatalf("listGrants after revoke: %v", err)
	}
	if !strings.Contains(out.String(), "No active grants (egress is deny-by-default).") {
		t.Fatalf("listGrants after revoke = %q", out.String())
	}

	// Double revoke surfaces the ErrGrantRevoked sentinel.
	if err := revokeGrant(ctx, management, testWS, "grant-binding-pr-github-merge", &out); !errors.Is(err, domain.ErrGrantRevoked) {
		t.Fatalf("second revokeGrant = %v, want ErrGrantRevoked", err)
	}
}

// grantInputForTest builds the grant create input through the same flag-state
// path runGrantCreate uses, exercising defaulting + validation.
func grantInputForTest(t *testing.T, ws string) (connectorsmodule.CreateGrantCommand, bool) {
	t.Helper()
	saved := []*string{&grantCreateID, &grantCreateConnector, &grantCreateBinding, &grantCreateAction, &grantCreateResource}
	savedVals := make([]string, len(saved))
	for i, p := range saved {
		savedVals[i] = *p
	}
	t.Cleanup(func() {
		for i, p := range saved {
			*p = savedVals[i]
		}
	})
	grantCreateID = ""
	grantCreateConnector = "gh-main"
	grantCreateBinding = "binding-pr"
	grantCreateAction = "github.merge"
	grantCreateResource = "repo:octocat/hello"
	in, err := newGrantCreateInput(ws)
	if err != nil {
		t.Fatalf("newGrantCreateInput: %v", err)
		return in, false
	}
	return in, true
}

func TestNewGrantCreateInput_Validation(t *testing.T) {
	tests := []struct {
		name                                  string
		id, connector, binding, action, resrc string
		wantID                                string
		wantErr                               string
	}{
		{
			name: "defaults grant id", connector: "gh", binding: "b1", action: "github.merge", resrc: "repo:o/r",
			wantID: "grant-b1-github-merge",
		},
		{
			name: "explicit grant id kept", id: "g-custom", connector: "gh", binding: "b1", action: "github.merge", resrc: "repo:o/r",
			wantID: "g-custom",
		},
		{
			name: "missing required flags", connector: "gh", binding: "", action: "github.merge", resrc: "repo:o/r",
			wantErr: "--connector, --binding, --action and --resource are all required",
		},
		{
			name: "invalid action", connector: "gh", binding: "b1", action: "merge", resrc: "repo:o/r",
			wantErr: "--action",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saved := []struct {
				p   *string
				val string
			}{
				{&grantCreateID, tt.id}, {&grantCreateConnector, tt.connector}, {&grantCreateBinding, tt.binding},
				{&grantCreateAction, tt.action}, {&grantCreateResource, tt.resrc},
			}
			for _, s := range saved {
				old := *s.p
				*s.p = s.val
				t.Cleanup(func() { *s.p = old })
			}
			in, err := newGrantCreateInput(testWS)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("newGrantCreateInput = %v, want error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("newGrantCreateInput: %v", err)
			}
			if in.GrantID != tt.wantID {
				t.Fatalf("GrantID = %q, want %q", in.GrantID, tt.wantID)
			}
			if errors.Is(domain.ValidateConnectorAction(in.Action), domain.ErrInvalid) {
				t.Fatalf("action %q invalid after parse", in.Action)
			}
		})
	}
}

func TestListGrants_SelectorValidation(t *testing.T) {
	tests := []struct {
		name               string
		binding, connector string
		wantErr            string
	}{
		{name: "both selectors", binding: "b1", connector: "c1", wantErr: "not both"},
		{name: "no selector", wantErr: "one of --binding or --connector is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			st := memstore.New()
			err := listGrants(context.Background(), testConnectorManagement(t, st), testWS, tt.binding, tt.connector, false, &out)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("listGrants = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// seedAuditJournal appends one granted and one denied call for run-1 on
// binding-pr, plus a granted call for run-2 on binding-other.
func seedAuditJournal(t *testing.T, st store.Store) {
	t.Helper()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	recs := []*domain.ConnectorCallRecord{
		{
			RunID: "run-1", BindingID: "binding-pr", Action: "github.merge", Seq: 1,
			Decision: domain.ConnectorCallGranted, Resource: "repo:octocat/hello", UpstreamStatus: 200,
			OccurredAt: now,
		},
		{
			RunID: "run-1", BindingID: "binding-pr", Action: "github.comment", Seq: 2,
			Decision: domain.ConnectorCallDenied, Resource: "repo:octocat/hello",
			SanitizedSummary: "no grant for github.comment", OccurredAt: now.Add(time.Second),
		},
		{
			RunID: "run-2", BindingID: "binding-other", Action: "slack.chat.post_message", Seq: 1,
			Decision: domain.ConnectorCallGranted, OccurredAt: now.Add(2 * time.Second),
		},
	}
	for _, r := range recs {
		r.WorkspaceKey = testWS
		r.ConnectorID = "gh-main"
		r.SourceKind = domain.ConnectorSourceGitHub
		r.CallID = domain.ConnectorCallID(r.RunID, r.Action, r.Seq)
		if err := st.ConnectorCalls().Append(context.Background(), r); err != nil {
			t.Fatalf("seed audit %s: %v", r.CallID, err)
		}
	}
}

func TestListAudit_RendersDecisions(t *testing.T) {
	st := memstore.New()
	seedAuditJournal(t, st)
	ctx := context.Background()

	tests := []struct {
		name        string
		params      auditParams
		wantLines   []string
		notWant     []string
		wantErrPart string
	}{
		{
			name:      "by run shows both decisions in order",
			params:    auditParams{runID: "run-1"},
			wantLines: []string{"decision=granted", "decision=denied", "run-1#github.merge#1", "run-1#github.comment#2", "upstream=200"},
			notWant:   []string{"run-2"},
		},
		{
			name:      "by binding",
			params:    auditParams{bindingID: "binding-other"},
			wantLines: []string{"run-2#slack.chat.post_message#1", "decision=granted"},
			notWant:   []string{"run-1"},
		},
		{
			name:      "decision filter",
			params:    auditParams{runID: "run-1", decision: connectorsmodule.ConnectorCallDenied},
			wantLines: []string{"decision=denied"},
			notWant:   []string{"decision=granted"},
		},
		{
			name:      "limit",
			params:    auditParams{runID: "run-1", limit: 1},
			wantLines: []string{"run-1#github.merge#1"},
			notWant:   []string{"run-1#github.comment#2"},
		},
		{
			name:      "json output",
			params:    auditParams{runID: "run-1", jsonOut: true},
			wantLines: []string{`"decision": "granted"`, `"decision": "denied"`},
		},
		{
			name:      "empty journal message",
			params:    auditParams{runID: "run-none"},
			wantLines: []string{"No connector calls."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := listAudit(ctx, testConnectorManagement(t, st), testWS, tt.params, &out)
			if err != nil {
				t.Fatalf("listAudit: %v", err)
			}
			for _, want := range tt.wantLines {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("audit output missing %q:\n%s", want, out.String())
				}
			}
			for _, not := range tt.notWant {
				if strings.Contains(out.String(), not) {
					t.Fatalf("audit output unexpectedly contains %q:\n%s", not, out.String())
				}
			}
		})
	}
}

func TestAuditParamsValidate(t *testing.T) {
	tests := []struct {
		name    string
		params  auditParams
		wantErr string // empty means valid
	}{
		{name: "run only", params: auditParams{runID: "r1"}},
		{name: "binding only", params: auditParams{bindingID: "b1"}},
		{name: "neither", params: auditParams{}, wantErr: "exactly one of --run or --binding"},
		{name: "both", params: auditParams{runID: "r1", bindingID: "b1"}, wantErr: "exactly one of --run or --binding"},
		{name: "bad decision", params: auditParams{runID: "r1", decision: "maybe"}, wantErr: `--decision "maybe" is invalid`},
		{name: "good decision", params: auditParams{runID: "r1", decision: connectorsmodule.ConnectorCallStaleSubject}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestFormatConnectorRowGolden pins the human-readable connector row so
// format drift is deliberate (pattern: trigger's bindings-list golden).
func TestFormatConnectorRowGolden(t *testing.T) {
	rotated := time.Date(2026, 6, 11, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		conn connectorsmodule.Connector
		want string
	}{
		{
			name: "minimal",
			conn: connectorsmodule.Connector{ConnectorID: "github", SourceKind: connectorsmodule.ConnectorSourceGitHub, Status: connectorsmodule.ConnectorStatusActive},
			want: "github                   source=github    status=active   ",
		},
		{
			name: "full",
			conn: connectorsmodule.Connector{
				ConnectorID: "dd-alerts", SourceKind: connectorsmodule.ConnectorSourceDatadog, Status: connectorsmodule.ConnectorStatusDisabled,
				DisplayName: "Datadog alerts", InboundEndpointPath: "/hooks/dd", RotatedAt: &rotated,
			},
			want: `dd-alerts                source=datadog   status=disabled  name="Datadog alerts" endpoint=/hooks/dd rotated=2026-06-11T09:30:00Z`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatConnectorRow(&tt.conn); got != tt.want {
				t.Fatalf("formatConnectorRow = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadSecretLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "newline terminated", input: "secret-1\n", want: "secret-1"},
		{name: "crlf terminated", input: "secret-1\r\n", want: "secret-1"},
		{name: "eof without newline", input: "secret-1", want: "secret-1"},
		{name: "empty", input: "", wantErr: true},
		{name: "blank line", input: "\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readSecretLine(bufio.NewReader(strings.NewReader(tt.input)), "test secret")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readSecretLine = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readSecretLine: %v", err)
			}
			if got != tt.want {
				t.Fatalf("readSecretLine = %q, want %q", got, tt.want)
			}
		})
	}
}
