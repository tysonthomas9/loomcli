package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

func TestBuildConnectorDispatcherUsesPersistedVaultFallback(t *testing.T) {
	t.Setenv(connector.VaultKeyEnvVar, "")
	dataDir := t.TempDir()
	server := &Server{config: webui.ServerConfig{
		Store:            memstore.New(),
		LocalSettingsDir: dataDir,
	}}
	if dispatcher := server.buildConnectorDispatcher(); dispatcher == nil || dispatcher.Vault == nil {
		t.Fatal("buildConnectorDispatcher returned nil without env key despite configured data dir")
	}
	if dispatcher := server.buildConnectorDispatcher(); dispatcher == nil || dispatcher.Vault == nil {
		t.Fatal("second buildConnectorDispatcher did not reuse persisted vault key")
	}
}

func TestBuildConnectorDispatcherVaultSourcePrecedence(t *testing.T) {
	t.Run("env works without data dir", func(t *testing.T) {
		key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
		t.Setenv(connector.VaultKeyEnvVar, key)
		server := &Server{config: webui.ServerConfig{Store: memstore.New()}}
		if dispatcher := server.buildConnectorDispatcher(); dispatcher == nil || dispatcher.Vault == nil {
			t.Fatal("buildConnectorDispatcher ignored env vault key")
		}
	})

	t.Run("missing env and data dir fails closed", func(t *testing.T) {
		t.Setenv(connector.VaultKeyEnvVar, "")
		server := &Server{config: webui.ServerConfig{Store: memstore.New()}}
		if dispatcher := server.buildConnectorDispatcher(); dispatcher != nil {
			t.Fatal("buildConnectorDispatcher enabled egress without a vault source")
		}
	})
}
func TestBuildConnectorDispatcherUsesRecordingProviderEnv(t *testing.T) {
	t.Setenv(connector.VaultKeyEnvVar, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	t.Setenv(connector.FakeProviderEnvVar, "1")
	connector.ResetRecordingProviderCalls()
	t.Cleanup(connector.ResetRecordingProviderCalls)

	app := &Server{config: webui.ServerConfig{Store: memstore.New()}}
	dispatcher := app.buildConnectorDispatcher()
	if dispatcher == nil {
		t.Fatal("buildConnectorDispatcher returned nil")
	}
	provider, err := dispatcher.Providers.Get(domain.ConnectorSourceGitHub)
	if err != nil {
		t.Fatalf("Get github provider: %v", err)
	}
	result, err := provider.Call(context.Background(), providers.CallSpec{
		Action:     providers.ActionGitHubPullRequestRead,
		Resource:   "repo:octocat/hello",
		Credential: "ghp_secret",
	})
	if err != nil {
		t.Fatalf("recording provider Call: %v", err)
	}
	if result.Decision != domain.ConnectorCallGranted || result.Status != 200 || result.Body["fakeProvider"] != true {
		t.Fatalf("result = %+v, want recording provider granted response", result)
	}
	calls := connector.RecordingProviderCalls()
	if len(calls) != 1 || calls[0].Action != providers.ActionGitHubPullRequestRead || !calls[0].CredentialPresented {
		t.Fatalf("recording calls = %+v, want one live-serve provider call", calls)
	}
}
