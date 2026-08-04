package fleet

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// WorkerClaims defines the JWT claims structure for fleet workers.
type WorkerClaims struct {
	WorkerID string   `json:"worker_id"`
	Repos    []string `json:"repos,omitempty"`
	jwt.RegisteredClaims
}

// TokenConfig holds JWT configuration for fleet worker tokens.
type TokenConfig struct {
	SigningKey []byte
	Expiry     time.Duration
}

// GenerateWorkerToken creates a signed JWT for a fleet worker.
func GenerateWorkerToken(workerID string, repos []string, signingKey []byte, expiry time.Duration) (string, error) {
	now := time.Now()
	claims := WorkerClaims{
		WorkerID: workerID,
		Repos:    repos,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(signingKey)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// ValidateWorkerToken parses and validates a worker JWT, returning the claims.
func ValidateWorkerToken(tokenString string, signingKey []byte) (*WorkerClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &WorkerClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return signingKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	claims, ok := token.Claims.(*WorkerClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}
