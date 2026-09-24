package browsers

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestParseOperatorGrantsScopesByWorkspaceAndTarget(t *testing.T) {
	resolve, count, err := ParseOperatorGrants(`[
		{"workspace":"ws","subjects":["alice"]},
		{"workspace":"ws2","subjects":["alice","bob"],"agents":["pairer"]}
	]`)
	if err != nil || resolve == nil || count != 3 {
		t.Fatalf("parse = %v, %d, %v", resolve != nil, count, err)
	}
	ctx := context.Background()
	id := func(s string) middleware.UserIdentity { return middleware.UserIdentity{UserID: s} }
	cases := []struct {
		ws, sub, target, op string
		ok                  bool
	}{
		{"ws", "alice", "lead", domain.BrowserOpList, true},
		{"ws", "alice", "anyone", domain.BrowserOpSelect, true},
		{"ws", "bob", "lead", domain.BrowserOpList, false},     // bob has no grant in ws
		{"ws2", "alice", "pairer", domain.BrowserOpGet, true},  // agent-scoped grant
		{"ws2", "alice", "other", domain.BrowserOpGet, false},  // agent not in grant
		{"ws3", "alice", "lead", domain.BrowserOpList, false},  // no grant for workspace
		{"WS", "alice", "lead", domain.BrowserOpList, false},   // workspace match is exact
		{"ws", "alice", "lead", domain.BrowserOpCreate, false}, // operators never create
		{"ws", "", "lead", domain.BrowserOpList, false},        // no subject
		{"", "alice", "lead", domain.BrowserOpList, false},     // no workspace
		{"ws", "alice", "lead", "browser:delete", false},       // unknown op
	}
	for _, c := range cases {
		err := resolve(ctx, c.ws, id(c.sub), c.target, c.op)
		if (err == nil) != c.ok {
			t.Errorf("%s@%s/%s %s: err=%v want ok=%v", c.sub, c.ws, c.target, c.op, err, c.ok)
		}
	}
}

func TestParseOperatorGrantsFailsClosed(t *testing.T) {
	for _, raw := range []string{"", "   ", "[]"} {
		if r, _, err := ParseOperatorGrants(raw); r != nil || err != nil {
			t.Errorf("%q: resolver=%v err=%v, want nil,nil", raw, r != nil, err)
		}
	}
	for _, raw := range []string{
		`alice,bob`,
		`{"workspace":"ws","subjects":["a"]}`,
		`[{"workspace":"","subjects":["a"]}]`,
		`[{"workspace":"*","subjects":["a"]}]`,
		`[{"workspace":"ws","subjects":[]}]`,
		`[{"workspace":"ws","subjects":["*"]}]`,
		`[{"workspace":"ws","subjects":["a"],"agents":["*"]}]`,
		`[{"workspace":"ws","subject":"a"}]`,
		`[{"workspace":"ws","subjects":["alice"]}] {"workspace":"other"}`,
		`[{"workspace":"ws","subjects":["alice"]}] []`,
		`[{"workspace":"ws","subjects":["alice"]}] garbage`,
		`[{"workspace":"ws","subjects":["alice"]}]]`,
	} {
		if r, _, err := ParseOperatorGrants(raw); r != nil || err == nil {
			t.Errorf("%q: resolver=%v err=%v, want error", raw, r != nil, err)
		}
	}
}
