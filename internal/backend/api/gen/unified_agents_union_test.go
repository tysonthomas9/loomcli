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
		{kind: "supervised", want: SupervisedAgent{}},
		{kind: "prompt", want: PromptAgentRecord{}},
		{kind: "scripted", want: ScriptedAgentRecord{}},
		{kind: "binding", want: LegacyBindingAgent{}},
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

func TestCreateUnifiedAgentUnionRepresentsLegacyOmittedKind(t *testing.T) {
	legacy := CreateSupervisedAgentRequest{
		Name:     "legacy-worker",
		RoleName: "task",
	}
	var request CreateUnifiedAgentRequest
	if err := request.FromCreateSupervisedAgentRequest(legacy); err != nil {
		t.Fatalf("build legacy supervised request: %v", err)
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
		t.Fatalf("legacy supervised request unexpectedly gained kind: %s", wire)
	}

	var decoded CreateUnifiedAgentRequest
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("unmarshal legacy supervised request: %v", err)
	}
	got, err := decoded.AsCreateSupervisedAgentRequest()
	if err != nil {
		t.Fatalf("read legacy supervised branch: %v", err)
	}
	if got.Kind != nil || got.Name != legacy.Name || got.RoleName != legacy.RoleName {
		t.Fatalf("legacy supervised round trip = %+v, want %+v with nil kind", got, legacy)
	}
}

func TestCreateSupervisedAgentKindEnumMatchesHandlerDispatch(t *testing.T) {
	for _, kind := range []CreateSupervisedAgentRequestKind{
		CreateSupervisedAgentRequestKindInteractive,
		CreateSupervisedAgentRequestKindSupervised,
		CreateSupervisedAgentRequestKindWorker,
	} {
		if !kind.Valid() {
			t.Errorf("supported create kind %q is not valid", kind)
		}
	}
	if CreateSupervisedAgentRequestKind("prompt").Valid() || CreateSupervisedAgentRequestKind("mystery").Valid() {
		t.Fatal("prompt or unknown kind unexpectedly validates as a supervised create kind")
	}
}
