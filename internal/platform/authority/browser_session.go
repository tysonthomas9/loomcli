package authority

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	// LocalBrowserLaunchTTL bounds how long a Desktop-created launch code can
	// remain in a URL fragment before the SPA exchanges it. Launch codes are
	// single-use and are never accepted as product-operation credentials.
	LocalBrowserLaunchTTL = 30 * time.Second
	// LocalBrowserSessionTTL bounds the in-memory browser bearer. Refreshing or
	// reopening a raw loopback URL loses it; the trusted Desktop launcher must
	// mint a new launch code.
	LocalBrowserSessionTTL = 15 * time.Minute

	localBrowserSubject = "local-browser-operator"
)

var localBrowserLaunchAction Action = "platform.local-browser-session.create"

// LocalBrowserLaunch is the short-lived, single-use value a trusted local
// launcher places in the URL fragment. The server stores only its SHA-256
// digest. Workspace is server-derived and carried separately so the SPA can
// use the canonical workspace-scoped exchange route.
type LocalBrowserLaunch struct {
	Code      string
	Workspace string
	ExpiresAt time.Time
}

// LocalBrowserSession is the short-lived bearer returned after a successful
// launch-code exchange. It exists only in server memory and the SPA's existing
// in-memory auth slot; it is never written to disk or browser storage.
type LocalBrowserSession struct {
	Bearer    string
	Workspace string
	ExpiresAt time.Time
}

type localBrowserGrant struct {
	workspace string
	expiresAt time.Time
}

// LocalBrowserSessionBroker delegates only an explicit action set from the
// durable local operator credential to a process-bound browser session. It is
// sealed to the same Issuer as the capability Admission registry.
type LocalBrowserSessionBroker struct {
	issuer     *LocalOperatorIssuer
	random     io.Reader
	now        func() time.Time
	launchTTL  time.Duration
	sessionTTL time.Duration
	actions    map[Action]struct{}

	mu       sync.Mutex
	launches map[[sha256.Size]byte]localBrowserGrant
	sessions map[[sha256.Size]byte]localBrowserGrant
}

// NewLocalBrowserSessionBroker constructs the local-only launch-code broker.
// The action set is exact and wildcard-free; an empty set fails closed.
func NewLocalBrowserSessionBroker(issuer *LocalOperatorIssuer, actions ...Action) (*LocalBrowserSessionBroker, error) {
	return newLocalBrowserSessionBroker(issuer, rand.Reader, issuerClock(issuer), LocalBrowserLaunchTTL, LocalBrowserSessionTTL, actions...)
}

func newLocalBrowserSessionBroker(issuer *LocalOperatorIssuer, random io.Reader, now func() time.Time, launchTTL, sessionTTL time.Duration, actions ...Action) (*LocalBrowserSessionBroker, error) {
	if err := issuer.validate(); err != nil {
		return nil, err
	}
	if random == nil || now == nil || launchTTL <= 0 || sessionTTL <= 0 {
		return nil, ErrInvalidLocalOperatorIssuer
	}
	allowed := make(map[Action]struct{}, len(actions))
	for _, value := range actions {
		action, err := normalizeAction(value)
		if err != nil {
			return nil, fmt.Errorf("local browser session action: %w", err)
		}
		allowed[action] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("%w: local browser session requires at least one exact action", ErrInvalidScope)
	}
	return &LocalBrowserSessionBroker{
		issuer: issuer, random: random, now: now, launchTTL: launchTTL, sessionTTL: sessionTTL,
		actions: allowed, launches: make(map[[sha256.Size]byte]localBrowserGrant), sessions: make(map[[sha256.Size]byte]localBrowserGrant),
	}, nil
}

func issuerClock(issuer *LocalOperatorIssuer) func() time.Time {
	if issuer == nil || issuer.issuer == nil {
		return nil
	}
	return issuer.issuer.now
}

