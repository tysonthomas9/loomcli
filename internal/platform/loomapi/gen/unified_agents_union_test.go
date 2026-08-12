package gen

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestUnifiedAgentDiscriminatorDecodesEveryCollectionKind(t *testing.T) {
	tests := []struct {
		kind string
		want any
	}{
		{kind: "interactive", want: InteractiveAgentRecord{}},
		{kind: "prompt", want: PromptAgentRecord{}},
		{kind: "scripted", want: ScriptedAgentRecord{}},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			var agent UnifiedAgent
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"kind":%q}`, tt.kind)), &agent); err != nil {
				t.Fatalf("unmarshal unified agent: %v", err)
			}
			got, err := agent.ValueByDiscriminator()
			if err != nil {
				t.Fatalf("resolve discriminator: %v", err)
			}
			if fmt.Sprintf("%T", got) != fmt.Sprintf("%T", tt.want) {
				t.Fatalf("decoded type = %T, want %T", got, tt.want)
			}
		})
	}
}

func TestCreateUnifiedAgentUnionRepresentsInteractiveOmittedKind(t *testing.T) {
	interactive := CreateInteractiveAgentRequest{
		Name:     "reviewer",
		RoleName: "review",
	}
	var request CreateUnifiedAgentRequest
	if err := request.FromCreateInteractiveAgentRequest(interactive); err != nil {
		t.Fatalf("build interactive request: %v", err)
	}
	wire, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal legacy supervised request: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(wire, &object); err != nil {
		t.Fatalf("decode legacy supervised request JSON: %v", err)
	}
	if _, ok := object["kind"]; ok {
		t.Fatalf("default interactive request unexpectedly gained kind: %s", wire)
	}

	var decoded CreateUnifiedAgentRequest
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("unmarshal legacy supervised request: %v", err)
	}
	got, err := decoded.AsCreateInteractiveAgentRequest()
	if err != nil {
		t.Fatalf("read interactive branch: %v", err)
	}
	if got.Kind != nil || got.Name != interactive.Name || got.RoleName != interactive.RoleName {
		t.Fatalf("interactive round trip = %+v, want %+v with nil kind", got, interactive)
	}
}

func TestCreateInteractiveAgentKindEnumMatchesHandlerDispatch(t *testing.T) {
	if !CreateInteractiveAgentRequestKindInteractive.Valid() {
		t.Fatal("interactive create kind is not valid")
	}
	if CreateInteractiveAgentRequestKind("worker").Valid() || CreateInteractiveAgentRequestKind("supervised").Valid() {
		t.Fatal("retired background assignment kind unexpectedly validates as interactive")
	}
}
