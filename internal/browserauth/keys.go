package browserauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Loom-side signing configuration (remote / explicitly configured servers).
const (
	// EnvSigningKey is "kid=<base64url 32-byte ed25519 seed>".
	EnvSigningKey = "LOOM_BROWSER_DELEGATION_SIGNING_KEY"
	EnvIssuer     = "LOOM_BROWSER_DELEGATION_ISSUER"
	EnvAudience   = "LOOM_BROWSER_DELEGATION_AUDIENCE"
)

// FleetDB-side verification configuration. Loom sets these on the embedded
// FleetDB it launches in local mode; remote FleetDB deployments set them in
// their own environment.
const (
	EnvFleetIssuer     = "FLEET_BROWSER_DELEGATION_ISSUER"
	EnvFleetAudience   = "FLEET_BROWSER_DELEGATION_AUDIENCE"
	EnvFleetPublicKeys = "FLEET_BROWSER_DELEGATION_PUBLIC_KEYS"
)

// localKeyDir/localKeyFile hold the local desktop runtime's signing key. The
// key is distinct from the notify token, SSE/terminal secrets and local
// operator-session bearers.
const (
	localKeyDir  = "browser-delegation"
	localKeyFile = "signing-key.json"
)

// KeyPair is one Ed25519 delegation key and its key ID.
type KeyPair struct {
	KeyID   string
	Private ed25519.PrivateKey
}

// Public returns the verification key.
func (k KeyPair) Public() ed25519.PublicKey {
	return k.Private.Public().(ed25519.PublicKey)
}

// FleetPublicKeysValue formats the key for FLEET_BROWSER_DELEGATION_PUBLIC_KEYS.
func (k KeyPair) FleetPublicKeysValue() string {
	return k.KeyID + "=" + base64.RawURLEncoding.EncodeToString(k.Public())
}

// FleetVerifierEnv returns the env entries an embedded FleetDB needs to verify
// delegations minted with this key under the default local issuer/audience.
func (k KeyPair) FleetVerifierEnv() []string {
	return []string{
		EnvFleetIssuer + "=" + DefaultIssuer,
		EnvFleetAudience + "=" + DefaultAudience,
		EnvFleetPublicKeys + "=" + k.FleetPublicKeysValue(),
	}
}

// ParseSigningKey parses the EnvSigningKey format.
func ParseSigningKey(raw string) (KeyPair, error) {
	kid, encoded, ok := strings.Cut(strings.TrimSpace(raw), "=")
	kid = strings.TrimSpace(kid)
	if !ok || kid == "" {
		return KeyPair{}, fmt.Errorf("%s must be kid=<base64url ed25519 seed>", EnvSigningKey)
	}
	seed, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(seed) != ed25519.SeedSize {
		return KeyPair{}, fmt.Errorf("%s: seed must be %d base64url bytes", EnvSigningKey, ed25519.SeedSize)
	}
	return KeyPair{KeyID: kid, Private: ed25519.NewKeyFromSeed(seed)}, nil
}

// SignerFromEnv builds a signer from EnvSigningKey. It returns (nil, nil) when
// no key is configured so callers can keep browser routes disabled (503)
// instead of refusing to start.
func SignerFromEnv(getenv func(string) string) (*Signer, error) {
	raw := strings.TrimSpace(getenv(EnvSigningKey))
	if raw == "" {
		return nil, nil
	}
	kp, err := ParseSigningKey(raw)
	if err != nil {
		return nil, err
	}
	issuer := firstNonEmpty(getenv(EnvIssuer), DefaultIssuer)
	audience := firstNonEmpty(getenv(EnvAudience), DefaultAudience)
	return NewSigner(kp.KeyID, kp.Private, issuer, audience)
}

// LocalSigner returns a signer for the local desktop runtime key.
func LocalSigner(kp KeyPair) (*Signer, error) {
	return NewSigner(kp.KeyID, kp.Private, DefaultIssuer, DefaultAudience)
}

type storedKey struct {
	KeyID string `json:"kid"`
	Seed  string `json:"seed"`
}

// LocalKeyPath is where the local runtime keeps its signing key.
func LocalKeyPath(dataDir string) string {
	return filepath.Join(dataDir, localKeyDir, localKeyFile)
}

// LoadOrCreateLocalKey returns the local runtime's persisted signing key,
// creating it on first use. The directory is 0700 and the file 0600, owned by
// the current user; anything looser or foreign-owned is refused rather than
// trusted. The key persists so an embedded FleetDB restarted by another loom
// invocation keeps verifying the same key.
func LoadOrCreateLocalKey(dataDir string) (KeyPair, error) {
	if strings.TrimSpace(dataDir) == "" {
		return KeyPair{}, errors.New("browser delegation key: data dir is required")
	}
	dir := filepath.Join(dataDir, localKeyDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return KeyPair{}, fmt.Errorf("browser delegation key: mkdir: %w", err)
	}
	if err := checkPrivatePath(dir, true); err != nil {
		return KeyPair{}, err
	}
	path := filepath.Join(dir, localKeyFile)
	if kp, err := readLocalKey(path); err == nil {
		return kp, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return KeyPair{}, err
	}
	data, err := newStoredKey()
	if err != nil {
		return KeyPair{}, err
	}
	if err := publishLocalKey(dir, path, data); err != nil {
		return KeyPair{}, err
	}
	return readLocalKey(path)
}

// newStoredKey generates a fresh Ed25519 seed and key id, JSON-encoded in the
// on-disk storedKey form.
func newStoredKey() ([]byte, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("browser delegation key: generate: %w", err)
	}
	var kidBytes [6]byte
	if _, err := rand.Read(kidBytes[:]); err != nil {
		return nil, fmt.Errorf("browser delegation key: generate kid: %w", err)
	}
	return json.Marshal(storedKey{KeyID: "local-" + hex.EncodeToString(kidBytes[:]), Seed: base64.RawURLEncoding.EncodeToString(seed)})
}

// publishLocalKey writes a private temp file, then hard-links it into place:
// the key path only ever appears fully written, and the link fails if another
// loom process published first, in which case its key wins.
func publishLocalKey(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, localKeyFile+".tmp-*")
	if err != nil {
		return fmt.Errorf("browser delegation key: create: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("browser delegation key: chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("browser delegation key: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("browser delegation key: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("browser delegation key: close: %w", err)
	}
	if err := os.Link(tmpPath, path); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("browser delegation key: publish: %w", err)
	}
	return nil
}

func readLocalKey(path string) (KeyPair, error) {
	if err := checkPrivatePath(path, false); err != nil {
		return KeyPair{}, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // fixed file under the Loom data dir, checked by checkPrivatePath
	if err != nil {
		return KeyPair{}, err
	}
	var sk storedKey
	if err := json.Unmarshal(data, &sk); err != nil {
		return KeyPair{}, fmt.Errorf("browser delegation key %s: %w", path, err)
	}
	return ParseSigningKey(sk.KeyID + "=" + sk.Seed)
}

// checkPrivatePath refuses group/other-accessible or foreign-owned key
// material. It uses Lstat so a symlink cannot redirect the key elsewhere.
func checkPrivatePath(path string, dir bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("browser delegation key: %s must not be a symlink", path)
	}
	if info.IsDir() != dir {
		return fmt.Errorf("browser delegation key: %s has unexpected type", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("browser delegation key: %s must not be accessible by group or others (mode %o)", path, info.Mode().Perm())
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("browser delegation key: %s is not owned by the current user", path)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