// MintLaunchCode verifies the durable local operator bearer and creates one
// process-bound launch code. The returned code is not an OperatorAuthority and
// cannot authorize a lifecycle command until exchanged.
func (b *LocalBrowserSessionBroker) MintLaunchCode(presentedBearer, serverDerivedWorkspace string) (LocalBrowserLaunch, error) {
	if err := b.validate(); err != nil {
		return LocalBrowserLaunch{}, err
	}
	workspace := strings.TrimSpace(serverDerivedWorkspace)
	if _, err := IssueOperator(b.issuer, presentedBearer, workspace, localBrowserLaunchAction); err != nil {
		return LocalBrowserLaunch{}, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.purgeExpiredLocked()
	code, digest, err := b.newTokenLocked()
	if err != nil {
		return LocalBrowserLaunch{}, fmt.Errorf("generate local browser launch code: %w", err)
	}
	expiresAt := b.now().Add(b.launchTTL)
	b.launches[digest] = localBrowserGrant{workspace: workspace, expiresAt: expiresAt}
	return LocalBrowserLaunch{Code: code, Workspace: workspace, ExpiresAt: expiresAt}, nil
}

// ExchangeLaunchCode consumes one launch code and returns a distinct browser
// bearer. Consumption happens before token generation so a failed exchange can
// never replay the launch code.
func (b *LocalBrowserSessionBroker) ExchangeLaunchCode(code, serverDerivedWorkspace string) (LocalBrowserSession, error) {
	if err := b.validate(); err != nil {
		return LocalBrowserSession{}, err
	}
	workspace := strings.TrimSpace(serverDerivedWorkspace)
	if workspace == "" {
		return LocalBrowserSession{}, ErrInvalidScope
	}
	digest, err := localBrowserTokenDigest(code)
	if err != nil {
		return LocalBrowserSession{}, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	grant, ok := b.launches[digest]
	delete(b.launches, digest)
	if !ok || !now.Before(grant.expiresAt) {
		return LocalBrowserSession{}, ErrInvalidOperatorToken
	}
	if grant.workspace != workspace {
		return LocalBrowserSession{}, ErrWorkspaceMismatch
	}
	b.purgeExpiredLocked()
	bearer, sessionDigest, err := b.newTokenLocked()
	if err != nil {
		return LocalBrowserSession{}, fmt.Errorf("generate local browser session: %w", err)
	}
	expiresAt := now.Add(b.sessionTTL)
	// A runtime-wide durable issuer may mint launches for multiple workspaces,
	// but each exchanged browser session retains the workspace of its launch.
	b.sessions[sessionDigest] = localBrowserGrant{workspace: workspace, expiresAt: expiresAt}
	return LocalBrowserSession{Bearer: bearer, Workspace: workspace, ExpiresAt: expiresAt}, nil
}

// IssueOperator verifies a browser session bearer and derives a fresh
// one-minute, exact-workspace/exact-action OperatorAuthority from the same
// issuer seal used by the Workflow Catalog Admission registry.
func (b *LocalBrowserSessionBroker) IssueOperator(presentedBearer, serverDerivedWorkspace string, action Action) (OperatorAuthority, error) {
	if err := b.validate(); err != nil {
		return OperatorAuthority{}, err
	}
	action, err := normalizeAction(action)
	if err != nil {
		return OperatorAuthority{}, err
	}
	if _, ok := b.actions[action]; !ok {
		return OperatorAuthority{}, ErrActionNotAllowed
	}
	workspace := strings.TrimSpace(serverDerivedWorkspace)
	if workspace == "" {
		return OperatorAuthority{}, ErrInvalidScope
	}
	digest, err := localBrowserTokenDigest(presentedBearer)
	if err != nil {
		return OperatorAuthority{}, err
	}

	b.mu.Lock()
	now := b.now()
	grant, ok := b.sessions[digest]
	if !ok || !now.Before(grant.expiresAt) {
		delete(b.sessions, digest)
		b.mu.Unlock()
		return OperatorAuthority{}, ErrInvalidOperatorToken
	}
	b.mu.Unlock()
	if grant.workspace != "" && grant.workspace != workspace {
		return OperatorAuthority{}, ErrWorkspaceMismatch
	}
	expiresAt := now.Add(b.issuer.ttl)
	if grant.expiresAt.Before(expiresAt) {
		expiresAt = grant.expiresAt
	}
	principal, err := b.issuer.issuer.DeriveVerifiedPrincipal(PrincipalClaims{
		Subject: localBrowserSubject, Class: ClassOperator, Workspace: workspace,
		Actions: []Action{action}, ExpiresAt: expiresAt,
	})
	if err != nil {
		return OperatorAuthority{}, err
	}
	return b.issuer.issuer.IssueOperator(principal, workspace, action)
}

func (b *LocalBrowserSessionBroker) validate() error {
	if b == nil || b.random == nil || b.now == nil || b.launchTTL <= 0 || b.sessionTTL <= 0 || len(b.actions) == 0 || b.launches == nil || b.sessions == nil {
		return ErrInvalidLocalOperatorIssuer
	}
	return b.issuer.validate()
}

func (b *LocalBrowserSessionBroker) newTokenLocked() (string, [sha256.Size]byte, error) {
	var raw [localOperatorTokenBytes]byte
	if _, err := io.ReadFull(b.random, raw[:]); err != nil {
		return "", [sha256.Size]byte{}, err
	}
	return hex.EncodeToString(raw[:]), sha256.Sum256(raw[:]), nil
}

func (b *LocalBrowserSessionBroker) purgeExpiredLocked() {
	now := b.now()
	for digest, grant := range b.launches {
		if !now.Before(grant.expiresAt) {
			delete(b.launches, digest)
		}
	}
	for digest, grant := range b.sessions {
		if !now.Before(grant.expiresAt) {
			delete(b.sessions, digest)
		}
	}
}

func localBrowserTokenDigest(presented string) ([sha256.Size]byte, error) {
	raw, err := decodePresentedOperatorBearer(presented)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(raw[:]), nil
}
