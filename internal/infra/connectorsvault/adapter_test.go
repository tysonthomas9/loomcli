package connectorsvault_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	legacyconnector "github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
)

type vaultFake struct {
	current []byte
	err     error
}

func (fake *vaultFake) Seal(plaintext, _ []byte) ([]byte, error) {
	return append([]byte("sealed:"), plaintext...), nil
}

func (fake *vaultFake) Unseal([]byte, []byte) ([]byte, error) {
	return fake.current, fake.err
}

func TestMatchesDoesNotExposeAndWipesCurrentCredential(t *testing.T) {
	t.Parallel()
	current := []byte("credential")
	adapter, err := connectorsvault.New(&vaultFake{current: current})
	if err != nil {
		t.Fatal(err)
	}
	matched, err := adapter.Matches([]byte("sealed"), []byte("credential"), []byte("aad"))
	if err != nil || !matched {
		t.Fatalf("Matches = %t, %v", matched, err)
	}
	if !bytes.Equal(current, make([]byte, len(current))) {
		t.Fatalf("unsealed current credential was not wiped: %q", current)
	}
}

func TestMatchesTreatsAuthenticationFailureAsCredentialDrift(t *testing.T) {
	t.Parallel()
	adapter, err := connectorsvault.New(&vaultFake{
		err: fmt.Errorf("wrong key: %w", legacyconnector.ErrUnseal),
	})
	if err != nil {
		t.Fatal(err)
	}
	matched, err := adapter.Matches([]byte("sealed"), []byte("credential"), []byte("aad"))
	if err != nil || matched {
		t.Fatalf("Matches = %t, %v, want false without error", matched, err)
	}
}

func TestMatchesPreservesOperationalVaultFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("vault unavailable")
	adapter, err := connectorsvault.New(&vaultFake{err: want})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Matches(nil, nil, nil); !errors.Is(err, want) {
		t.Fatalf("Matches error = %v, want %v", err, want)
	}
}
