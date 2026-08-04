package app

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/connector"
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
	if dispatcher := legacyConnectorDispatcher(server.buildConnectorDispatcher()); dispatcher == nil || dispatcher.Vault == nil {
		t.Fatal("buildConnectorDispatcher returned nil without env key despite configured data dir")
	}
	if dispatcher := legacyConnectorDispatcher(server.buildConnectorDispatcher()); dispatcher == nil || dispatcher.Vault == nil {
		t.Fatal("second buildConnectorDispatcher did not reuse persisted vault key")
	}
}

func TestBuildConnectorDispatcherVaultSourcePrecedence(t *testing.T) {
	t.Run("env works without data dir", func(t *testing.T) {
		key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
		t.Setenv(connector.VaultKeyEnvVar, key)
		server := &Server{config: webui.ServerConfig{Store: memstore.New()}}
		if dispatcher := legacyConnectorDispatcher(server.buildConnectorDispatcher()); dispatcher == nil || dispatcher.Vault == nil {
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

func legacyConnectorDispatcher(value any) *connector.Dispatcher {
	dispatcher, _ := value.(*connector.Dispatcher)
	return dispatcher
}
