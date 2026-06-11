package git

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

const prListJSONFields = "number,title,url,state,isDraft,headRefName,baseRefName,author,createdAt,updatedAt,reviewDecision,additions,deletions,changedFiles"

type ghPRAuthor struct {
	Login string `json:"login"`
}

type ghPRItem struct {
	Number         int        `json:"number"`
	Title          string     `json:"title"`
	URL            string     `json:"url"`
	State          string     `json:"state"`
	IsDraft        bool       `json:"isDraft"`
	HeadRefName    string     `json:"headRefName"`
	BaseRefName    string     `json:"baseRefName"`
	Author         ghPRAuthor `json:"author"`
	CreatedAt      string     `json:"createdAt"`
	UpdatedAt      string     `json:"updatedAt"`
	ReviewDecision string     `json:"reviewDecision"`
	Additions      int        `json:"additions"`
	Deletions      int        `json:"deletions"`
	ChangedFiles   int        `json:"changedFiles"`
}

// ListPullRequests returns open/closed/merged PRs for a repo via gh CLI.
func ListPullRequests(repoPath, state string, limit int) ([]ops.GitPullRequest, error) {
	if err := CheckGhInstalled(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}

	result := cli.GetDeps(nil).Exec.Run(
		repoPath,
		"gh", "pr", "list",
		"--state", mapPRListGhState(state),
		"--limit", strconv.Itoa(limit),
		"--json", prListJSONFields,
	)
	if result.Err != nil {
		errMsg := strings.TrimSpace(result.Stderr + result.Stdout)
		if errMsg == "" {
			return nil, fmt.Errorf("listing pull requests: %w", result.Err)
		}
		return nil, fmt.Errorf("listing pull requests: %s", errMsg)
	}

	raw := strings.TrimSpace(result.Stdout)
	if raw == "" || raw == "[]" {
		return []ops.GitPullRequest{}, nil
	}

	var items []ghPRItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("parsing pull requests: %w", err)
	}

	out := make([]ops.GitPullRequest, 0, len(items))
	for _, item := range items {
		out = append(out, ops.GitPullRequest{
			Number:         item.Number,
			Title:          item.Title,
			URL:            item.URL,
			State:          strings.ToUpper(item.State),
			IsDraft:        item.IsDraft,
			HeadRefName:    item.HeadRefName,
			BaseRefName:    item.BaseRefName,
			AuthorLogin:    item.Author.Login,
			CreatedAt:      item.CreatedAt,
			UpdatedAt:      item.UpdatedAt,
			ReviewDecision: item.ReviewDecision,
			Additions:      item.Additions,
			Deletions:      item.Deletions,
			ChangedFiles:   item.ChangedFiles,
		})
	}
	return out, nil
}

func mapPRListGhState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "merged":
		return "merged"
	case "closed":
		return "closed"
	case "open", "review":
		return "open"
	default:
		return "all"
	}
}

// FilterPullRequestsForReview keeps open PRs that still need human review.
func FilterPullRequestsForReview(prs []ops.GitPullRequest) []ops.GitPullRequest {
	out := make([]ops.GitPullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		if strings.EqualFold(pr.State, "MERGED") || strings.EqualFold(pr.State, "CLOSED") {
			continue
		}
		switch strings.ToUpper(pr.ReviewDecision) {
		case "APPROVED":
			continue
		default:
			out = append(out, pr)
		}
	}
	return out
}
