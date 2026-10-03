package loomagent

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// MCS3: a workspace custom model id is listed under the harness as custom,
// with the harness's generic options, and a create with it is not
// unverified; removing it makes it an unlisted model again.
func TestCustomModels(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	if _, err := s.SetCustomModels(ctx, "opencode", []string{"openai/mine", "bad id"}); !isCode(err, CodePresetInvalid) {
		t.Fatalf("malformed custom id = %v, want preset_invalid", err)
	}
	if _, err := s.SetCustomModels(ctx, "nope", nil); !isCode(err, CodePresetInvalid) {
		t.Fatalf("unknown harness = %v, want preset_invalid", err)
	}
	ids, err := s.SetCustomModels(ctx, "opencode", []string{"openai/mine", "fake-model", "openai/mine"})
	if err != nil || !reflect.DeepEqual(ids, []string{"openai/mine", "fake-model"}) {
		t.Fatalf("set = %v, %v", ids, err)
	}
	if got, err := s.CustomModels(ctx, "opencode"); err != nil || !reflect.DeepEqual(got, ids) {
		t.Fatalf("custom models = %v, %v", got, err)
	}
	ms, err := s.Models(ctx, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	custom := slices.DeleteFunc(slices.Clone(ms), func(m loomharness.Model) bool { return !m.Custom })
	def := ms[slices.IndexFunc(ms, func(m loomharness.Model) bool { return m.Default })]
	if len(custom) != 1 || custom[0].ID != "openai/mine" || custom[0].Provider != "custom" ||
		!reflect.DeepEqual(custom[0].Options, def.Options) {
		t.Fatalf("custom catalog entries = %+v; want openai/mine alone (fake-model is listed), with %+v", custom, def.Options)
	}

	req := leadReq("r1")
	req.Overrides.Model = "openai/mine"
	a, err := s.Create(ctx, req)
	if err != nil || a.ModelUnverified || e.events(t, a.AgentID, KindModelUnverified) != 0 {
		t.Fatalf("create with a custom model = unverified %v, %v", a.ModelUnverified, err)
	}

	if _, err := s.SetCustomModels(ctx, "opencode", nil); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.Models(ctx, "opencode"); slices.ContainsFunc(ms, func(m loomharness.Model) bool { return m.Custom }) {
		t.Fatalf("removed custom model still listed: %+v", ms)
	}
	req = leadReq("r2")
	req.Name, req.Overrides.Model = "b", "openai/mine"
	b, err := s.Create(ctx, req)
	wantUnverified(t, e, b, err, "openai/mine")
}
