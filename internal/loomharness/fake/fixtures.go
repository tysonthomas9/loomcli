package fake

import "github.com/tysonthomas9/loomcli/internal/loomharness"

// Kind says what a passing fixture proves.
type Kind string

const (
	// Orchestration fixtures test Loom's own state machine. The fake's
	// behavior is the whole contract, so a pass on the fake is enough.
	Orchestration Kind = "orchestration"
	// ProviderContract fixtures copy a behavior a real harness must show.
	// A pass on the fake proves only the orchestration side; each adapter's
	// contract tests must re-run the fixture against the real harness.
	ProviderContract Kind = "provider_contract"
)

// Fixture is one named scripted turn.
type Fixture struct {
	Name string
	Kind Kind
	Turn Turn
}

// Fixtures is the shared set the agent state tests and adapter contract tests use.
var Fixtures = []Fixture{
	{Name: "streamed_reply", Kind: Orchestration, Turn: Turn{Steps: []Step{{Delta: "Hel"}, {Delta: "lo"}}}},
	{Name: "ask_then_reply", Kind: Orchestration, Turn: Turn{Steps: []Step{{Ask: "ask_1"}, {Delta: "done"}}}},
	{Name: "feed_gap", Kind: Orchestration, Turn: Turn{Steps: []Step{{Delta: "a"}, {Delta: "b", Gap: true}, {Delta: "c"}}}},
	{Name: "crash_mid_turn_cancelled", Kind: ProviderContract, Turn: Turn{Steps: []Step{{Delta: "a"}, {Crash: true}}}},
	{Name: "crash_mid_turn_resumed", Kind: ProviderContract, Turn: Turn{Steps: []Step{{Delta: "a"}, {Crash: true}, {Delta: "b"}}, ResumeContinues: true}},
	{Name: "input_lost_before_delivery", Kind: ProviderContract, Turn: Turn{Delivery: loomharness.LandedNotFound}},
	{Name: "input_delivery_unknown", Kind: ProviderContract, Turn: Turn{Delivery: loomharness.LandedUnknown}},
}
