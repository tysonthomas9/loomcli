package loomgit

import (
	"errors"
	"fmt"
	"testing"
)

func TestEveryErrorCodeRoundTrips(t *testing.T) {
	if len(ErrorCodes) != 29 {
		t.Fatalf("got %d codes", len(ErrorCodes))
	}
	seen := map[Code]bool{}
	for _, code := range ErrorCodes {
		if seen[code] {
			t.Fatalf("duplicate code %s", code)
		}
		seen[code] = true
		cause := errors.New("cause")
		err := fmt.Errorf("outer: %w", NewError(code, "detail", cause))
		if !errors.Is(err, NewError(code, "", nil)) {
			t.Fatalf("Is failed for %s", code)
		}
		var typed *Error
		if !errors.As(err, &typed) || typed.Code() != string(code) || !errors.Is(err, cause) {
			t.Fatalf("As/Code/Unwrap failed for %s: %v", code, err)
		}
	}
}
