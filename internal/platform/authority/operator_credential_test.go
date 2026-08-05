package authority

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const localCredentialTestAction = Action("workflowcatalog.approve-version")

func TestLoadOrCreateLocalOperatorCredentialPersistsOnly256BitHexToken(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	issuer, err := loadOrCreateLocalOperatorCredential(runtimeDir, "workspace-a", localOperatorDependencies{
		random: bytes.NewReader(bytes.Repeat([]byte{0xab}, localOperatorTokenBytes)),
		now:    func() time.Time { return now },
		ttl:    time.Minute,
	})
	if err != nil {
		t.Fatalf("loadOrCreateLocalOperatorCredential: %v", err)
	}
	if err := issuer.validate(); err != nil {
		t.Fatalf("issuer validation: %v", err)
	}

	dirInfo, err := os.Lstat(runtimeDir)
	if err != nil {
		t.Fatalf("stat runtime directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("runtime directory mode = %04o, want 0700", got)
	}
	path := filepath.Join(runtimeDir, LocalOperatorTokenFileName)
	fileInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v/%04o, want regular/0600", fileInfo.Mode().Type(), fileInfo.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if got, want := len(contents), localOperatorTokenBytes*2; got != want {
		t.Fatalf("token character length = %d, want %d", got, want)
	}
	if got, want := string(contents), strings.Repeat("ab", localOperatorTokenBytes); got != want {
		t.Fatalf("token contents = %q, want %q", got, want)
	}
	if bytes.ContainsAny(contents, "{}[]\"\n\r") || bytes.Contains(contents, []byte("workspace-a")) {
		t.Fatalf("token file contains structured authority or workspace data: %q", contents)
	}
	read, err := ReadLocalOperatorToken(runtimeDir)
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	if read != string(contents) {
		t.Fatalf("ReadLocalOperatorToken = %q, want %q", read, contents)
	}
}

func TestLocalOperatorCredentialReusesExistingToken(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	first, err := loadOrCreateLocalOperatorCredential(runtimeDir, "workspace-a", localOperatorDependencies{
		random: bytes.NewReader(bytes.Repeat([]byte{0x11}, localOperatorTokenBytes)),
		now:    func() time.Time { return now },
		ttl:    time.Minute,
	})
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := loadOrCreateLocalOperatorCredential(runtimeDir, "workspace-a", localOperatorDependencies{
		random: bytes.NewReader(bytes.Repeat([]byte{0x22}, localOperatorTokenBytes)),
		now:    func() time.Time { return now },
		ttl:    time.Minute,
	})
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if first.token != second.token {
		t.Fatalf("reloaded token differs: %x != %x", first.token, second.token)
	}
	token, err := ReadLocalOperatorToken(runtimeDir)
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	if token != strings.Repeat("11", localOperatorTokenBytes) {
		t.Fatalf("reused token = %q", token)
	}
}

