package skills

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type invalidCatalogStore struct {
	store.Store
	skills []*domain.Skill
}

func (s invalidCatalogStore) Skills() store.SkillStore { return invalidCatalogSkills{skills: s.skills} }

type invalidCatalogSkills struct {
	store.SkillStore
	skills []*domain.Skill
}

func (s invalidCatalogSkills) List(context.Context, string, store.SkillFilter) ([]*domain.Skill, error) {
	return s.skills, nil
}
func TestCatalogRejectsMalformedEntriesInsteadOfDroppingThem(t *testing.T) {
	for _, skill := range []*domain.Skill{nil, {WorkspaceKey: "WS", Name: "bad", Scope: "invalid"}, {WorkspaceKey: "OTHER", Name: "valid", Scope: domain.SkillScopeWorkspace}} {
		h := Handler{Store: invalidCatalogStore{skills: []*domain.Skill{skill}}}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetPathValue("ws", "WS")
		response := httptest.NewRecorder()
		h.getCatalog(response, req)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("malformed catalog acknowledged: %d %s", response.Code, response.Body.String())
		}
	}
}
