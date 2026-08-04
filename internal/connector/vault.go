// Package connector retains behavior-free compatibility facades while the
// Connectors owner and its adapters replace legacy callers.
package connector

import connectorsvault "github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"

const VaultKeyEnvVar = connectorsvault.VaultKeyEnvVar

var (
	ErrVaultKeyMissing = connectorsvault.ErrVaultKeyMissing
	ErrVaultKeyInvalid = connectorsvault.ErrVaultKeyInvalid
	ErrUnseal          = connectorsvault.ErrUnseal
)

type Sealer = connectorsvault.Sealer
type Vault = connectorsvault.Vault

func NewVault(key []byte) (*Vault, error) {
	return connectorsvault.NewVault(key)
}

func NewVaultFromEnv() (*Vault, error) {
	return connectorsvault.NewVaultFromEnv()
}

func NewVaultFromEnvOrKeyFile(dataDir string) (*Vault, error) {
	return connectorsvault.NewVaultFromEnvOrKeyFile(dataDir)
}

func CredentialAAD(workspaceKey string, connectorID string) []byte {
	return connectorsvault.CredentialAAD(workspaceKey, connectorID)
}