func TestLocalFleetDBServiceCredentialIsSeparateSecureAndReusable(t *testing.T) {
	root := t.TempDir()
	serviceDir := filepath.Join(root, "fleet-service")
	operatorDir := filepath.Join(root, "operator")
	if _, err := LoadOrCreateLocalOperatorCredential(operatorDir, "workspace-a"); err != nil {
		t.Fatalf("create operator credential: %v", err)
	}
	operatorToken, err := ReadLocalOperatorToken(operatorDir)
	if err != nil {
		t.Fatalf("read operator credential: %v", err)
	}
	first, err := LoadOrCreateLocalFleetDBServiceCredential(serviceDir)
	if err != nil {
		t.Fatalf("create FleetDB service credential: %v", err)
	}
	second, err := LoadOrCreateLocalFleetDBServiceCredential(serviceDir)
	if err != nil {
		t.Fatalf("reuse FleetDB service credential: %v", err)
	}
	if first != second {
		t.Fatalf("reused FleetDB credential differs")
	}
	if first == operatorToken {
		t.Fatal("FleetDB service credential reused the operator token")
	}
	if got := len(first); got != localOperatorTokenBytes*2 {
		t.Fatalf("FleetDB credential length = %d, want %d", got, localOperatorTokenBytes*2)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("FleetDB credential is not hex: %v", err)
	}
	dirInfo, err := os.Lstat(serviceDir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("FleetDB credential dir = %v err=%v, want directory/0700", credentialModeOrZero(dirInfo), err)
	}
	tokenPath := filepath.Join(serviceDir, LocalFleetDBServiceTokenFileName)
	fileInfo, err := os.Lstat(tokenPath)
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("FleetDB credential file = %v err=%v, want regular/0600", credentialModeOrZero(fileInfo), err)
	}
	if _, err := os.Lstat(filepath.Join(serviceDir, LocalOperatorTokenFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("operator token exists in FleetDB credential scope: %v", err)
	}
	read, err := ReadLocalFleetDBServiceCredential(serviceDir)
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
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		t.Fatalf("read FleetDB credential directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != LocalFleetDBServiceTokenFileName {
		t.Fatalf("FleetDB credential entries = %v", entryNames(entries))
	}
}

func TestLocalFleetDBServiceCredentialRejectsSymlinksAndBroadModes(t *testing.T) {
	t.Run("directory symlink", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatalf("mkdir target: %v", err)
		}
		link := filepath.Join(t.TempDir(), "fleet-service")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := LoadOrCreateLocalFleetDBServiceCredential(link); !errors.Is(err, ErrInsecureOperatorCredential) {
			t.Fatalf("directory symlink error = %v", err)
		}
	})

	t.Run("token symlink", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "fleet-service")
		if err := os.Mkdir(runtimeDir, 0o700); err != nil {
			t.Fatalf("mkdir runtime: %v", err)
		}
		target := filepath.Join(t.TempDir(), "target-token")
		if err := os.WriteFile(target, []byte(strings.Repeat("11", localOperatorTokenBytes)), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(runtimeDir, LocalFleetDBServiceTokenFileName)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := ReadLocalFleetDBServiceCredential(runtimeDir); !errors.Is(err, ErrInsecureOperatorCredential) {
			t.Fatalf("token symlink error = %v", err)
		}
	})

	t.Run("broad token mode", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "fleet-service")
		if _, err := LoadOrCreateLocalFleetDBServiceCredential(runtimeDir); err != nil {
			t.Fatalf("create credential: %v", err)
		}
		path := filepath.Join(runtimeDir, LocalFleetDBServiceTokenFileName)
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatalf("chmod credential: %v", err)
		}
		if _, err := ReadLocalFleetDBServiceCredential(runtimeDir); !errors.Is(err, ErrInsecureOperatorCredential) {
			t.Fatalf("broad mode error = %v", err)
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "fleet-service")
		if err := os.Mkdir(runtimeDir, 0o700); err != nil {
			t.Fatalf("mkdir runtime: %v", err)
		}
		path := filepath.Join(runtimeDir, LocalFleetDBServiceTokenFileName)
		if err := os.WriteFile(path, []byte("not-a-256-bit-token"), 0o600); err != nil {
			t.Fatalf("write malformed credential: %v", err)
		}
		if _, err := ReadLocalFleetDBServiceCredential(runtimeDir); !errors.Is(err, ErrInvalidOperatorToken) {
			t.Fatalf("malformed credential error = %v", err)
		}
	})
}

