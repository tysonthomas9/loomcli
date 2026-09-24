package browserauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Env vars Loom injects into an interactive agent's PTY at spawn time. They
// are never written to tab metadata (which is served to the UI) and die with
// the PTY.
const (
	EnvAgentSessionToken = "LOOM_AGENT_BROWSER_SESSION" //nolint:gosec // env var name, not a credential
	EnvAgentBrowserURL   = "LOOM_AGENT_BROWSER_URL"
)

// AgentSessionHeader carries the agent-session bearer on agent browser routes.
const AgentSessionHeader = "X-Loom-Agent-Session"

// maxAgentBindings bounds registry growth; each live interactive terminal
// holds one binding.
const maxAgentBindings = 4096

// AgentBinding is the server-side record tying an agent-session bearer to one
// (workspace, interactive agent, terminal). Loom creates it only when it
// spawns the agent's PTY from server-built launch metadata.
type AgentBinding struct {
	ID                    string // opaque; becomes the delegation session_id
	Workspace             string
	AgentName             string
	OrchestratorSessionID string
	TerminalID            string
	IssuedAt              time.Time
}

type terminalRef struct{ workspace, terminal string }

// AgentSessionRegistry holds the live agent-session bindings in memory. A
// Loom restart invalidates every binding, as does the PTY ending.
type AgentSessionRegistry struct {
	mu         sync.Mutex
	byHash     map[[32]byte]AgentBinding
	byTerminal map[terminalRef][32]byte
	now        func() time.Time
}

// NewAgentSessionRegistry returns an empty registry.
func NewAgentSessionRegistry() *AgentSessionRegistry {
	return &AgentSessionRegistry{
		byHash:     map[[32]byte]AgentBinding{},
		byTerminal: map[terminalRef][32]byte{},
		now:        time.Now,
	}
}

// Issue binds a new bearer to (workspace, agent, terminal). A terminal holds
// at most one binding; re-issuing for the same terminal revokes the old one.
func (r *AgentSessionRegistry) Issue(workspace, agentName, orchestratorSessionID, terminalID string) (string, AgentBinding, error) {
	if r == nil {
		return "", AgentBinding{}, fmt.Errorf("agent session registry: %w", domain.ErrBrowserUnavailable)
	}
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(agentName) == "" || strings.TrimSpace(terminalID) == "" {
		return "", AgentBinding{}, errors.New("agent session binding requires workspace, agent and terminal")
	}
	token, hash, err := newBearer()
	if err != nil {
		return "", AgentBinding{}, err
	}
	id, err := randomID()
	if err != nil {
		return "", AgentBinding{}, err
	}
	b := AgentBinding{ID: "abs_" + id, Workspace: workspace, AgentName: agentName,
		OrchestratorSessionID: orchestratorSessionID, TerminalID: terminalID, IssuedAt: r.now().UTC()}
	ref := terminalRef{workspace, terminalID}

	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.byTerminal[ref]; ok {
		delete(r.byHash, old)
	}
	if len(r.byHash) >= maxAgentBindings {
		return "", AgentBinding{}, errors.New("agent session registry is full")
	}
	r.byHash[hash] = b
	r.byTerminal[ref] = hash
	return token, b, nil
}

// Resolve returns the binding for token. Unknown, revoked or malformed
// bearers return domain.ErrBrowserUnauthorized.
func (r *AgentSessionRegistry) Resolve(token string) (AgentBinding, error) {
	if r == nil {
		return AgentBinding{}, fmt.Errorf("agent session registry: %w", domain.ErrBrowserUnavailable)
	}
	hash, ok := hashBearer(token)
	if !ok {
		return AgentBinding{}, fmt.Errorf("agent session: %w", domain.ErrBrowserUnauthorized)
	}
	r.mu.Lock()
	b, found := r.byHash[hash]
	r.mu.Unlock()
	if !found {
		return AgentBinding{}, fmt.Errorf("agent session not active: %w", domain.ErrBrowserUnauthorized)
	}
	return b, nil
}

// RevokeTerminal drops the binding for a terminal. It is called whenever the
// PTY ends (exit, kill, grace/idle reap, shutdown). Returns true when a
// binding was removed.
func (r *AgentSessionRegistry) RevokeTerminal(workspace, terminalID string) bool {
	if r == nil {
		return false
	}
	ref := terminalRef{workspace, terminalID}
	r.mu.Lock()
	defer r.mu.Unlock()
	hash, ok := r.byTerminal[ref]
	if !ok {
		return false
	}
	delete(r.byTerminal, ref)
	delete(r.byHash, hash)
	return true
}

// RevokeToken drops the binding for one bearer (the exact token a PTY was
// spawned with). Unknown or empty tokens are a no-op.
func (r *AgentSessionRegistry) RevokeToken(token string) bool {
	if r == nil {
		return false
	}
	hash, ok := hashBearer(token)
	if !ok {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, found := r.byHash[hash]
	if !found {
		return false
	}
	delete(r.byHash, hash)
	ref := terminalRef{b.Workspace, b.TerminalID}
	if cur, ok := r.byTerminal[ref]; ok && cur == hash {
		delete(r.byTerminal, ref)
	}
	return true
}

// Len reports the number of live bindings.
func (r *AgentSessionRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byHash)
}

// newBearer returns a 256-bit random bearer and its SHA-256. Only the hash is
// retained, so a memory dump of the maps does not reveal live bearers.
func newBearer() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, fmt.Errorf("generate bearer: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}

func hashBearer(token string) ([32]byte, bool) {
	token = strings.TrimSpace(token)
	if len(token) != base64.RawURLEncoding.EncodedLen(32) {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(token)), true
}
