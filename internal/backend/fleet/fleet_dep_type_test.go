package fleet

import (
	"strings"
	"testing"
)

// loom's in-process vocabularies (internal/types, internal/entity) declare
// "waits-for" and "conditional-blocks", which no storage backend implements —
// fleet-db rejects them. Catching it client-side turns an opaque server 400
// into a message that names the supported set.
func TestValidateFleetDepType(t *testing.T) {
	for _, ok := range []string{"", "blocks", "parent-child", "related", "duplicate-of", "superseded-by"} {
		if err := validateFleetDepType(ok); err != nil {
			t.Errorf("validateFleetDepType(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"waits-for", "conditional-blocks", "discovered-from", "nonsense"} {
		err := validateFleetDepType(bad)
		if err == nil {
			t.Errorf("validateFleetDepType(%q) = nil, want a validation error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "not storable in fleet-db") ||
			!strings.Contains(err.Error(), "blocks, parent-child") {
			t.Errorf("error for %q should name the supported set, got %v", bad, err)
		}
	}
}