func TestLocalOperatorCredentialConcurrentCreationPublishesOneCompleteToken(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	const callers = 16
	randomBytes := make([]byte, callers*localOperatorTokenBytes)
	for index := range randomBytes {
		randomBytes[index] = byte(index)
	}
	random := &lockedReader{reader: bytes.NewReader(randomBytes)}

	var wg sync.WaitGroup
	issuers := make([]*LocalOperatorIssuer, callers)
	errs := make([]error, callers)
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			issuers[index], errs[index] = loadOrCreateLocalOperatorCredential(runtimeDir, "workspace-a", localOperatorDependencies{
				random: random,
				now:    func() time.Time { return now },
				ttl:    time.Minute,
			})
		}(index)
	}
	wg.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", index, err)
		}
	}
	want := issuers[0].token
	for index, issuer := range issuers[1:] {
		if issuer.token != want {
			t.Fatalf("caller %d token differs: %x != %x", index+1, issuer.token, want)
		}
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != LocalOperatorTokenFileName {
		t.Fatalf("runtime directory entries = %v, want only %s", entryNames(entries), LocalOperatorTokenFileName)
	}
	if token, err := ReadLocalOperatorToken(runtimeDir); err != nil || len(token) != localOperatorTokenBytes*2 {
		t.Fatalf("published token = %q err=%v", token, err)
	}
}

func TestLocalOperatorCredentialRejectsInsecureModes(t *testing.T) {
	t.Run("runtime directory", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "runtime")
		if err := os.Mkdir(runtimeDir, 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		if _, err := LoadOrCreateLocalOperatorCredential(runtimeDir, "workspace-a"); !errors.Is(err, ErrInsecureOperatorCredential) {
			t.Fatalf("LoadOrCreate error = %v, want ErrInsecureOperatorCredential", err)
		}
	})

	t.Run("token file", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "runtime")
		if err := os.Mkdir(runtimeDir, 0o700); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		path := filepath.Join(runtimeDir, LocalOperatorTokenFileName)
		if err := os.WriteFile(path, []byte(strings.Repeat("11", localOperatorTokenBytes)), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		if _, err := ReadLocalOperatorToken(runtimeDir); !errors.Is(err, ErrInsecureOperatorCredential) {
			t.Fatalf("Read error = %v, want ErrInsecureOperatorCredential", err)
		}
		if _, err := LoadOrCreateLocalOperatorCredential(runtimeDir, "workspace-a"); !errors.Is(err, ErrInsecureOperatorCredential) {
			t.Fatalf("LoadOrCreate error = %v, want ErrInsecureOperatorCredential", err)
		}
	})
}

func TestLocalOperatorCredentialRejectsMalformedPersistedToken(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "short", contents: "abcd"},
		{name: "newline", contents: strings.Repeat("11", localOperatorTokenBytes) + "\n"},
		{name: "non hex", contents: strings.Repeat("zz", localOperatorTokenBytes)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := filepath.Join(t.TempDir(), "runtime")
			if err := os.Mkdir(runtimeDir, 0o700); err != nil {
				t.Fatalf("Mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(runtimeDir, LocalOperatorTokenFileName), []byte(tt.contents), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := ReadLocalOperatorToken(runtimeDir); !errors.Is(err, ErrInvalidOperatorToken) {
				t.Fatalf("Read error = %v, want ErrInvalidOperatorToken", err)
			}
		})
	}
}

