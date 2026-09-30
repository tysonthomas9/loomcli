package journal

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

// Inventory is a read-only view of every durable Loom Git object.
type Inventory struct {
	Repos        []loomgit.WorkspaceRepo
	Areas        []WorkingArea
	Revisions    []loomgit.Revision
	Publications []Publication
	Mirrors      []MirrorRecord
}

//nolint:funlen // The four ordered table scans form one consistent inventory read.
func (s *SQLite) Inventory(ctx context.Context) (Inventory, error) {
	var out Inventory
	repos, err := s.db.QueryContext(ctx, `SELECT workspace,repo,trunk,workspace_branch,base_sha FROM workspace_repos ORDER BY workspace,repo`)
	if err != nil {
		return out, err
	}
	for repos.Next() {
		var row loomgit.WorkspaceRepo
		if err := repos.Scan(&row.Workspace, &row.Repo, &row.Trunk, &row.WorkspaceBranch, &row.BaseSHA); err != nil {
			_ = repos.Close()
			return out, err
		}
		out.Repos = append(out.Repos, row)
	}
	err = repos.Err()
	_ = repos.Close()
	if err != nil {
		return out, err
	}
	areas, err := s.db.QueryContext(ctx, `SELECT workspace,lead,repo,path,branch,base_sha,mode FROM working_areas ORDER BY workspace,lead,repo`)
	if err != nil {
		return out, err
	}
	for areas.Next() {
		var row WorkingArea
		if err := areas.Scan(&row.Workspace, &row.Lead, &row.Repo, &row.Path, &row.Branch, &row.BaseSHA, &row.Mode); err != nil {
			_ = areas.Close()
			return out, err
		}
		out.Areas = append(out.Areas, row)
	}
	err = areas.Err()
	_ = areas.Close()
	if err != nil {
		return out, err
	}
	revisions, err := s.db.QueryContext(ctx, `SELECT `+revisionColumns+` FROM change_revisions ORDER BY workspace,change_id,number`)
	if err != nil {
		return out, err
	}
	for revisions.Next() {
		row, err := scanRevision(revisions)
		if err != nil {
			_ = revisions.Close()
			return out, err
		}
		out.Revisions = append(out.Revisions, row)
	}
	err = revisions.Err()
	_ = revisions.Close()
	if err != nil {
		return out, err
	}
	publications, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,phase,pr_number,pr_url FROM change_publications ORDER BY workspace,change_id`)
	if err != nil {
		return out, err
	}
	for publications.Next() {
		var row Publication
		if err := publications.Scan(&row.Workspace, &row.Change, &row.Repo, &row.Branch, &row.Trunk, &row.Slug, &row.Head, &row.Phase, &row.PRNumber, &row.PRURL); err != nil {
			_ = publications.Close()
			return out, err
		}
		out.Publications = append(out.Publications, row)
	}
	err = publications.Err()
	_ = publications.Close()
	if err != nil {
		return out, err
	}
	out.Mirrors, err = s.ExistingMirrorRecords(ctx)
	return out, err
}
