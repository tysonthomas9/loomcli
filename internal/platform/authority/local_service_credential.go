package authority

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// LocalFleetDBServiceTokenFileName is the persistent service credential used
	// only between Loom and its embedded FleetDB process.
	LocalFleetDBServiceTokenFileName = "fleetdb-service.token"
	localServiceCredentialBytes      = 32
)

var (
	errInvalidLocalServiceCredential  = errors.New("authority: invalid local service credential")
	errInsecureLocalServiceCredential = errors.New("authority: insecure local service credential")
)

// LoadOrCreateLocalFleetDBServiceCredential securely loads or atomically
// creates the persistent credential used by Loom's embedded FleetDB client.
func LoadOrCreateLocalFleetDBServiceCredential(runtimeDir string) (string, error) {
	return loadOrCreateLocalFleetDBServiceCredential(runtimeDir, rand.Reader)
}

func loadOrCreateLocalFleetDBServiceCredential(runtimeDir string, random io.Reader) (string, error) {
	if random == nil {
		return "", errInvalidLocalServiceCredential
	}
	dir, err := prepareLocalServiceCredentialDir(runtimeDir, true)
	if err != nil {
		return "", err
	}
	token, _, err := loadOrCreateLocalServiceCredentialFile(dir, random)
	return token, err
}

// ReadLocalFleetDBServiceCredential loads an existing, validated embedded
// FleetDB service credential without creating filesystem state.
func ReadLocalFleetDBServiceCredential(runtimeDir string) (string, error) {
	dir, err := prepareLocalServiceCredentialDir(runtimeDir, false)
	if err != nil {
		return "", err
	}
	token, _, err := readLocalServiceCredentialFile(dir)
	return token, err
}

func prepareLocalServiceCredentialDir(runtimeDir string, create bool) (string, error) {
	if strings.TrimSpace(runtimeDir) == "" {
		return "", fmt.Errorf("%w: runtime directory is required", ErrInvalidScope)
	}
	dir := filepath.Clean(runtimeDir)
	info, err := os.Lstat(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) || !create {
			return "", fmt.Errorf("local service credential directory: %w", err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create local service credential directory: %w", err)
		}
		// MkdirAll is affected by the process umask. Set the leaf explicitly so
		// the server-owned directory is usable and never broader than 0700.
		// #nosec G302 -- directories require execute bits; 0700 is intentional.
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", fmt.Errorf("secure local service credential directory: %w", err)
		}
		info, err = os.Lstat(dir)
		if err != nil {
			return "", fmt.Errorf("stat local service credential directory: %w", err)
		}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%w: runtime path must be a real directory", errInsecureLocalServiceCredential)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%w: runtime directory mode is %04o, want no broader than 0700", errInsecureLocalServiceCredential, info.Mode().Perm())
	}
	return dir, nil
}

func loadOrCreateLocalServiceCredentialFile(runtimeDir string, random io.Reader) (string, [localServiceCredentialBytes]byte, error) {
	token, decoded, err := readLocalServiceCredentialFile(runtimeDir)
	if err == nil {
		return token, decoded, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", [localServiceCredentialBytes]byte{}, err
	}
	return createLocalServiceCredentialFile(runtimeDir, random)
}

func createLocalServiceCredentialFile(runtimeDir string, random io.Reader) (string, [localServiceCredentialBytes]byte, error) {
	var generated [localServiceCredentialBytes]byte
	if _, err := io.ReadFull(random, generated[:]); err != nil {
		return "", generated, fmt.Errorf("generate local service credential: %w", err)
	}
	token := hex.EncodeToString(generated[:])
	temporary, err := os.CreateTemp(runtimeDir, ".fleetdb-service.token-*")
	if err != nil {
		return "", generated, fmt.Errorf("create local service credential temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	closeWithError := func(prior error) error {
		if closeErr := temporary.Close(); prior == nil {
			return closeErr
		}
		return prior
	}
	if err := temporary.Chmod(0o600); err != nil {
		return "", generated, fmt.Errorf("chmod local service credential temporary file: %w", closeWithError(err))
	}
	if _, err := io.WriteString(temporary, token); err != nil {
		return "", generated, fmt.Errorf("write local service credential temporary file: %w", closeWithError(err))
	}
	if err := temporary.Sync(); err != nil {
		return "", generated, fmt.Errorf("sync local service credential temporary file: %w", closeWithError(err))
	}
	if err := temporary.Close(); err != nil {
		return "", generated, fmt.Errorf("close local service credential temporary file: %w", err)
	}
	path := filepath.Join(runtimeDir, LocalFleetDBServiceTokenFileName)
	if err := os.Link(temporaryPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return readLocalServiceCredentialFile(runtimeDir)
		}
		return "", generated, fmt.Errorf("publish local service credential: %w", err)
	}
	return readLocalServiceCredentialFile(runtimeDir)
}

func readLocalServiceCredentialFile(runtimeDir string) (string, [localServiceCredentialBytes]byte, error) {
	path := filepath.Join(runtimeDir, LocalFleetDBServiceTokenFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return "", [localServiceCredentialBytes]byte{}, fmt.Errorf("stat local service credential: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", [localServiceCredentialBytes]byte{}, fmt.Errorf("%w: credential path must be a regular file", errInsecureLocalServiceCredential)
	}
	if info.Mode().Perm() != 0o600 {
		return "", [localServiceCredentialBytes]byte{}, fmt.Errorf("%w: credential file mode is %04o, want 0600", errInsecureLocalServiceCredential, info.Mode().Perm())
	}
	file, err := os.Open(path) // #nosec G304 -- the configured directory and file were lstat-validated above.
	if err != nil {
		return "", [localServiceCredentialBytes]byte{}, fmt.Errorf("open local service credential: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", [localServiceCredentialBytes]byte{}, fmt.Errorf("stat opened local service credential: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm() != 0o600 || !os.SameFile(info, openedInfo) {
		return "", [localServiceCredentialBytes]byte{}, fmt.Errorf("%w: opened credential is not a mode-0600 regular file", errInsecureLocalServiceCredential)
	}
	contents, err := io.ReadAll(io.LimitReader(file, int64(hex.EncodedLen(localServiceCredentialBytes)+1)))
	if err != nil {
		return "", [localServiceCredentialBytes]byte{}, fmt.Errorf("read local service credential: %w", err)
	}
	if len(contents) != hex.EncodedLen(localServiceCredentialBytes) {
		return "", [localServiceCredentialBytes]byte{}, fmt.Errorf("%w: credential must contain exactly %d hex characters", errInvalidLocalServiceCredential, hex.EncodedLen(localServiceCredentialBytes))
	}
	var decoded [localServiceCredentialBytes]byte
	if _, err := hex.Decode(decoded[:], contents); err != nil {
		return "", decoded, fmt.Errorf("%w: persisted credential is not hex", errInvalidLocalServiceCredential)
	}
	return string(contents), decoded, nil
}