func TestIssueLocalOperatorValidatesBearerAndServerWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	issuer, token := newLocalOperatorTestIssuer(t, runtimeDir, "workspace-a", &now, time.Minute)
	admission, err := issuer.NewAdmission(OperatorOnly(localCredentialTestAction))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	for _, presented := range []string{token, "Bearer " + token, "bearer " + token} {
		operator, err := IssueOperator(issuer, presented, "workspace-a", localCredentialTestAction)
		if err != nil {
			t.Fatalf("IssueOperator(%q): %v", presented[:min(len(presented), 8)], err)
		}
		if operator.Subject() != localOperatorSubject || operator.Workspace() != "workspace-a" || operator.Action() != localCredentialTestAction {
			t.Fatalf("operator scope = subject %q workspace %q action %q", operator.Subject(), operator.Workspace(), operator.Action())
		}
		if err := admission.RequireOperator(localCredentialTestAction, "workspace-a", operator); err != nil {
			t.Fatalf("RequireOperator: %v", err)
		}
	}

	if _, err := IssueOperator(issuer, token, " ", localCredentialTestAction); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("empty workspace error = %v, want ErrInvalidScope", err)
	}
	if _, err := IssueOperator(issuer, token, "workspace-b", localCredentialTestAction); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("wrong workspace error = %v, want ErrWorkspaceMismatch", err)
	}

	invalid := []string{
		"",
		"Bearer",
		"Bearer " + token + " extra",
		strings.Repeat("00", localOperatorTokenBytes),
		strings.Repeat("zz", localOperatorTokenBytes),
		token[:len(token)-2],
	}
	for _, presented := range invalid {
		if _, err := IssueOperator(issuer, presented, "workspace-a", localCredentialTestAction); !errors.Is(err, ErrInvalidOperatorToken) {
			t.Fatalf("invalid bearer %q error = %v, want ErrInvalidOperatorToken", presented, err)
		}
	}
}

func TestRuntimeOperatorCredentialNarrowsToEachServerDerivedWorkspace(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	issuer, err := LoadOrCreateLocalRuntimeOperatorCredential(runtimeDir)
	if err != nil {
		t.Fatalf("LoadOrCreateLocalRuntimeOperatorCredential: %v", err)
	}
	token, err := ReadLocalOperatorToken(runtimeDir)
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	admission, err := issuer.NewAdmission(OperatorOnly(localCredentialTestAction))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		auth, err := IssueOperator(issuer, token, workspace, localCredentialTestAction)
		if err != nil {
			t.Fatalf("IssueOperator(%s): %v", workspace, err)
		}
		if auth.Workspace() != workspace {
			t.Fatalf("authority workspace = %q, want %q", auth.Workspace(), workspace)
		}
		if err := admission.RequireOperator(localCredentialTestAction, workspace, auth); err != nil {
			t.Fatalf("RequireOperator(%s): %v", workspace, err)
		}
		other := "workspace-a"
		if workspace == other {
			other = "workspace-b"
		}
		if err := admission.RequireOperator(localCredentialTestAction, other, auth); !errors.Is(err, ErrAdmissionDenied) {
			t.Fatalf("cross-workspace admission error = %v", err)
		}
	}
}

func TestIssuedLocalOperatorAuthorityExpiresAtAdmission(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	const ttl = 30 * time.Second
	issuer, token := newLocalOperatorTestIssuer(t, runtimeDir, "workspace-a", &now, ttl)
	admission, err := issuer.NewAdmission(OperatorOnly(localCredentialTestAction))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	operator, err := IssueOperator(issuer, token, "workspace-a", localCredentialTestAction)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	if got, want := operator.ExpiresAt(), now.Add(ttl); !got.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got, want)
	}
	if err := admission.RequireOperator(localCredentialTestAction, "workspace-a", operator); err != nil {
		t.Fatalf("fresh authority rejected: %v", err)
	}
	now = now.Add(ttl)
	err = admission.RequireOperator(localCredentialTestAction, "workspace-a", operator)
	var denial *AdmissionError
	if !errors.As(err, &denial) || denial.Reason != DenialExpired {
		t.Fatalf("expired admission error = %v, want DenialExpired", err)
	}
}

func TestLocalOperatorOperationRegistryStillDefaultDeniesUnknownAction(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	issuer, token := newLocalOperatorTestIssuer(t, runtimeDir, "workspace-a", &now, time.Minute)
	admission, err := issuer.NewAdmission(OperatorOnly(localCredentialTestAction))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	unknown := Action("workflowcatalog.delete-version")
	operator, err := IssueOperator(issuer, token, "workspace-a", unknown)
	if err != nil {
		t.Fatalf("IssueOperator unknown action: %v", err)
	}
	err = admission.RequireOperator(unknown, "workspace-a", operator)
	var denial *AdmissionError
	if !errors.As(err, &denial) || denial.Reason != DenialUnknownOperation {
		t.Fatalf("unknown operation error = %v, want DenialUnknownOperation", err)
	}
}

