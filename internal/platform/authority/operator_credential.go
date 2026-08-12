package authority

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// LocalOperatorTokenFileName is the separate runtime file used by local and
	// open-mode operator clients. It contains only one 256-bit hex token.
	LocalOperatorTokenFileName = "operator.token"
	// LocalFleetDBServiceTokenFileName is the distinct local service credential
	// used by Loom to authenticate to its embedded FleetDB. It must never share
	// the operator token: the FleetDB credential is a global service identity,
	// while the operator token is admitted only to scoped product commands.
	LocalFleetDBServiceTokenFileName = "fleetdb-service.token"
	// LocalOperatorAuthorityTTL keeps a verified bearer useful only for the
	// immediate command being admitted. The durable token remains on disk; the
	// derived authority does not.
	LocalOperatorAuthorityTTL = time.Minute

	localOperatorTokenBytes = 32
	localOperatorSubject    = "local-operator"
)

var (
	// ErrInvalidLocalOperatorIssuer means the credential issuer is nil,
	// uninitialized, or configured with invalid dependencies.
	ErrInvalidLocalOperatorIssuer = errors.New("authority: invalid local operator issuer")
	// ErrInvalidOperatorToken means a presented or persisted token is empty,
	// malformed, the wrong length, or does not match in constant time.
	ErrInvalidOperatorToken = errors.New("authority: invalid local operator token")
	// ErrInsecureOperatorCredential means the runtime directory or token file
	// has an unsafe type or permission mode.
	ErrInsecureOperatorCredential = errors.New("authority: insecure local operator credential")
)

// LocalOperatorIssuer owns the local/open-mode credential verification state.
// Its expected token and server-derived workspace exist only in memory. The
// associated Issuer seals both derived authorities and admission registries.
type LocalOperatorIssuer struct {
	issuer      *Issuer
	workspace   string
	runtimeWide bool
	token       [localOperatorTokenBytes]byte
	ttl         time.Duration
}

// LoadOrCreateLocalOperatorCredential securely loads or atomically creates the
// local/open-mode operator token and binds it in memory to the workspace
// derived by the server. The workspace is never persisted in the token file.
func LoadOrCreateLocalOperatorCredential(runtimeDir, serverDerivedWorkspace string) (*LocalOperatorIssuer, error) {
	return loadOrCreateLocalOperatorCredential(runtimeDir, serverDerivedWorkspace, localOperatorDependencies{
		random: rand.Reader,
		now:    time.Now,
		ttl:    LocalOperatorAuthorityTTL,
	})
}

// LoadOrCreateLocalRuntimeOperatorCredential creates the same durable local
// credential without binding it to one startup workspace. The credential
// represents the local runtime operator; each request is still narrowed to the
// exact canonical workspace supplied by server middleware and one action.
func LoadOrCreateLocalRuntimeOperatorCredential(runtimeDir string) (*LocalOperatorIssuer, error) {
	return loadOrCreateLocalOperatorCredentialScope(runtimeDir, "", true, localOperatorDependencies{
		random: rand.Reader,
		now:    time.Now,
		ttl:    LocalOperatorAuthorityTTL,
	})
}

// LoadOrCreateLocalRuntimeOperatorCredentialWithIssuer is the composition-root
// form used when system and operator authorities must share one Admission
// seal. The caller-owned issuer is never exposed to transports or modules.
func LoadOrCreateLocalRuntimeOperatorCredentialWithIssuer(runtimeDir string, issuer *Issuer) (*LocalOperatorIssuer, error) {
	if err := issuer.validate(); err != nil {
		return nil, err
	}
	return loadOrCreateLocalOperatorCredentialScope(runtimeDir, "", true, localOperatorDependencies{
		random: rand.Reader,
		now:    issuer.now,
		ttl:    LocalOperatorAuthorityTTL,
		issuer: issuer,
	})
}

// ReadLocalOperatorToken returns the validated hex token for an explicitly
// configured local CLI. It does not create a missing runtime directory or
// token and never returns an authority value.
func ReadLocalOperatorToken(runtimeDir string) (string, error) {
	dir, err := prepareLocalOperatorRuntimeDir(runtimeDir, false)
	if err != nil {
		return "", err
	}
	token, _, err := readLocalOperatorTokenFile(dir)
	return token, err
}

