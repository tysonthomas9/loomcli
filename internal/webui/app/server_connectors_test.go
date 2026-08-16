package app

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

func TestBuildConnectorCapabilitiesUsePersistedVault(t *testing.T) {
	t.Setenv(connectorsmodule.VaultKeyEnvVar, "")
	dataDir := t.TempDir()
	server := &Server{config: webui.ServerConfig{
		ProjectionRecords: memstore.New(),
		LocalSettingsDir:  dataDir,
	}}
	if dispatcher, management, sealer := server.buildConnectorCapabilities(); !connectorsmodule.DispatcherAvailable(dispatcher) || management == nil || sealer == nil {
		t.Fatal("buildConnectorCapabilities returned nil without env key despite configured data dir")
	}
	if dispatcher, management, sealer := server.buildConnectorCapabilities(); !connectorsmodule.DispatcherAvailable(dispatcher) || management == nil || sealer == nil {
		t.Fatal("second buildConnectorCapabilities did not reuse persisted vault key")
	}
}

func TestBuildConnectorCapabilitiesVaultSourcePrecedence(t *testing.T) {
	t.Run("env works without data dir", func(t *testing.T) {
		key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
		t.Setenv(connectorsmodule.VaultKeyEnvVar, key)
		server := &Server{config: webui.ServerConfig{ProjectionRecords: memstore.New()}}
		if dispatcher, management, sealer := server.buildConnectorCapabilities(); !connectorsmodule.DispatcherAvailable(dispatcher) || management == nil || sealer == nil {
			t.Fatal("buildConnectorCapabilities ignored env vault key")
		}
	})

	t.Run("missing env and data dir fails closed", func(t *testing.T) {
		t.Setenv(connectorsmodule.VaultKeyEnvVar, "")
		server := &Server{config: webui.ServerConfig{ProjectionRecords: memstore.New()}}
		if dispatcher, management, sealer := server.buildConnectorCapabilities(); dispatcher != nil || management != nil || sealer != nil {
			t.Fatal("buildConnectorCapabilities enabled connectors without a vault source")
		}
	})
}

func TestBuildConnectorCapabilitiesRequireCompositionStore(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	t.Setenv(connectorsmodule.VaultKeyEnvVar, key)
	server := &Server{config: webui.ServerConfig{}}
	if dispatcher, management, sealer := server.buildConnectorCapabilities(); dispatcher != nil || management != nil || sealer != nil {
		t.Fatal("buildConnectorCapabilities enabled connectors without an injected composition store")
	}
}
