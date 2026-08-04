package middleware

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	jwksRefreshInterval  = 5 * time.Minute
	jwksOnDemandCooldown = 10 * time.Second
	jwksNegativeCacheTTL = 30 * time.Second
	jwksMaxStaleness     = 24 * time.Hour
	jwksMaxResponseBytes = 64 * 1024 // 64KB
	jwksMaxKeys          = 10
	jwksHTTPTimeout      = 10 * time.Second
	jwksSingleflightKey  = "jwks-refresh"
	jwksMinRSABits       = 2048
)

// jwksResponse represents the JSON structure of a JWKS endpoint response.
type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

// jwkKey represents a single JWK entry.
type jwkKey struct {
	Kty string `json:"kty"` // Key type: "RSA"
	Kid string `json:"kid"` // Key ID
	Alg string `json:"alg"` // Algorithm: "RS256"
	Use string `json:"use"` // Key use: "sig"
	N   string `json:"n"`   // RSA modulus (base64url)
	E   string `json:"e"`   // RSA exponent (base64url)
}

// JWKSCache fetches and caches public keys from a JWKS endpoint.
type JWKSCache struct {
	endpoint string
	client   *http.Client
	logger   *slog.Logger

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey // kid → *rsa.PublicKey
	allKeys   []*rsa.PublicKey          // ordered list for kid-absent fallback
	lastFetch time.Time                 // for max staleness bound (24h)

	refreshGroup singleflight.Group
	lastOnDemand time.Time            // for 10-second cooldown between on-demand refreshes
	negCache     map[string]time.Time // kid → expiry time for negative cache (30s TTL)

	done chan struct{}
}

// NewJWKSCacheNoFetch creates a JWKSCache without an initial fetch or background
// refresh goroutine. The caller must call Fetch() to populate keys. This is
// intended for tests that need fine-grained control over fetch timing.
func NewJWKSCacheNoFetch(endpoint string, client *http.Client, logger *slog.Logger) *JWKSCache {
	if logger == nil {
		logger = slog.Default()
	}
	if client == nil {
		client = &http.Client{Timeout: jwksHTTPTimeout}
	}
	return &JWKSCache{
		endpoint: endpoint,
		client:   client,
		logger:   logger,
		keys:     make(map[string]*rsa.PublicKey),
		negCache: make(map[string]time.Time),
		done:     make(chan struct{}),
	}
}

// NewJWKSCache creates a new JWKS cache that fetches keys from the given endpoint.
// If client is nil, a default client with a 10s timeout is created.
// If logger is nil, slog.Default() is used.
// Performs an initial synchronous fetch (logs warning on failure, doesn't fail startup).
// Starts a background refresh goroutine.
func NewJWKSCache(endpoint string, client *http.Client, logger *slog.Logger) *JWKSCache {
	if logger == nil {
		logger = slog.Default()
	}
	if client == nil {
		client = &http.Client{
			Timeout: jwksHTTPTimeout,
		}
	}

	c := &JWKSCache{
		endpoint: endpoint,
		client:   client,
		logger:   logger,
		keys:     make(map[string]*rsa.PublicKey),
		negCache: make(map[string]time.Time),
		done:     make(chan struct{}),
	}

	// Initial synchronous fetch — log on failure but don't block startup.
	if err := c.fetch(context.Background()); err != nil {
		logger.Warn("JWKS initial fetch failed", "endpoint", endpoint, "error", err)
	}

	go c.refreshLoop(jwksRefreshInterval)
	return c
}

// GetKey returns the RSA public key for the given kid.
// If kid is empty, returns all cached keys for the caller to try.
// If kid is not found, checks negative cache, then triggers on-demand refresh.
func (c *JWKSCache) GetKey(kid string) ([]*rsa.PublicKey, error) {
	// Check max staleness first.
	c.mu.RLock()
	stale := !c.lastFetch.IsZero() && time.Since(c.lastFetch) > jwksMaxStaleness
	c.mu.RUnlock()
	if stale {
		// Try a refresh — if it fails, keys are stale and we must reject.
		if err := c.fetch(context.Background()); err != nil {
			c.mu.RLock()
			stillStale := time.Since(c.lastFetch) > jwksMaxStaleness
			c.mu.RUnlock()
			if stillStale {
				return nil, fmt.Errorf("JWKS cache expired (>%v without successful refresh): %w", jwksMaxStaleness, err)
			}
		}
	}

	// Empty kid: return all cached keys for caller to try each.
	if kid == "" {
		c.mu.RLock()
		keys := make([]*rsa.PublicKey, len(c.allKeys))
		copy(keys, c.allKeys)
		c.mu.RUnlock()
		if len(keys) == 0 {
			return nil, errors.New("JWKS cache is empty and no kid specified")
		}
		return keys, nil
	}

	// Lookup by kid.
	c.mu.RLock()
	key, ok := c.keys[kid]
	c.mu.RUnlock()
	if ok {
		return []*rsa.PublicKey{key}, nil
	}

	// Check negative cache.
	c.mu.RLock()
	if expiry, negHit := c.negCache[kid]; negHit && time.Now().Before(expiry) {
		c.mu.RUnlock()
		return nil, fmt.Errorf("unknown kid %q (negative cached)", kid)
	}
	c.mu.RUnlock()

	// On-demand refresh with cooldown.
	c.mu.RLock()
	cooldownActive := time.Since(c.lastOnDemand) < jwksOnDemandCooldown
	c.mu.RUnlock()
	if cooldownActive {
		return nil, fmt.Errorf("unknown kid %q (on-demand refresh on cooldown)", kid)
	}

	// Set cooldown before singleflight to close TOCTOU gap between check and write.
	c.mu.Lock()
	c.lastOnDemand = time.Now()
	c.mu.Unlock()

	// Trigger singleflight refresh.
	_, err, _ := c.refreshGroup.Do(jwksSingleflightKey, func() (interface{}, error) {
		return nil, c.fetch(context.Background())
	})
	if err != nil {
		return nil, fmt.Errorf("JWKS on-demand refresh failed: %w", err)
	}

	// Retry lookup after refresh.
	c.mu.RLock()
	key, ok = c.keys[kid]
	c.mu.RUnlock()
	if ok {
		return []*rsa.PublicKey{key}, nil
	}

	// Still not found — add to negative cache.
	c.mu.Lock()
	c.negCache[kid] = time.Now().Add(jwksNegativeCacheTTL)
	c.mu.Unlock()
	return nil, fmt.Errorf("unknown kid %q after JWKS refresh", kid)
}