// LoadOrCreateLocalFleetDBServiceCredential securely loads or atomically
// creates the persistent credential used by Loom's embedded FleetDB client.
// Callers must provide a service-only directory separate from the local
// operator credential directory.
func LoadOrCreateLocalFleetDBServiceCredential(runtimeDir string) (string, error) {
	dir, err := prepareLocalOperatorRuntimeDir(runtimeDir, true)
	if err != nil {
		return "", err
	}
	token, _, err := loadOrCreateLocalCredentialTokenFile(
		dir,
		LocalFleetDBServiceTokenFileName,
		".fleetdb-service.token-*",
		rand.Reader,
	)
	return token, err
}

// ReadLocalFleetDBServiceCredential loads an existing, validated embedded
// FleetDB service credential without creating filesystem state.
func ReadLocalFleetDBServiceCredential(runtimeDir string) (string, error) {
	dir, err := prepareLocalOperatorRuntimeDir(runtimeDir, false)
	if err != nil {
		return "", err
	}
	token, _, err := readLocalCredentialTokenFile(dir, LocalFleetDBServiceTokenFileName)
	return token, err
}

// NewAdmission constructs an operation registry sealed to authorities issued
// by this local credential issuer.
func (i *LocalOperatorIssuer) NewAdmission(rules ...OperationRule) (*Admission, error) {
	if err := i.validate(); err != nil {
		return nil, err
	}
	return i.issuer.NewAdmission(rules...)
}

// IssueOperator verifies a raw token or a complete "Bearer <token>" value in
// constant time, checks the server-bound workspace, and returns a short-lived,
// action-scoped opaque OperatorAuthority.
func IssueOperator(issuer *LocalOperatorIssuer, presentedBearer, requestedWorkspace string, action Action) (OperatorAuthority, error) {
	if err := issuer.validate(); err != nil {
		return OperatorAuthority{}, err
	}
	presented, decodeErr := decodePresentedOperatorBearer(presentedBearer)
	if decodeErr != nil {
		// Keep a fixed-size comparison in the malformed-token path as well. Token
		// syntax is public, while the expected value remains timing-independent.
		var zero [localOperatorTokenBytes]byte
		_ = subtle.ConstantTimeCompare(issuer.token[:], zero[:])
		return OperatorAuthority{}, decodeErr
	}
	if subtle.ConstantTimeCompare(issuer.token[:], presented[:]) != 1 {
		return OperatorAuthority{}, ErrInvalidOperatorToken
	}
	requestedWorkspace = strings.TrimSpace(requestedWorkspace)
	if requestedWorkspace == "" {
		return OperatorAuthority{}, fmt.Errorf("%w: workspace is required", ErrInvalidScope)
	}
	if !issuer.runtimeWide && requestedWorkspace != issuer.workspace {
		return OperatorAuthority{}, fmt.Errorf("%w: requested %q, server-derived %q", ErrWorkspaceMismatch, requestedWorkspace, issuer.workspace)
	}
	now := issuer.issuer.now()
	principal, err := issuer.issuer.DeriveVerifiedPrincipal(PrincipalClaims{
		Subject:   localOperatorSubject,
		Class:     ClassOperator,
		Workspace: requestedWorkspace,
		Actions:   []Action{action},
		ExpiresAt: now.Add(issuer.ttl),
	})
	if err != nil {
		return OperatorAuthority{}, err
	}
	return issuer.issuer.IssueOperator(principal, requestedWorkspace, action)
}

func (i *LocalOperatorIssuer) validate() error {
	if i == nil || i.issuer == nil || (!i.runtimeWide && i.workspace == "") || i.ttl <= 0 {
		return ErrInvalidLocalOperatorIssuer
	}
	if err := i.issuer.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidLocalOperatorIssuer, err)
	}
	return nil
}

type localOperatorDependencies struct {
	random io.Reader
	now    func() time.Time
	ttl    time.Duration
	issuer *Issuer
}

