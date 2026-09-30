package journal

import (
	"context"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

type PullPlan struct {
	RequestID, Workspace, Lead, Repo, BaseSHA, Phase string
	Layers                                           []loomgit.AppliedLayer
}

func (s *SQLite) SavePullPlan(ctx context.Context, plan PullPlan) error {
	layers, err := json.Marshal(plan.Layers)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO pull_plans(request_id,workspace,lead,repo,base_sha,layers)
		VALUES (?,?,?,?,?,?)`, plan.RequestID, plan.Workspace, plan.Lead, plan.Repo, plan.BaseSHA, layers)
	return err
}

func (s *SQLite) PendingPullPlans(ctx context.Context, workspace, lead string) ([]PullPlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.request_id,p.workspace,p.lead,p.repo,p.base_sha,p.layers,
		COALESCE(a.phase,'') FROM pull_plans p LEFT JOIN applied_layers a ON a.request_id=p.request_id
		WHERE p.workspace=? AND p.lead=? ORDER BY p.rowid`, workspace, lead)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var plans []PullPlan
	for rows.Next() {
		var plan PullPlan
		var data []byte
		if err := rows.Scan(&plan.RequestID, &plan.Workspace, &plan.Lead, &plan.Repo, &plan.BaseSHA, &data, &plan.Phase); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &plan.Layers); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func (s *SQLite) DiscardPullPlan(ctx context.Context, requestID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pull_plans WHERE request_id=?`, requestID)
	return err
}
