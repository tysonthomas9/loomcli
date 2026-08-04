// Package connectorsvault owns the local connector vault adapter. Unsealed
// current credentials are wiped inside this package and never returned to a
// caller.
package connectorsvault

import (
	"crypto/subtle"
	"errors"
	"fmt"

	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

type Adapter struct {
	vault Sealer
}

var _ connectorsmodule.CredentialVault = (*Adapter)(nil)

func New(vault Sealer) (*Adapter, error) {
	if vault == nil {
		return nil, connectorsmodule.ErrUnavailable
	}
	return &Adapter{vault: vault}, nil
}

func (adapter *Adapter) Seal(plaintext, aad []byte) ([]byte, error) {
	return adapter.vault.Seal(plaintext, aad)
}

func (adapter *Adapter) Matches(sealed, plaintext, aad []byte) (bool, error) {
	current, err := adapter.vault.Unseal(sealed, aad)
	if err != nil {
		if errors.Is(err, ErrUnseal) {
			return false, nil
		}
		return false, fmt.Errorf("unseal connector credential: %w", err)
	}
	defer zeroBytes(current)
	return subtle.ConstantTimeCompare(current, plaintext) == 1, nil
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
