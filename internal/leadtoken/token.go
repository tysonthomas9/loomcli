// Package leadtoken mints and verifies occupant tokens for sandboxed lead
// runtimes.
//
// Occupant tokens are the lead equivalent of driver run tokens: HS256 JWTs
// signed by loom serve, bound to one node placement generation, and checked
// again against the store before any operation runs. Unlike
// driver.RunTokenClaims.Caps, occupant Caps are enforced by the lead API. The
// sandbox never receives fleet-db credentials; the token only grants the caps
// serve maps onto individual lead operations.
package leadtoken

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

const (
	// CapLeadSession authorizes the sandboxed lead to call lead session APIs.
	CapLeadSession = "lead:session"
	// CapLeadAssignment authorizes the sandboxed lead to read its assignment.
	CapLeadAssignment = "lead:assignment"
	// CapLeadInbox authorizes the sandboxed lead to drain its inbox.
	CapLeadInbox = "lead:inbox"
	// CapLeadData authorizes the sandboxed lead to read and write issue data
	// through the /lead/data mount. It never grants the general REST surface.
	CapLeadData = "lead:data"
	// CapLeadDispatch authorizes only the pinned epic-runner dispatch and
	// status surface. It never grants the general /workflows/ routes.
	CapLeadDispatch = "lead:dispatch"

	// DefaultOccupantTokenTTL is the default lifetime for a lead occupant
	// token. Tokens share driver.ResolveRunTokenSigningKey's signing key; an
	// ephemeral per-process key means every occupant token dies with serve.
	DefaultOccupantTokenTTL = time.Hour

	occupantSubjectPrefix = "lead-occupant:"
)

// ErrOccupantTokenInvalid indicates an occupant token failed validation. It
// wraps domain.ErrNotOwner because a bad token does not prove ownership of the
// placement identity.
var ErrOccupantTokenInvalid = fmt.Errorf("leadtoken: occupant token invalid: %w", domain.ErrNotOwner)

// OccupantClaims bind a bearer token to one lead placement generation.
type OccupantClaims struct {
	WorkspaceKey string   `json:"workspaceKey"`
	PlacementID  string   `json:"placementId"`
	Generation   int64    `json:"generation"`
	Caps         []string `json:"caps,omitempty"`
	jwt.RegisteredClaims
}

// OccupantActor returns the JWT subject for a placement-bound occupant token.
func OccupantActor(placementID string) string {
	return occupantSubjectPrefix + strings.TrimSpace(placementID)
}

// ResolveSigningKey returns the same HS256 key used for driver run tokens.
func ResolveSigningKey() ([]byte, error) {
	return driverpkg.ResolveRunTokenSigningKey()
}

// MintOccupantToken signs claims as an HS256 JWT. Subject is bound to the
// placement id, IssuedAt and ExpiresAt are always stamped, and ttl must be
// positive.
func MintOccupantToken(claims OccupantClaims, key []byte, ttl time.Duration) (string, error) {
	if err := validateOccupantIdentity(claims); err != nil {
		return "", fmt.Errorf("mint occupant token: %w", err)
	}
	if len(key) == 0 {
		return "", fmt.Errorf("mint occupant token: signing key required: %w", domain.ErrInvalid)
	}
	if ttl <= 0 {
		return "", fmt.Errorf("mint occupant token: ttl must be positive, got %s: %w", ttl, domain.ErrInvalid)
	}
	now := time.Now()
	claims.Subject = OccupantActor(claims.PlacementID)
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		return "", fmt.Errorf("mint occupant token: sign: %w", err)
	}
	return signed, nil
}

// ParseOccupantToken validates an HS256 occupant token and returns its claims.
// The algorithm is pinned to HS256, expiry is required, and Subject must match
// the placement identity. Callers must still read the placement record and
// enforce generation/caps before serving an op.
func ParseOccupantToken(token string, key []byte) (*OccupantClaims, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("parse occupant token: signing key required: %w", domain.ErrInvalid)
	}
	parsed, err := jwt.ParseWithClaims(token, &OccupantClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOccupantTokenInvalid, err)
	}
	claims, ok := parsed.Claims.(*OccupantClaims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("%w: claims missing", ErrOccupantTokenInvalid)
	}
	if err := validateOccupantIdentity(*claims); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOccupantTokenInvalid, err)
	}
	if claims.Subject != OccupantActor(claims.PlacementID) {
		return nil, fmt.Errorf("%w: subject %q does not match placement %q",
			ErrOccupantTokenInvalid, claims.Subject, claims.PlacementID)
	}
	return claims, nil
}

// IsOccupantTokenExpired reports whether a ParseOccupantToken failure means
// the token was correctly signed but past its expiry. jwt/v5 verifies the
// signature before validating expiry, so this proves the token was ours.
func IsOccupantTokenExpired(err error) bool {
	return errors.Is(err, jwt.ErrTokenExpired)
}

// HasCap reports whether claims include cap exactly after trimming whitespace.
func HasCap(claims *OccupantClaims, cap string) bool {
	want := strings.TrimSpace(cap)
	if claims == nil || want == "" {
		return false
	}
	for _, got := range claims.Caps {
		if strings.TrimSpace(got) == want {
			return true
		}
	}
	return false
}

func validateOccupantIdentity(claims OccupantClaims) error {
	if strings.TrimSpace(claims.WorkspaceKey) == "" {
		return fmt.Errorf("workspace key claim empty: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(claims.PlacementID) == "" {
		return fmt.Errorf("placement id claim empty: %w", domain.ErrInvalid)
	}
	if claims.Generation <= 0 {
		return fmt.Errorf("generation must be positive, got %d: %w", claims.Generation, domain.ErrInvalid)
	}
	return nil
}