func loadOrCreateLocalOperatorCredential(runtimeDir, serverDerivedWorkspace string, deps localOperatorDependencies) (*LocalOperatorIssuer, error) {
	return loadOrCreateLocalOperatorCredentialScope(runtimeDir, serverDerivedWorkspace, false, deps)
}

func loadOrCreateLocalOperatorCredentialScope(runtimeDir, serverDerivedWorkspace string, runtimeWide bool, deps localOperatorDependencies) (*LocalOperatorIssuer, error) {
	workspace := strings.TrimSpace(serverDerivedWorkspace)
	if workspace == "" && !runtimeWide {
		return nil, fmt.Errorf("%w: server-derived workspace is required", ErrInvalidScope)
	}
	if deps.random == nil || deps.now == nil || deps.ttl <= 0 {
		return nil, ErrInvalidLocalOperatorIssuer
	}
	dir, err := prepareLocalOperatorRuntimeDir(runtimeDir, true)
	if err != nil {
		return nil, err
	}
	_, token, err := loadOrCreateLocalOperatorTokenFile(dir, deps.random)
	if err != nil {
		return nil, err
	}
	authorityIssuer := deps.issuer
	if authorityIssuer == nil {
		authorityIssuer, err = NewIssuerWithClock(deps.now)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidLocalOperatorIssuer, err)
		}
	} else if err := authorityIssuer.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLocalOperatorIssuer, err)
	}
	return &LocalOperatorIssuer{
		issuer:      authorityIssuer,
		workspace:   workspace,
		runtimeWide: runtimeWide,
		token:       token,
		ttl:         deps.ttl,
	}, nil
}

func prepareLocalOperatorRuntimeDir(runtimeDir string, create bool) (string, error) {
	if strings.TrimSpace(runtimeDir) == "" {
		return "", fmt.Errorf("%w: runtime directory is required", ErrInvalidScope)
	}
	dir := filepath.Clean(runtimeDir)
	info, err := os.Lstat(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) || !create {
			return "", fmt.Errorf("local operator runtime directory: %w", err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create local operator runtime directory: %w", err)
		}
		// MkdirAll is affected by the process umask. Set the leaf explicitly so
		// the server-owned directory is usable and never broader than 0700.
		// #nosec G302 -- directories require execute bits; 0700 is the intended maximum.
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", fmt.Errorf("secure local operator runtime directory: %w", err)
		}
		info, err = os.Lstat(dir)
		if err != nil {
			return "", fmt.Errorf("stat local operator runtime directory: %w", err)
		}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%w: runtime path must be a real directory", ErrInsecureOperatorCredential)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%w: runtime directory mode is %04o, want no broader than 0700", ErrInsecureOperatorCredential, info.Mode().Perm())
	}
	return dir, nil
}

func loadOrCreateLocalOperatorTokenFile(runtimeDir string, random io.Reader) (string, [localOperatorTokenBytes]byte, error) {
	return loadOrCreateLocalCredentialTokenFile(runtimeDir, LocalOperatorTokenFileName, ".operator.token-*", random)
}

func loadOrCreateLocalCredentialTokenFile(runtimeDir, fileName, temporaryPattern string, random io.Reader) (string, [localOperatorTokenBytes]byte, error) {
	token, decoded, err := readLocalCredentialTokenFile(runtimeDir, fileName)
	if err == nil {
		return token, decoded, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", [localOperatorTokenBytes]byte{}, err
	}
	return createLocalCredentialTokenFile(runtimeDir, fileName, temporaryPattern, random)
}

func createLocalOperatorTokenFile(runtimeDir string, random io.Reader) (string, [localOperatorTokenBytes]byte, error) {
	return createLocalCredentialTokenFile(runtimeDir, LocalOperatorTokenFileName, ".operator.token-*", random)
}

