// Package connector holds the pure control-plane logic for step-7
// connectors: envelope encryption of outbound credentials (vault.go) and
// deny-by-default grant evaluation plus the irreversible-action registry
// (grants.go).
//
// The package deliberately imports only the standard library and
// internal/domain — no driver, driverapi, webhook, or trigger packages — so
// it conflicts with nothing else in flight and stays independently testable.
package connector

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// VaultKeyEnvVar names the environment variable holding the serve-held vault
// key: the standard base64 encoding of exactly 32 random bytes. Only the
// control plane (loomcli serve) ever reads it; stores hold ciphertext only.
const VaultKeyEnvVar = "LOOM_CONNECTOR_VAULT_KEY"

// vaultKeySize is the AES-256 key length in bytes.
const vaultKeySize = 32

// sealedFormatV1 is the version byte prefixing every sealed blob:
//
//	sealed = version(1) || nonce(12) || AES-256-GCM ciphertext+tag
//
// A future key or scheme rotation (re-seal sweep, KMS) bumps the version so
// old and new blobs coexist during the sweep.
const sealedFormatV1 byte = 0x01

// Vault sentinel errors. Callers match them via errors.Is; the key-shape
// errors additionally wrap domain.ErrInvalid.
var (
	// ErrVaultKeyMissing indicates LOOM_CONNECTOR_VAULT_KEY is unset or
	// empty while connectors are enabled. The constructor errors loudly so
	// serve refuses to start a connector path without a sealing key.
	ErrVaultKeyMissing = fmt.Errorf("connector: %s not set: %w", VaultKeyEnvVar, domain.ErrInvalid)

	// ErrVaultKeyInvalid indicates the supplied key material is not the
	// standard base64 encoding of exactly 32 bytes.
	ErrVaultKeyInvalid = fmt.Errorf("connector: vault key invalid: %w", domain.ErrInvalid)

	// ErrUnseal indicates a sealed credential failed authentication: the
	// blob was tampered with, truncated, sealed under a different key, or
	// presented with the wrong AAD (e.g. spliced across connectors). The
	// error is deliberately uniform so callers cannot distinguish the
	// failure mode.
	ErrUnseal = errors.New("connector: credential unseal failed")
)

// Sealer is the envelope-encryption seam for outbound connector credentials.
//
// Seal is called by serve BEFORE any store write — stores only ever see the
// returned ciphertext. Unseal is called only inside the dispatch call path;
// the plaintext credential lives on that call's stack and is never returned
// over any API or written to any log.
//
// Implementations must bind the ciphertext to the supplied additional
// authenticated data so a blob copied between connectors fails to unseal
// (see CredentialAAD). Vault is the default AES-256-GCM implementation; a
// KMS-backed sealer can replace it behind this interface without touching
// callers. Key rotation is a re-seal sweep: unseal with the old sealer, seal
// with the new.
type Sealer interface {
	Seal(plaintext, aad []byte) ([]byte, error)
	Unseal(sealed, aad []byte) ([]byte, error)
}

// Vault seals and unseals outbound credentials with AES-256-GCM under a
// single serve-held key. It implements Sealer.
type Vault struct {
	aead cipher.AEAD
}

var _ Sealer = (*Vault)(nil)

// NewVault builds a Vault from raw key bytes (exactly 32). Key-shape
// violations wrap ErrVaultKeyInvalid.
func NewVault(key []byte) (*Vault, error) {
	if len(key) != vaultKeySize {
		return nil, fmt.Errorf("key is %d bytes, want %d: %w", len(key), vaultKeySize, ErrVaultKeyInvalid)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w (%w)", err, ErrVaultKeyInvalid)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm mode: %w (%w)", err, ErrVaultKeyInvalid)
	}
	return &Vault{aead: aead}, nil
}

// NewVaultFromEnv builds a Vault from LOOM_CONNECTOR_VAULT_KEY (standard
// base64, 32 decoded bytes). It fails with ErrVaultKeyMissing when the
// variable is unset or empty and ErrVaultKeyInvalid when it does not decode
// to a 32-byte key — serve must refuse to enable connectors in either case.
func NewVaultFromEnv() (*Vault, error) {
	encoded := os.Getenv(VaultKeyEnvVar)
	if encoded == "" {
		return nil, ErrVaultKeyMissing
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%s is not standard base64: %w", VaultKeyEnvVar, ErrVaultKeyInvalid)
	}
	return NewVault(key)
}

// CredentialAAD builds the additional authenticated data binding a sealed
// outbound credential to its owning connector. Unsealing with a different
// workspace or connector identity fails authentication, which blocks
// splicing a ciphertext from one connector record into another. The NUL
// separators make the encoding injective (identifiers never contain NUL).
func CredentialAAD(workspaceKey, connectorID string) []byte {
	aad := make([]byte, 0, len("loom-connector-credential")+len(workspaceKey)+len(connectorID)+2)
	aad = append(aad, "loom-connector-credential"...)
	aad = append(aad, 0)
	aad = append(aad, workspaceKey...)
	aad = append(aad, 0)
	aad = append(aad, connectorID...)
	return aad
}

// Seal encrypts plaintext under the vault key with a fresh random nonce and
// binds it to aad. The returned blob is version || nonce || ciphertext+tag
// and is the only form a store ever holds.
func (v *Vault) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("connector: seal nonce: %w", err)
	}
	sealed := make([]byte, 0, 1+len(nonce)+len(plaintext)+v.aead.Overhead())
	sealed = append(sealed, sealedFormatV1)
	sealed = append(sealed, nonce...)
	return v.aead.Seal(sealed, nonce, plaintext, aad), nil
}

// Unseal authenticates and decrypts a blob produced by Seal under the same
// key and aad. Every failure mode — tampered ciphertext, truncation, unknown
// format version, wrong key, wrong AAD — wraps ErrUnseal uniformly.
//
// The returned plaintext must stay on the dispatch call's stack: never store
// it, never return it over an API, never log it.
func (v *Vault) Unseal(sealed, aad []byte) ([]byte, error) {
	nonceSize := v.aead.NonceSize()
	if len(sealed) < 1+nonceSize+v.aead.Overhead() {
		return nil, fmt.Errorf("sealed blob too short: %w", ErrUnseal)
	}
	if sealed[0] != sealedFormatV1 {
		return nil, fmt.Errorf("unknown sealed format version %#x: %w", sealed[0], ErrUnseal)
	}
	nonce := sealed[1 : 1+nonceSize]
	ciphertext := sealed[1+nonceSize:]
	plaintext, err := v.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", ErrUnseal)
	}
	return plaintext, nil
}
