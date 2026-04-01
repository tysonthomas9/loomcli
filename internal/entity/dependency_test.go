package entity

import (
	"strings"
	"testing"
)

func TestDependencyType_IsValid(t *testing.T) {
	tests := []struct {
		name    string
		depType DependencyType
		want    bool
	}{
		{"non-empty short string", "blocks", true},
		{"single char", "a", true},
		{"exactly 50 chars", DependencyType(strings.Repeat("x", 50)), true},
		{"51 chars is invalid", DependencyType(strings.Repeat("x", 51)), false},
		{"empty is invalid", "", false},
		{"typical dependency type", "depends_on", true},
		{"another type", "blocks", true},
		{"type with spaces", "some type", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.depType.IsValid(); got != tt.want {
				t.Errorf("DependencyType(%q).IsValid() = %v, want %v", tt.depType, got, tt.want)
			}
		})
	}
}
