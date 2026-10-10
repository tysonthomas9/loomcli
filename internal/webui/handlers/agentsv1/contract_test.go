package agentsv1

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
)

// TestAgentWireMatchesOpenAPI: every wire type has the same JSON fields as
// its api/openapi.yaml schema, so the generated Go and TypeScript contracts
// cannot drift from what the routes read and write.
func TestAgentWireMatchesOpenAPI(t *testing.T) {
	fields := func(v any) []string {
		var out []string
		rt := reflect.TypeOf(v)
		for i := range rt.NumField() {
			out = append(out, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
		}
		slices.Sort(out)
		return out
	}
	for _, p := range [][2]any{
		{Agent{}, gen.AgentV1{}}, {WaitingMessage{}, gen.AgentV1WaitingMessage{}}, {Ask{}, gen.AgentV1Ask{}}, {Question{}, gen.AgentV1AskQuestion{}},
		{AgentList{}, gen.AgentV1List{}}, {Overrides{}, gen.AgentV1Overrides{}}, {Persona{}, gen.AgentV1Persona{}},
		{Subject{}, gen.AgentV1Subject{}}, {CreateBody{}, gen.AgentV1CreateBody{}}, {Expect{}, gen.AgentV1Expect{}},
		{UpdateBody{}, gen.AgentV1UpdateBody{}}, {ArchiveBody{}, gen.AgentV1ArchiveBody{}},
		{SendBody{}, gen.AgentV1SendBody{}}, {SendResult{}, gen.AgentV1SendResult{}},
		{WithdrawResult{}, gen.AgentV1WithdrawResult{}}, {RespondBody{}, gen.AgentV1RespondBody{}},
		{Event{}, gen.AgentV1Event{}}, {EventPage{}, gen.AgentV1EventPage{}},
		{PermissionRule{}, gen.AgentV1PermissionRule{}}, {Preset{}, gen.AgentV1Preset{}},
		{PresetList{}, gen.AgentV1PresetList{}}, {Error{}, gen.AgentV1Error{}},
		{ModelCatalog{}, gen.AgentV1ModelCatalog{}}, {ModelProvider{}, gen.AgentV1ModelProvider{}},
		{Model{}, gen.AgentV1Model{}}, {OptionDescriptor{}, gen.AgentV1OptionDescriptor{}},
		{OptionChoice{}, gen.AgentV1OptionChoice{}}, {OptionValue{}, gen.AgentV1OptionValue{}},
	} {
		if w, g := fields(p[0]), fields(p[1]); !slices.Equal(w, g) {
			t.Errorf("%T fields %v; openapi %T has %v", p[0], w, p[1], g)
		}
	}
}