func createLocalCredentialTokenFile(runtimeDir, fileName, temporaryPattern string, random io.Reader) (string, [localOperatorTokenBytes]byte, error) {
	var generated [localOperatorTokenBytes]byte
	if _, err := io.ReadFull(random, generated[:]); err != nil {
		return "", generated, fmt.Errorf("generate local operator token: %w", err)
	}
	token := hex.EncodeToString(generated[:])
	temporary, err := os.CreateTemp(runtimeDir, temporaryPattern)
	if err != nil {
		return "", generated, fmt.Errorf("create local operator token temporary file: %w", err)
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
		return "", generated, fmt.Errorf("chmod local operator token temporary file: %w", closeWithError(err))
	}
	if _, err := io.WriteString(temporary, token); err != nil {
		return "", generated, fmt.Errorf("write local operator token temporary file: %w", closeWithError(err))
	}
	if err := temporary.Sync(); err != nil {
		return "", generated, fmt.Errorf("sync local operator token temporary file: %w", closeWithError(err))
	}
	if err := temporary.Close(); err != nil {
		return "", generated, fmt.Errorf("close local operator token temporary file: %w", err)
	}
	path := filepath.Join(runtimeDir, fileName)
	if err := os.Link(temporaryPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			// Another server startup won the atomic link. Its file was fully
			// written and synced before it became visible, so reuse it safely.
			return readLocalCredentialTokenFile(runtimeDir, fileName)
		}
		return "", generated, fmt.Errorf("publish local operator token: %w", err)
	}
	return readLocalCredentialTokenFile(runtimeDir, fileName)
}

func readLocalOperatorTokenFile(runtimeDir string) (string, [localOperatorTokenBytes]byte, error) {
	return readLocalCredentialTokenFile(runtimeDir, LocalOperatorTokenFileName)
}

func readLocalCredentialTokenFile(runtimeDir, fileName string) (string, [localOperatorTokenBytes]byte, error) {
	path := filepath.Join(runtimeDir, fileName)
	info, err := os.Lstat(path)
	if err != nil {
		return "", [localOperatorTokenBytes]byte{}, fmt.Errorf("stat local operator token: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", [localOperatorTokenBytes]byte{}, fmt.Errorf("%w: token path must be a regular file", ErrInsecureOperatorCredential)
	}
	if info.Mode().Perm() != 0o600 {
		return "", [localOperatorTokenBytes]byte{}, fmt.Errorf("%w: token file mode is %04o, want 0600", ErrInsecureOperatorCredential, info.Mode().Perm())
	}
	file, err := os.Open(path) // #nosec G304 -- path is rooted in the configured runtime and was lstat-validated above.
	if err != nil {
		return "", [localOperatorTokenBytes]byte{}, fmt.Errorf("open local operator token: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", [localOperatorTokenBytes]byte{}, fmt.Errorf("stat opened local operator token: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm() != 0o600 || !os.SameFile(info, openedInfo) {
		return "", [localOperatorTokenBytes]byte{}, fmt.Errorf("%w: opened token file is not a mode-0600 regular file", ErrInsecureOperatorCredential)
	}
	contents, err := io.ReadAll(io.LimitReader(file, int64(hex.EncodedLen(localOperatorTokenBytes)+1)))
	if err != nil {
		return "", [localOperatorTokenBytes]byte{}, fmt.Errorf("read local operator token: %w", err)
	}
	if len(contents) != hex.EncodedLen(localOperatorTokenBytes) {
		return "", [localOperatorTokenBytes]byte{}, fmt.Errorf("%w: persisted token must contain exactly %d hex characters", ErrInvalidOperatorToken, hex.EncodedLen(localOperatorTokenBytes))
	}
	var decoded [localOperatorTokenBytes]byte
	if _, err := hex.Decode(decoded[:], contents); err != nil {
		return "", decoded, fmt.Errorf("%w: persisted token is not hex", ErrInvalidOperatorToken)
	}
	return string(contents), decoded, nil
}

func decodePresentedOperatorBearer(value string) ([localOperatorTokenBytes]byte, error) {
	var decoded [localOperatorTokenBytes]byte
	fields := strings.Fields(value)
	switch {
	case len(fields) == 1:
		value = fields[0]
	case len(fields) == 2 && strings.EqualFold(fields[0], "bearer"):
		value = fields[1]
	default:
		return decoded, ErrInvalidOperatorToken
	}
	if len(value) != hex.EncodedLen(localOperatorTokenBytes) {
		return decoded, ErrInvalidOperatorToken
	}
	if _, err := hex.Decode(decoded[:], []byte(value)); err != nil {
		return decoded, ErrInvalidOperatorToken
	}
	return decoded, nil
}
