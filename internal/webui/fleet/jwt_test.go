package fleet

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testSigningKey = []byte("test-secret-key-for-jwt-signing!")

func TestJWT_GenerateToken_ProducesValidJWT(t *testing.T) {
	token, err := GenerateWorkerToken("worker-1", []string{"repo-a", "repo-b"}, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("GenerateWorkerToken failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	// Verify the token can be parsed as a valid JWT
	parsed, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		return testSigningKey, nil
	})
	if err != nil {
		t.Fatalf("failed to parse generated token: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("generated token is not valid")
	}
}

func TestJWT_ValidateToken_SucceedsForValidToken(t *testing.T) {
	token, err := GenerateWorkerToken("worker-1", []string{"repo-a"}, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("GenerateWorkerToken failed: %v", err)
	}

	claims, err := ValidateWorkerToken(token, testSigningKey)
	if err != nil {
		t.Fatalf("ValidateWorkerToken failed: %v", err)
	}
	if claims == nil {
		t.Fatal("expected non-nil claims")
	}
	if claims.WorkerID != "worker-1" {
		t.Errorf("WorkerID = %q, want %q", claims.WorkerID, "worker-1")
	}
}

func TestJWT_ExpiredToken_IsRejected(t *testing.T) {
	// Generate a token that expired 1 second ago
	token, err := GenerateWorkerToken("worker-1", nil, testSigningKey, -1*time.Second)
	if err != nil {
		t.Fatalf("GenerateWorkerToken failed: %v", err)
	}

	_, err = ValidateWorkerToken(token, testSigningKey)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestJWT_InvalidSignature_IsRejected(t *testing.T) {
	token, err := GenerateWorkerToken("worker-1", nil, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("GenerateWorkerToken failed: %v", err)
	}

	wrongKey := []byte("wrong-key-not-the-right-signing!!")
	_, err = ValidateWorkerToken(token, wrongKey)
	if err == nil {
		t.Fatal("expected error for invalid signature, got nil")
	}
}

func TestJWT_ClaimsExtraction_WorkerIDAndRepos(t *testing.T) {
	repos := []string{"repo-a", "repo-b", "repo-c"}
	token, err := GenerateWorkerToken("worker-42", repos, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("GenerateWorkerToken failed: %v", err)
	}

	claims, err := ValidateWorkerToken(token, testSigningKey)
	if err != nil {
		t.Fatalf("ValidateWorkerToken failed: %v", err)
	}

	if claims.WorkerID != "worker-42" {
		t.Errorf("WorkerID = %q, want %q", claims.WorkerID, "worker-42")
	}
	if len(claims.Repos) != 3 {
		t.Fatalf("len(Repos) = %d, want 3", len(claims.Repos))
	}
	for i, want := range repos {
		if claims.Repos[i] != want {
			t.Errorf("Repos[%d] = %q, want %q", i, claims.Repos[i], want)
		}
	}

	// Verify timing claims are set
	if claims.IssuedAt == nil {
		t.Error("IssuedAt should be set")
	}
	if claims.ExpiresAt == nil {
		t.Error("ExpiresAt should be set")
	}
	if claims.ExpiresAt != nil && claims.IssuedAt != nil {
		diff := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
		if diff < 59*time.Minute || diff > 61*time.Minute {
			t.Errorf("token expiry duration = %v, want ~1h", diff)
		}
	}
}

func TestJWT_EmptyRepos_Works(t *testing.T) {
	token, err := GenerateWorkerToken("worker-1", nil, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("GenerateWorkerToken failed: %v", err)
	}

	claims, err := ValidateWorkerToken(token, testSigningKey)
	if err != nil {
		t.Fatalf("ValidateWorkerToken failed: %v", err)
	}

	if claims.WorkerID != "worker-1" {
		t.Errorf("WorkerID = %q, want %q", claims.WorkerID, "worker-1")
	}
	if claims.Repos != nil {
		t.Errorf("Repos = %v, want nil", claims.Repos)
	}
}

func TestJWT_EmptySliceRepos_Works(t *testing.T) {
	token, err := GenerateWorkerToken("worker-1", []string{}, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("GenerateWorkerToken failed: %v", err)
	}

	claims, err := ValidateWorkerToken(token, testSigningKey)
	if err != nil {
		t.Fatalf("ValidateWorkerToken failed: %v", err)
	}

	if claims.WorkerID != "worker-1" {
		t.Errorf("WorkerID = %q, want %q", claims.WorkerID, "worker-1")
	}
	// Empty slice may deserialize as nil due to omitempty; either is acceptable
	if len(claims.Repos) != 0 {
		t.Errorf("len(Repos) = %d, want 0", len(claims.Repos))
	}
}

func TestJWT_MalformedToken_IsRejected(t *testing.T) {
	_, err := ValidateWorkerToken("not-a-valid-jwt-token", testSigningKey)
	if err == nil {
		t.Fatal("expected error for malformed token, got nil")
	}
}

func TestJWT_EmptyToken_IsRejected(t *testing.T) {
	_, err := ValidateWorkerToken("", testSigningKey)
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
}

func TestJWT_DifferentWorkerIDs_ProduceDifferentTokens(t *testing.T) {
	token1, err := GenerateWorkerToken("worker-1", nil, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("GenerateWorkerToken for worker-1 failed: %v", err)
	}

	token2, err := GenerateWorkerToken("worker-2", nil, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("GenerateWorkerToken for worker-2 failed: %v", err)
	}

	if token1 == token2 {
		t.Error("tokens for different workers should differ")
	}
}

func TestJWT_UsesHS256SigningMethod(t *testing.T) {
	token, err := GenerateWorkerToken("worker-1", nil, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("GenerateWorkerToken failed: %v", err)
	}

	parsed, _, err := jwt.NewParser().ParseUnverified(token, &WorkerClaims{})
	if err != nil {
		t.Fatalf("ParseUnverified failed: %v", err)
	}
	if parsed.Method.Alg() != "HS256" {
		t.Errorf("signing method = %q, want %q", parsed.Method.Alg(), "HS256")
	}
}
