package connectors

import (
	"strings"
	"testing"
)

func TestActionsForSourceOwnsCanonicalVocabulary(t *testing.T) {
	seen := map[string]ConnectorSourceKind{}
	for _, source := range []ConnectorSourceKind{
		ConnectorSourceGitHub,
		ConnectorSourceSlack,
		ConnectorSourceDatadog,
	} {
		actions := ActionsForSource(source)
		if len(actions) == 0 {
			t.Fatalf("source %q has no actions", source)
		}
		for _, action := range actions {
			if normalized, err := normalizeConnectorAction(action); err != nil || normalized != action {
				t.Fatalf("source %q action %q invalid: %v", source, action, err)
			}
			if !strings.HasPrefix(action, string(source)+".") {
				t.Fatalf("source %q owns cross-source action %q", source, action)
			}
			if prior, duplicate := seen[action]; duplicate {
				t.Fatalf("action %q owned by both %q and %q", action, prior, source)
			}
			seen[action] = source
		}
		actions[0] = "mutated"
		if got := ActionsForSource(source)[0]; got == "mutated" {
			t.Fatalf("source %q action registry mutated through returned slice", source)
		}
	}
	if got := ActionsForSource(ConnectorSourceInternal); got != nil {
		t.Fatalf("internal source actions = %v, want nil", got)
	}
}