// Fetch triggers a synchronous key refresh from the JWKS endpoint.
func (c *JWKSCache) Fetch(ctx context.Context) error {
	return c.fetch(ctx)
}

// Stop shuts down the background refresh goroutine.
func (c *JWKSCache) Stop() {
	select {
	case <-c.done:
		// Already stopped.
	default:
		close(c.done)
	}
}

// fetch retrieves and parses the JWKS from the endpoint.
// On success, updates the cache. On failure, retains existing cache.
func (c *JWKSCache) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating JWKS request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, jwksMaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("reading JWKS response: %w", err)
	}
	if int64(len(body)) > jwksMaxResponseBytes {
		return fmt.Errorf("JWKS response too large (>%d bytes)", jwksMaxResponseBytes)
	}

	var jwks jwksResponse
	if err := json.Unmarshal(body, &jwks); err != nil {
		return fmt.Errorf("parsing JWKS JSON: %w", err)
	}

	// Empty JWKS retains cache — defensive against misconfigured auth service.
	if len(jwks.Keys) == 0 {
		c.logger.Warn("JWKS endpoint returned empty keys array, retaining existing cache")
		return nil
	}

	newKeys := make(map[string]*rsa.PublicKey)
	var newAllKeys []*rsa.PublicKey
	for i, jk := range jwks.Keys {
		if i >= jwksMaxKeys {
			c.logger.Warn("JWKS response has more keys than maximum, skipping extras",
				"max", jwksMaxKeys, "total", len(jwks.Keys))
			break
		}
		pubKey, err := parseJWK(jk)
		if err != nil {
			c.logger.Warn("skipping invalid JWK", "kid", jk.Kid, "error", err)
			continue
		}
		newKeys[jk.Kid] = pubKey
		newAllKeys = append(newAllKeys, pubKey)
	}

	if len(newKeys) == 0 {
		c.logger.Warn("JWKS response had keys but none were valid, retaining existing cache")
		return nil
	}

	c.mu.Lock()
	c.keys = newKeys
	c.allKeys = newAllKeys
	c.lastFetch = time.Now()
	// Clear negative cache on successful refresh.
	c.negCache = make(map[string]time.Time)
	c.mu.Unlock()

	c.logger.Info("JWKS cache refreshed", "keys", len(newKeys))
	return nil
}

// refreshLoop runs the background refresh on a ticker.
func (c *JWKSCache) refreshLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if err := c.fetch(context.Background()); err != nil {
				c.logger.Warn("JWKS background refresh failed", "error", err)
			}
		}
	}
}

// parseJWK converts a single jwkKey into an *rsa.PublicKey.
// Only kty="RSA" with alg="RS256" is accepted. Keys must be >= 2048 bits.
func parseJWK(key jwkKey) (*rsa.PublicKey, error) {
	if key.Kty != "RSA" {
		return nil, fmt.Errorf("unsupported key type %q (only RSA)", key.Kty)
	}
	if key.Alg != "" && key.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm %q (only RS256)", key.Alg)
	}

	// Decode modulus (n) — base64url without padding.
	nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, fmt.Errorf("decoding RSA modulus (n): %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)

	// Decode exponent (e) — base64url without padding.
	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, fmt.Errorf("decoding RSA exponent (e): %w", err)
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() {
		return nil, fmt.Errorf("RSA exponent too large")
	}

	pubKey := &rsa.PublicKey{
		N: n,
		E: int(e.Int64()),
	}

	if pubKey.N.BitLen() < jwksMinRSABits {
		return nil, fmt.Errorf("RSA key too small: %d bits (minimum %d)", pubKey.N.BitLen(), jwksMinRSABits)
	}

	return pubKey, nil
}

// NewJWKSHTTPClient creates an HTTP client suitable for JWKS fetching.
// Accessible from same-package tests to allow custom dial functions.
func NewJWKSHTTPClient(dialCtx func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Client {
	return &http.Client{
		Timeout: jwksHTTPTimeout,
		Transport: &http.Transport{
			DialContext: dialCtx,
		},
	}
}
