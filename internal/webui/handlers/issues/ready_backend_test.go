package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

type stubReadyQueries struct {
	query  workitems.AvailabilityQuery
	values []workitems.IssueSummary
	err    error
}

func (s *stubReadyQueries) Ready(_ context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	s.query = query
	return s.values, s.err
}

func TestHandleReadyWorkItems(t *testing.T) {
	queries := &stubReadyQueries{values: []workitems.IssueSummary{
		{ID: "RDY-1", Title: "Ready One", Status: "open", Priority: 1, IssueType: "task", Parent: "EPIC-1", SourceRepo: "repoA", Repo: "repoA", Design: "approved plan body", HasDesign: true},
		{ID: "RDY-2", Title: "Ready Two", Status: "open", Priority: 2, IssueType: "bug"},
	}}
	handler := HandleReadyWorkItems(queries)

	req := httptest.NewRequest(http.MethodGet, "/api/ready?assignee=agent-1&labels_any=backend,urgent&sort=priority", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if queries.query.Assignee != "agent-1" || queries.query.SortPolicy != "priority" || len(queries.query.LabelsAny) != 2 {
		t.Fatalf("query = %+v", queries.query)
	}
	var response ReadyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !response.Success || len(response.Data) != 2 {
		t.Fatalf("response = %+v", response)
	}
	found := response.Data[0]
	if found.Parent == nil || *found.Parent != "EPIC-1" || found.Repo != "repoA" {
		t.Fatalf("ready projection = %+v", found)
	}
	if found.Design != "approved plan body" || !found.HasDesign {
		t.Fatalf("design projection = %+v", found)
	}
}

func TestHandleReadyWorkItemsUnavailable(t *testing.T) {
	recorder := httptest.NewRecorder()
	HandleReadyWorkItems(nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
}
