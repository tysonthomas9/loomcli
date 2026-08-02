package domain

import (
	"strings"
	"testing"
)

// The single rule this whole type exists to hold: a role that says nothing —
// in ANY of the shapes "nothing" can take — must never end up more permissive
// than a role that says something. Every one of these resolves to deny, and if
// any of them ever resolves to allow the safety knobs are bypassable by
// omission rather than by decision.
func TestDispositionFor_DenyByDefaultAcrossEveryUnsetShape(t *testing.T) {
	kinds := []string{"trust_prompt", "confirm", "", "some_kind_no_harness_has_yet"}

	tests := []struct {
		name   string
		policy *RoleInputPolicy
	}{
		{name: "nil policy", policy: nil},
		{name: "zero value", policy: &RoleInputPolicy{}},
		{name: "empty default", policy: &RoleInputPolicy{Default: ""}},
		{name: "empty kinds map", policy: &RoleInputPolicy{Kinds: map[string]string{}}},
		{
			name:   "entry with empty disposition",
			policy: &RoleInputPolicy{Kinds: map[string]string{"trust_prompt": "", "confirm": ""}},
		},
		{
			// The named kind is allowed; everything the policy did NOT name
			// still denies, because Default is unset.
			name:   "unnamed kind alongside an allowed one",
			policy: &RoleInputPolicy{Kinds: map[string]string{"unrelated_kind": RoleInputAllow}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, kind := range kinds {
				if kind == "unrelated_kind" {
					continue
				}
				if got := tt.policy.DispositionFor(kind); got != RoleInputDeny {
					t.Errorf("DispositionFor(%q) = %q, want %q — an unset policy must never be permissive", kind, got, RoleInputDeny)
				}
			}
		})
	}
}

func TestDispositionFor_ExplicitDispositionsWin(t *testing.T) {
	policy := &RoleInputPolicy{
		Default: RoleInputDeny,
		Kinds: map[string]string{
			"trust_prompt": RoleInputAllow,
			"confirm":      RoleInputAsk,
			"text_input":   RoleInputDeny,
		},
	}

	for kind, want := range map[string]string{
		"trust_prompt": RoleInputAllow,
		"confirm":      RoleInputAsk,
		"text_input":   RoleInputDeny,
		"unnamed":      RoleInputDeny,
	} {
		if got := policy.DispositionFor(kind); got != want {
			t.Errorf("DispositionFor(%q) = %q, want %q", kind, got, want)
		}
	}
}

// An explicit permissive Default is honored — the deny-by-default rule is about
// the UNSET case, not about refusing to let a role opt in.
func TestDispositionFor_ExplicitAllowDefaultApplies(t *testing.T) {
	policy := &RoleInputPolicy{Default: RoleInputAllow, Kinds: map[string]string{"trust_prompt": RoleInputDeny}}
	if got := policy.DispositionFor("anything"); got != RoleInputAllow {
		t.Errorf("DispositionFor(anything) = %q, want %q", got, RoleInputAllow)
	}
	if got := policy.DispositionFor("trust_prompt"); got != RoleInputDeny {
		t.Errorf("a named kind must override Default, got %q", got)
	}
}

// An entry that names a kind with an empty disposition must NOT fall through to
// Default. A half-written policy fails closed even when the default is open.
func TestDispositionFor_EmptyEntryDoesNotInheritPermissiveDefault(t *testing.T) {
	policy := &RoleInputPolicy{Default: RoleInputAllow, Kinds: map[string]string{"trust_prompt": ""}}
	if got := policy.DispositionFor("trust_prompt"); got != RoleInputDeny {
		t.Fatalf("DispositionFor(trust_prompt) = %q, want %q — an empty entry must deny, not inherit allow", got, RoleInputDeny)
	}
}

func TestRoleInputPolicyClone_IsADeepCopy(t *testing.T) {
	if (*RoleInputPolicy)(nil).Clone() != nil {
		t.Fatal("Clone of nil must stay nil so it keeps meaning deny-everything")
	}
	orig := &RoleInputPolicy{Default: RoleInputDeny, Kinds: map[string]string{"trust_prompt": RoleInputDeny}}
	clone := orig.Clone()
	clone.Kinds["trust_prompt"] = RoleInputAllow
	if orig.Kinds["trust_prompt"] != RoleInputDeny {
		t.Fatal("mutating a clone changed the original — a shared Kinds map lets a caller flip a stored role's disposition")
	}
}

func TestValidateRoleInputPolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  *RoleInputPolicy
		wantErr string
	}{
		{name: "nil is valid and means deny", policy: nil},
		{name: "empty is valid", policy: &RoleInputPolicy{}},
		{name: "full vocabulary", policy: &RoleInputPolicy{
			Default: RoleInputAsk,
			Kinds:   map[string]string{"a": RoleInputAllow, "b": RoleInputDeny, "c": RoleInputAsk, "d": ""},
		}},
		{
			name:    "unknown default",
			policy:  &RoleInputPolicy{Default: "yes"},
			wantErr: `role input_policy default "yes" must be one of deny, allow, ask`,
		},
		{
			name:    "unknown disposition",
			policy:  &RoleInputPolicy{Kinds: map[string]string{"trust_prompt": "sure"}},
			wantErr: `role input_policy kind "trust_prompt" disposition "sure" must be one of deny, allow, ask`,
		},
		{
			name:    "empty kind key",
			policy:  &RoleInputPolicy{Kinds: map[string]string{"": RoleInputDeny}},
			wantErr: "role input_policy kind is required",
		},
		{
			name:    "over-long kind key",
			policy:  &RoleInputPolicy{Kinds: map[string]string{strings.Repeat("k", MaxRoleInputPolicyKindLength+1): RoleInputDeny}},
			wantErr: "exceeds maximum length",
		},
		{name: "too many kinds", policy: tooManyKinds(), wantErr: "exceeds maximum of 64 kinds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRoleInputPolicy(tt.policy)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateRoleInputPolicy() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateRoleInputPolicy() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func tooManyKinds() *RoleInputPolicy {
	kinds := make(map[string]string, MaxRoleInputPolicyKinds+1)
	for i := 0; i <= MaxRoleInputPolicyKinds; i++ {
		kinds[string(rune('a'+i%26))+strings.Repeat("x", i)] = RoleInputDeny
	}
	return &RoleInputPolicy{Kinds: kinds}
}

func TestEncodeDecodeRoleInputPolicy_RoundTrip(t *testing.T) {
	orig := &RoleInputPolicy{
		Default: RoleInputDeny,
		Kinds:   map[string]string{"trust_prompt": RoleInputAllow, "confirm": RoleInputAsk},
	}
	raw, err := EncodeRoleInputPolicy(orig)
	if err != nil {
		t.Fatalf("EncodeRoleInputPolicy: %v", err)
	}
	got, err := DecodeRoleInputPolicy(raw)
	if err != nil {
		t.Fatalf("DecodeRoleInputPolicy(%q): %v", raw, err)
	}
	if got.Default != orig.Default || len(got.Kinds) != len(orig.Kinds) {
		t.Fatalf("round trip = %+v, want %+v", got, orig)
	}
	for kind, want := range orig.Kinds {
		if got.DispositionFor(kind) != want {
			t.Errorf("after round trip DispositionFor(%q) = %q, want %q", kind, got.DispositionFor(kind), want)
		}
	}
}

func TestEncodeRoleInputPolicy_NilEncodesToEmpty(t *testing.T) {
	raw, err := EncodeRoleInputPolicy(nil)
	if err != nil || raw != "" {
		t.Fatalf("EncodeRoleInputPolicy(nil) = (%q, %v), want (\"\", nil) so absent and deny-everything stay the same state", raw, err)
	}
}

// Every way the transported value can be wrong must land on deny, never on a
// partially-decoded policy and never on a hard error the caller has to handle
// to stay safe.
func TestDecodeRoleInputPolicy_FailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "absent", raw: ""},
		{name: "whitespace", raw: "   "},
		{name: "truncated json", raw: `{"default":"deny","kinds":{`, wantErr: true},
		{name: "not an object", raw: `"deny"`, wantErr: true},
		{name: "wrong value type", raw: `{"kinds":{"trust_prompt":true}}`, wantErr: true},
		{name: "disposition outside the vocabulary", raw: `{"kinds":{"trust_prompt":"yes"}}`, wantErr: true},
		{name: "default outside the vocabulary", raw: `{"default":"allow-everything"}`, wantErr: true},
		{name: "empty kind key", raw: `{"kinds":{"":"allow"}}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := DecodeRoleInputPolicy(tt.raw)
			if tt.wantErr && err == nil {
				t.Fatalf("DecodeRoleInputPolicy(%q) error = nil, want an error", tt.raw)
			}
			if policy != nil {
				t.Fatalf("DecodeRoleInputPolicy(%q) = %+v, want nil so the caller denies", tt.raw, policy)
			}
			// The load-bearing assertion: whatever the caller does with the
			// error, the policy it got denies everything.
			if got := policy.DispositionFor("trust_prompt"); got != RoleInputDeny {
				t.Fatalf("a failed decode resolved to %q, want %q", got, RoleInputDeny)
			}
		})
	}
}