func TestReadLocalOperatorTokenDoesNotCreateMissingRuntimeDirectory(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "missing")
	if _, err := ReadLocalOperatorToken(runtimeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Read error = %v, want not-exist", err)
	}
	if _, err := os.Lstat(runtimeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime directory was created or unexpected stat error: %v", err)
	}
}

func TestLocalOperatorCredentialValidatesInputsAndRandomFailure(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	validDeps := localOperatorDependencies{random: bytes.NewReader(make([]byte, localOperatorTokenBytes)), now: func() time.Time { return now }, ttl: time.Minute}
	if _, err := loadOrCreateLocalOperatorCredential("", "workspace-a", validDeps); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("empty runtime dir error = %v, want ErrInvalidScope", err)
	}
	if _, err := loadOrCreateLocalOperatorCredential(filepath.Join(t.TempDir(), "runtime"), " ", validDeps); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("empty workspace error = %v, want ErrInvalidScope", err)
	}
	badDeps := []localOperatorDependencies{
		{now: func() time.Time { return now }, ttl: time.Minute},
		{random: bytes.NewReader(nil), ttl: time.Minute},
		{random: bytes.NewReader(nil), now: func() time.Time { return now }},
	}
	for index, deps := range badDeps {
		if _, err := loadOrCreateLocalOperatorCredential(filepath.Join(t.TempDir(), fmt.Sprintf("runtime-%d", index)), "workspace-a", deps); !errors.Is(err, ErrInvalidLocalOperatorIssuer) {
			t.Fatalf("bad deps %d error = %v, want ErrInvalidLocalOperatorIssuer", index, err)
		}
	}
	runtimeDir := filepath.Join(t.TempDir(), "runtime-random-failure")
	_, err := loadOrCreateLocalOperatorCredential(runtimeDir, "workspace-a", localOperatorDependencies{
		random: errorReader{err: io.ErrUnexpectedEOF},
		now:    func() time.Time { return now },
		ttl:    time.Minute,
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("random error = %v, want io.ErrUnexpectedEOF", err)
	}
	if _, statErr := os.Lstat(filepath.Join(runtimeDir, LocalOperatorTokenFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("token exists after random failure: %v", statErr)
	}
}

func TestPublicLocalOperatorCredentialAPI(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	issuer, err := LoadOrCreateLocalOperatorCredential(runtimeDir, "workspace-a")
	if err != nil {
		t.Fatalf("LoadOrCreateLocalOperatorCredential: %v", err)
	}
	token, err := ReadLocalOperatorToken(runtimeDir)
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	operator, err := IssueOperator(issuer, token, "workspace-a", localCredentialTestAction)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	admission, err := issuer.NewAdmission(OperatorOnly(localCredentialTestAction))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	if err := admission.RequireOperator(localCredentialTestAction, "workspace-a", operator); err != nil {
		t.Fatalf("RequireOperator: %v", err)
	}
}

type lockedReader struct {
	mu     sync.Mutex
	reader io.Reader
}

func (r *lockedReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reader.Read(p)
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func newLocalOperatorTestIssuer(t *testing.T, runtimeDir, workspace string, now *time.Time, ttl time.Duration) (*LocalOperatorIssuer, string) {
	t.Helper()
	issuer, err := loadOrCreateLocalOperatorCredential(runtimeDir, workspace, localOperatorDependencies{
		random: bytes.NewReader(bytes.Repeat([]byte{0x5a}, localOperatorTokenBytes)),
		now:    func() time.Time { return *now },
		ttl:    ttl,
	})
	if err != nil {
		t.Fatalf("loadOrCreateLocalOperatorCredential: %v", err)
	}
	token, err := ReadLocalOperatorToken(runtimeDir)
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	return issuer, token
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func credentialModeOrZero(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}
