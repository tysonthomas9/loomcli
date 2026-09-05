package fleetdb

import (
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSkillListRejectsMissingOrInvalidSource(t *testing.T) {
	for _, body := range []string{"", `{}`, `{"skills":null}`, `{"skills":[null]}`, `{"skills":[{"workspace_key":"OTHER","name":"skill","scope":"workspace"}]}`, `{"skills":[{"workspace_key":"WS","name":"skill","scope":"bad"}]}`} {
		t.Run(body, func(t *testing.T) {
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
			defer closeFn()
			if result, err := client.Skills().List(t.Context(), "WS", store.SkillFilter{}); err == nil || result != nil {
				t.Fatalf("invalid source acknowledged: %+v err=%v", result, err)
			}
		})
	}
}
func TestSkillListAcceptsExplicitEmptySource(t *testing.T) {
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"skills":[]}`)) })
	defer closeFn()
	result, err := client.Skills().List(t.Context(), "WS", store.SkillFilter{})
	if err != nil || result == nil || len(result) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
