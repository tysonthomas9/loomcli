package memstore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSessionEvalStoreConflictFilterOrderAndLimit(t *testing.T) {
	ctx := context.Background()
	st := New()
	base := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	evals := []*domain.SessionEval{
		{WorkspaceKey: "WS", EvalID: "eval-1", SessionID: "sess-1", TaskID: "T-1", AgentID: "agent-1", JudgePromptVersion: "v1", CreatedAt: base.Add(-2 * time.Hour)},
		{WorkspaceKey: "WS", EvalID: "eval-2", SessionID: "sess-2", TaskID: "T-1", AgentID: "agent-1", JudgePromptVersion: "v1", CreatedAt: base.Add(-1 * time.Hour)},
		{WorkspaceKey: "WS", EvalID: "eval-3", SessionID: "sess-3", TaskID: "T-2", AgentID: "agent-2", JudgePromptVersion: "v2", CreatedAt: base},
	}
	for _, in := range evals {
		if _, err := st.SessionEvals().Create(ctx, in); err != nil {
			t.Fatalf("Create(%s): %v", in.EvalID, err)
		}
	}
	if _, err := st.SessionEvals().Create(ctx, evals[0]); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate Create err = %v, want ErrConflict", err)
	}
	since := base.Add(-90 * time.Minute)
	until := base.Add(30 * time.Minute)
	got, err := st.SessionEvals().List(ctx, "WS", store.SessionEvalFilter{
		TaskID:             "T-1",
		AgentID:            "agent-1",
		JudgePromptVersion: "v1",
		Since:              &since,
		Until:              &until,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotIDs := evalIDs(got); !reflect.DeepEqual(gotIDs, []string{"eval-2"}) {
		t.Fatalf("listed eval IDs = %v, want [eval-2]", gotIDs)
	}
	if err := st.SessionEvals().Delete(ctx, "WS", "eval-2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.SessionEvals().Get(ctx, "WS", "eval-2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get deleted err = %v, want ErrNotFound", err)
	}
}

func evalIDs(evals []*domain.SessionEval) []string {
	out := make([]string, 0, len(evals))
	for _, eval := range evals {
		out = append(out, eval.EvalID)
	}
	return out
}
