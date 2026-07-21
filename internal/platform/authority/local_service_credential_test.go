package authority

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLocalFleetDBServiceCredentialIsSecureAndReusable(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "fleet-service")
	first, err := loadOrCreateLocalFleetDBServiceCredential(
		runtimeDir,
		bytes.NewReader(bytes.Repeat([]byte{0x11}, localServiceCredentialBytes)),
	)
	if err != nil {
		t.Fatalf("create FleetDB service credential: %v", err)
	}
	second, err := loadOrCreateLocalFleetDBServiceCredential(
		runtimeDir,
		bytes.NewReader(bytes.Repeat([]byte{0x22}, localServiceCredentialBytes)),
	)
	if err != nil {
		t.Fatalf("reuse FleetDB service credential: %v", err)
	}
	if first != second || first != strings.Repeat("11", localServiceCredentialBytes) {
		t.Fatalf("reused FleetDB credential = %q, want first generated value", second)
	}
	if got := len(first); got != localServiceCredentialBytes*2 {
		t.Fatalf("FleetDB credential length = %d", got)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("FleetDB credential is not hex: %v", err)
	}
	dirInfo, err := os.Lstat(runtimeDir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("FleetDB credential dir = %v err=%v, want directory/0700", modeOrZero(dirInfo), err)
	}
	tokenPath := filepath.Join(runtimeDir, LocalFleetDBServiceTokenFileName)
	fileInfo, err := os.Lstat(tokenPath)
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("FleetDB credential file = %v err=%v, want regular/0600", modeOrZero(fileInfo), err)
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != LocalFleetDBServiceTokenFileName {
		t.Fatalf("FleetDB credential entries = %v err=%v", entryNames(entries), err)
	}
	read, err := ReadLocalFleetDBServiceCredential(runtimeDir)
	if err != nil || read != first {
		t.Fatalf("read FleetDB credential = %q err=%v", read, err)
	}
}

func TestLocalFleetDBServiceCredentialConcurrentCreationPublishesOneToken(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "fleet-service")
	const callers = 16
	var wg sync.WaitGroup
	tokens := make([]string, callers)
	errs := make([]error, callers)
	for index := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokens[index], errs[index] = LoadOrCreateLocalFleetDBServiceCredential(runtimeDir)
		}()
	}
	wg.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", index, err)
		}
		if tokens[index] != tokens[0] {
			t.Fatalf("caller %d credential differs", index)
		}
	}
}

func TestLocalFleetDBServiceCredentialRejectsUnsafeOrMalformedState(t *testing.T) {
	t.Run("directory symlink", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "fleet-service")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := LoadOrCreateLocalFleetDBServiceCredential(link); !errors.Is(err, errInsecureLocalServiceCredential) {
			t.Fatalf("directory symlink error = %v", err)
		}
	})

	t.Run("token symlink", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "fleet-service")
		if err := os.Mkdir(runtimeDir, 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "target-token")
		if err := os.WriteFile(target, []byte(strings.Repeat("11", localServiceCredentialBytes)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(runtimeDir, LocalFleetDBServiceTokenFileName)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := ReadLocalFleetDBServiceCredential(runtimeDir); !errors.Is(err, errInsecureLocalServiceCredential) {
			t.Fatalf("token symlink error = %v", err)
		}
	})

	t.Run("broad token mode", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "fleet-service")
		if _, err := LoadOrCreateLocalFleetDBServiceCredential(runtimeDir); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(runtimeDir, LocalFleetDBServiceTokenFileName)
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLocalFleetDBServiceCredential(runtimeDir); !errors.Is(err, errInsecureLocalServiceCredential) {
			t.Fatalf("broad mode error = %v", err)
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "fleet-service")
		if err := os.Mkdir(runtimeDir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(runtimeDir, LocalFleetDBServiceTokenFileName)
		if err := os.WriteFile(path, []byte("not-a-256-bit-token"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLocalFleetDBServiceCredential(runtimeDir); !errors.Is(err, errInvalidLocalServiceCredential) {
			t.Fatalf("malformed credential error = %v", err)
		}
	})
}

func TestLocalFleetDBServiceCredentialReadDoesNotCreateAndRandomFailureIsClean(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "missing")
	if _, err := ReadLocalFleetDBServiceCredential(runtimeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing read error = %v", err)
	}
	if _, err := os.Lstat(runtimeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created runtime directory: %v", err)
	}

	runtimeDir = filepath.Join(t.TempDir(), "random-failure")
	want := errors.New("random failed")
	if _, err := loadOrCreateLocalFleetDBServiceCredential(runtimeDir, errorReader{err: want}); !errors.Is(err, want) {
		t.Fatalf("random failure = %v", err)
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("random failure entries = %v err=%v", entryNames(entries), err)
	}
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

var _ io.Reader = errorReader{}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func modeOrZero(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}
