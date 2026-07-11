package connector

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func testKey(t *testing.T, b byte) []byte {
	t.Helper()
	key := make([]byte, vaultKeySize)
	for i := range key {
		key[i] = b
	}
	return key
}

func testVault(t *testing.T, keyByte byte) *Vault {
	t.Helper()
	v, err := NewVault(testKey(t, keyByte))
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	return v
}

func TestVaultSealUnsealRoundTrip(t *testing.T) {
	v := testVault(t, 0x42)
	aad := CredentialAAD("ws-1", "github-prod")

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{"token", []byte("ghp_secret_token_value")},
		{"json blob", []byte(`{"app_id":7,"private_key":"-----BEGIN-----"}`)},
		{"empty", nil},
		{"binary", []byte{0x00, 0xff, 0x10, 0x80}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealed, err := v.Seal(tt.plaintext, aad)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if bytes.Contains(sealed, tt.plaintext) && len(tt.plaintext) > 0 {
				t.Fatalf("sealed blob contains plaintext")
			}
			got, err := v.Unseal(sealed, aad)
			if err != nil {
				t.Fatalf("Unseal: %v", err)
			}
			if !bytes.Equal(got, tt.plaintext) {
				t.Fatalf("round trip = %q, want %q", got, tt.plaintext)
			}
		})
	}
}

func TestVaultSealNonceUniqueness(t *testing.T) {
	v := testVault(t, 0x42)
	aad := CredentialAAD("ws-1", "github-prod")
	a, err := v.Seal([]byte("same plaintext"), aad)
	if err != nil {
		t.Fatalf("Seal a: %v", err)
	}
	b, err := v.Seal([]byte("same plaintext"), aad)
	if err != nil {
		t.Fatalf("Seal b: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatalf("two seals of the same plaintext produced identical blobs (nonce reuse)")
	}
}

func TestVaultUnsealRejects(t *testing.T) {
	v := testVault(t, 0x42)
	aad := CredentialAAD("ws-1", "github-prod")
	sealed, err := v.Seal([]byte("ghp_secret_token_value"), aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	tests := []struct {
		name   string
		sealed func() []byte
		aad    []byte
		vault  *Vault
	}{
		{
			name: "tampered ciphertext byte",
			sealed: func() []byte {
				out := bytes.Clone(sealed)
				out[len(out)-1] ^= 0x01
				return out
			},
			aad:   aad,
			vault: v,
		},
		{
			name: "tampered nonce byte",
			sealed: func() []byte {
				out := bytes.Clone(sealed)
				out[1] ^= 0x01
				return out
			},
			aad:   aad,
			vault: v,
		},
		{
			name: "unknown format version",
			sealed: func() []byte {
				out := bytes.Clone(sealed)
				out[0] = 0x7f
				return out
			},
			aad:   aad,
			vault: v,
		},
		{
			name:   "truncated blob",
			sealed: func() []byte { return sealed[:8] },
			aad:    aad,
			vault:  v,
		},
		{
			name:   "empty blob",
			sealed: func() []byte { return nil },
			aad:    aad,
			vault:  v,
		},
		{
			name:   "wrong AAD: different connector",
			sealed: func() []byte { return sealed },
			aad:    CredentialAAD("ws-1", "github-staging"),
			vault:  v,
		},
		{
			name:   "wrong AAD: different workspace",
			sealed: func() []byte { return sealed },
			aad:    CredentialAAD("ws-2", "github-prod"),
			vault:  v,
		},
		{
			name:   "wrong key",
			sealed: func() []byte { return sealed },
			aad:    aad,
			vault:  testVault(t, 0x43),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.vault.Unseal(tt.sealed(), tt.aad)
			if !errors.Is(err, ErrUnseal) {
				t.Fatalf("Unseal err = %v, want ErrUnseal", err)
			}
			if got != nil {
				t.Fatalf("Unseal returned plaintext %q alongside error", got)
			}
		})
	}
}

func TestNewVaultKeyShape(t *testing.T) {
	tests := []struct {
		name string
		key  []byte
	}{
		{"nil", nil},
		{"short", make([]byte, 16)},
		{"long", make([]byte, 33)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewVault(tt.key); !errors.Is(err, ErrVaultKeyInvalid) {
				t.Fatalf("NewVault err = %v, want ErrVaultKeyInvalid", err)
			} else if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("NewVault err = %v, want wrap of domain.ErrInvalid", err)
			}
		})
	}
}

func TestNewVaultFromEnv(t *testing.T) {
	validKey := base64.StdEncoding.EncodeToString(testKey(t, 0x42))

	tests := []struct {
		name    string
		value   string
		wantErr error
	}{
		{"unset", "", ErrVaultKeyMissing},
		{"not base64", "not-valid-base64!!!", ErrVaultKeyInvalid},
		{"wrong decoded length", base64.StdEncoding.EncodeToString([]byte("short")), ErrVaultKeyInvalid},
		{"valid", validKey, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(VaultKeyEnvVar, tt.value)
			v, err := NewVaultFromEnv()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewVaultFromEnv err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewVaultFromEnv: %v", err)
			}
			// Env-built vault interoperates with a raw-key vault.
			aad := CredentialAAD("ws-1", "github-prod")
			sealed, err := v.Seal([]byte("credential"), aad)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			got, err := testVault(t, 0x42).Unseal(sealed, aad)
			if err != nil {
				t.Fatalf("Unseal with raw-key vault: %v", err)
			}
			if string(got) != "credential" {
				t.Fatalf("round trip = %q, want %q", got, "credential")
			}
		})
	}
}

func TestCredentialAADInjective(t *testing.T) {
	tests := []struct {
		name     string
		ws1, id1 string
		ws2, id2 string
	}{
		{"boundary shift", "ab", "c", "a", "bc"},
		{"swap", "ws", "id", "id", "ws"},
		{"empty vs filled", "", "x", "x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := CredentialAAD(tt.ws1, tt.id1)
			b := CredentialAAD(tt.ws2, tt.id2)
			if bytes.Equal(a, b) {
				t.Fatalf("CredentialAAD(%q,%q) == CredentialAAD(%q,%q): encoding not injective",
					tt.ws1, tt.id1, tt.ws2, tt.id2)
			}
		})
	}
}
