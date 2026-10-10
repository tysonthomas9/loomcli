package agentsv1

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Tokens issues and checks the bridge and daemon tokens (design v2 §6.1).
// Each agent's bridge and the daemon get a server-issued token bound to one
// workspace; the API derives the caller from that token alone, never from a
// header, body field, tool argument or harness metadata. A token has no
// expiry: it is revoked by its agent being archived or deleted.
type Tokens struct{ key []byte }

// NewTokens returns Tokens signed with key.
func NewTokens(key []byte) *Tokens { return &Tokens{key: key} }

// LoadTokens reads the server's token key from path, creating a random one
// (mode 0600) the first time, so issued tokens survive a serve restart.
func LoadTokens(path string) (*Tokens, error) {
	path = filepath.Clean(path)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	err := os.WriteFile(path+".new", key, 0o600)
	if err == nil {
		// Link fails if a key already exists; the existing key wins.
		if err = os.Link(path+".new", path); errors.Is(err, os.ErrExist) {
			err = nil
		}
		_ = os.Remove(path + ".new")
	}
	if err != nil {
		return nil, err
	}
	if key, err = os.ReadFile(path); err != nil {
		return nil, err
	}
	if len(key) < 32 {
		return nil, errors.New("agentsv1: token key " + path + " is too short")
	}
	return NewTokens(key), nil
}

// Agent returns the bridge token of agentID in workspace.
func (t *Tokens) Agent(workspace, agentID string) string {
	return t.sign("agent", workspace, agentID)
}

// Daemon returns the daemon's token for workspace.
func (t *Tokens) Daemon(workspace string) string { return t.sign("system", workspace, "daemon") }

const tokenPrefix = middleware.BridgeTokenPrefix

func (t *Tokens) sign(kind, workspace, id string) string {
	body := base64.RawURLEncoding.EncodeToString([]byte(kind + "\x00" + workspace + "\x00" + id))
	return tokenPrefix + body + "." + base64.RawURLEncoding.EncodeToString(t.mac(body))
}

func (t *Tokens) mac(body string) []byte {
	m := hmac.New(sha256.New, t.key)
	m.Write([]byte(body))
	return m.Sum(nil)
}

// verify returns the caller and workspace a valid token names.
func (t *Tokens) verify(tok string) (loomagent.ActorRef, string, bool) {
	body, sig, ok := strings.Cut(strings.TrimPrefix(tok, tokenPrefix), ".")
	mac, err := base64.RawURLEncoding.DecodeString(sig)
	if t == nil || !ok || err != nil || !hmac.Equal(mac, t.mac(body)) {
		return loomagent.ActorRef{}, "", false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		return loomagent.ActorRef{}, "", false
	}
	return loomagent.ActorRef{Kind: parts[0], ID: parts[2]}, parts[1], true
}

type callerKey struct{}

// caller resolves the request's caller: a bridge or daemon token from the
// Authorization header, else the verified signed-in user, else the local
// user. A bridge token must be valid, for this workspace, and name an agent
// there that is neither archived nor deleted.
func (h *Handler) caller(r *http.Request, s *loomagent.Service) (loomagent.ActorRef, bool) {
	if tok, ok := bridgeToken(r); ok {
		c, ws, ok := h.tokens.verify(tok)
		if !ok || ws != middleware.WorkspaceFromContext(r.Context()) {
			return c, false
		}
		if c.Kind == "agent" {
			a, err := s.Get(r.Context(), c.ID)
			return c, err == nil && a.Agent.State != loomagent.StateArchived && a.Agent.DeletedAt == nil
		}
		return c, true
	}
	if _, id, ok := middleware.VerifiedUserActorFromContext(r.Context()); ok {
		return loomagent.ActorRef{Kind: "user", ID: id}, true
	}
	return loomagent.ActorRef{Kind: "user", ID: "local"}, true
}

// bridgeToken returns the request's bearer token when it is a bridge or
// daemon token.
func bridgeToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") && strings.HasPrefix(auth[7:], tokenPrefix) {
		return auth[7:], true
	}
	return "", false
}

// actor is the caller serve resolved from the authenticated request.
func actor(r *http.Request) loomagent.ActorRef {
	c, _ := r.Context().Value(callerKey{}).(loomagent.ActorRef)
	return c
}

// ownChild fails with agent_not_found unless agent id is a child of the
// calling agent: a bridge reaches only its own children (design v2 §6.1).
func ownChild(ctx context.Context, s *loomagent.Service, caller, id string) error {
	a, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if p := a.Agent.ParentAgentID; p == nil || *p != caller {
		return &loomagent.Error{Code: loomagent.CodeAgentNotFound, Message: id}
	}
	return nil
}
