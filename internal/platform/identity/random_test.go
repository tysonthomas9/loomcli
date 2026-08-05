package identity

import (
	"bytes"
	"errors"
	"testing"
)

func TestNewUUIDUsesVersionFourAndRFCVariant(t *testing.T) {
	value, err := newUUID(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if value != "00000000-0000-4000-8000-000000000000" {
		t.Fatalf("UUID = %q", value)
	}
}

func TestNewUUIDFailsClosedWhenRandomnessFails(t *testing.T) {
	if _, err := newUUID(errorReader{}); err == nil {
		t.Fatal("newUUID accepted a failed random source")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("random unavailable")
}
