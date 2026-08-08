package workspace

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateNameOwnsWorkspaceNamingPolicy(t *testing.T) {
	tests := []struct {
		name string
		kind NameValidationKind
	}{
		{name: "valid-name_42"},
		{name: "", kind: NameRequired},
		{name: strings.Repeat("a", MaxNameLength+1), kind: NameTooLong},
		{name: "invalid/name", kind: NameInvalidCharacters},
		{name: "unicode-π", kind: NameInvalidCharacters},
	}
	for _, test := range tests {
		err := ValidateName(test.name)
		if test.kind == "" {
			if err != nil {
				t.Fatalf("ValidateName(%q) error = %v", test.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("ValidateName(%q) error = %v, want ErrInvalid", test.name, err)
		}
		kind, ok := NameValidationKindOf(err)
		if !ok || kind != test.kind {
			t.Fatalf("ValidateName(%q) kind = %q/%t, want %q", test.name, kind, ok, test.kind)
		}
	}
}
