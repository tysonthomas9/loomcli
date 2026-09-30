package pushproxy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/errcode"
)

const tokenLifetime = 5 * time.Minute

type Claims struct {
	Repo      string `json:"repo"`
	Workspace string `json:"workspace"`
	Attempt   string `json:"attempt"`
	Fence     int64  `json:"fence"`
	Expires   int64  `json:"exp"`
}

type Store interface {
	ProxyKey(context.Context) ([]byte, error)
	CurrentLease(context.Context, string) (loomgit.Lease, error)
}

func LeaseScope(workspace, attempt string) string { return "attempt:" + workspace + "/" + attempt }

func current(ctx context.Context, store Store, claims Claims, now time.Time) error {
	lease, err := store.CurrentLease(ctx, LeaseScope(claims.Workspace, claims.Attempt))
	if err != nil || lease.Fence != claims.Fence || !lease.ExpiresAt.After(now) {
		return errcode.New(errcode.Stale, "attempt lease expired or taken over", err)
	}
	return nil
}

// Mint creates a short-lived, attempt-scoped credential. It cannot outlive the lease.
func Mint(ctx context.Context, store Store, repo, workspace, attempt string, fence int64) (string, error) {
	if repo == "" || workspace == "" || attempt == "" || fence <= 0 {
		return "", errors.New("repo, workspace, attempt and fence are required")
	}
	now := time.Now().UTC()
	claims := Claims{Repo: repo, Workspace: workspace, Attempt: attempt, Fence: fence}
	if err := current(ctx, store, claims, now); err != nil {
		return "", err
	}
	lease, err := store.CurrentLease(ctx, LeaseScope(workspace, attempt))
	if err != nil {
		return "", err
	}
	exp := now.Add(tokenLifetime)
	if lease.ExpiresAt.Before(exp) {
		exp = lease.ExpiresAt
	}
	claims.Expires = exp.Unix()
	if claims.Expires <= now.Unix() {
		return "", errcode.New(errcode.Stale, "attempt lease expires too soon", nil)
	}
	key, err := store.ProxyKey(ctx)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	part := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(part))
	return part + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify rejects expired tokens and fences that have been replaced since minting.
func Verify(ctx context.Context, store Store, token string) (Claims, error) {
	var claims Claims
	part, signature, ok := strings.Cut(token, ".")
	if !ok || strings.Contains(signature, ".") || len(token) > 4096 {
		return claims, errors.New("invalid proxy token")
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return claims, errors.New("invalid proxy token")
	}
	key, err := store.ProxyKey(ctx)
	if err != nil {
		return claims, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(part))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return claims, errors.New("invalid proxy token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.Repo == "" || claims.Workspace == "" || claims.Attempt == "" || claims.Fence <= 0 {
		return Claims{}, errors.New("invalid proxy token")
	}
	now := time.Now().UTC()
	if claims.Expires <= now.Unix() || claims.Expires > now.Add(tokenLifetime).Unix() {
		return Claims{}, errcode.New(errcode.Stale, "proxy token expired", nil)
	}
	if err := current(ctx, store, claims, now); err != nil {
		return Claims{}, err
	}
	return claims, nil
}
