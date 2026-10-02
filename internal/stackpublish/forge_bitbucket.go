package stackpublish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// BitbucketForge reads Bitbucket Cloud pull requests and keeps the
// loom/dependencies build status. Bitbucket has no merge queue. It only reads
// branch restrictions; it never changes them.
type BitbucketForge struct {
	token, baseURL string // baseURL is the API root, e.g. https://api.bitbucket.org/2.0
	client         *http.Client
}

func NewBitbucketForge(token string, client *http.Client, baseURL string) *BitbucketForge {
	if client == nil {
		client = http.DefaultClient
	}
	return &BitbucketForge{token: token, baseURL: strings.TrimSuffix(baseURL, "/"), client: client}
}

// call decodes a response with an expected status into out; any other status
// (including 202 "still indexing") is an error, so unknown answers fail closed.
func (b *BitbucketForge) call(ctx context.Context, method, target string, body, out any, want ...int) error {
	if !strings.HasPrefix(target, "http") {
		target = b.baseURL + target
	}
	code, data, err := providerCall(ctx, b.client, b.token, method, target, body)
	if err != nil {
		return err
	}
	for _, ok := range want {
		if code == ok {
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("bitbucket %s decode: %w", target, err)
			}
			return nil
		}
	}
	return providerErr("bitbucket", b.token, method, target, code, data)
}

type bitbucketPR struct {
	ID          int    `json:"id"`
	State       string `json:"state"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Source      struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
		Commit *struct {
			Hash string `json:"hash"`
		} `json:"commit"`
	} `json:"source"`
	Destination struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"destination"`
	MergeCommit *struct {
		Hash string `json:"hash"`
	} `json:"merge_commit"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// fullHash expands the abbreviated hashes Bitbucket puts in pull requests.
func (b *BitbucketForge) fullHash(ctx context.Context, owner, repo, hash string) (string, error) {
	if hash == "" || fullSHA.MatchString(hash) {
		return hash, nil
	}
	var commit struct {
		Hash string `json:"hash"`
	}
	if err := b.call(ctx, http.MethodGet, fmt.Sprintf("/repositories/%s/%s/commit/%s", owner, repo, hash), nil, &commit, http.StatusOK); err != nil {
		return "", err
	}
	if !fullSHA.MatchString(commit.Hash) || !strings.HasPrefix(commit.Hash, hash) {
		return "", fmt.Errorf("bitbucket commit %s resolved to %q", hash, commit.Hash)
	}
	return commit.Hash, nil
}

func (b *BitbucketForge) toPR(ctx context.Context, owner, repo string, pull bitbucketPR) (PR, error) {
	pr := PR{Number: pull.ID, Head: pull.Source.Branch.Name, Base: pull.Destination.Branch.Name,
		Title: pull.Title, Body: pull.Description, URL: pull.Links.HTML.Href}
	switch pull.State {
	case "OPEN":
		pr.State = "open"
	case "DECLINED", "SUPERSEDED":
		pr.State = "closed"
	case "MERGED":
		pr.State, pr.Merged = "closed", true
		if pull.MergeCommit == nil || pull.MergeCommit.Hash == "" {
			return PR{}, fmt.Errorf("bitbucket pull request #%d is merged without a merge commit", pull.ID)
		}
		sha, err := b.fullHash(ctx, owner, repo, pull.MergeCommit.Hash)
		if err != nil {
			return PR{}, err
		}
		pr.MergeCommitSHA = sha
	default:
		return PR{}, fmt.Errorf("bitbucket pull request #%d has unknown state %q", pull.ID, pull.State)
	}
	if pull.Source.Commit != nil {
		sha, err := b.fullHash(ctx, owner, repo, pull.Source.Commit.Hash)
		if err != nil {
			return PR{}, err
		}
		pr.HeadSHA = sha
	}
	return pr, nil
}

func (b *BitbucketForge) PullByNumber(ctx context.Context, owner, repo string, number int) (PR, error) {
	var pull bitbucketPR
	if err := b.call(ctx, http.MethodGet, fmt.Sprintf("/repositories/%s/%s/pullrequests/%d", owner, repo, number), nil, &pull, http.StatusOK); err != nil {
		return PR{}, err
	}
	return b.toPR(ctx, owner, repo, pull)
}

func (b *BitbucketForge) PullsForCommit(ctx context.Context, owner, repo, sha string) ([]PR, error) {
	var out []PR
	for next := fmt.Sprintf("/repositories/%s/%s/commit/%s/pullrequests", owner, repo, sha); next != ""; {
		var page struct {
			Values []bitbucketPR `json:"values"`
			Next   string        `json:"next"`
		}
		if err := b.call(ctx, http.MethodGet, next, nil, &page, http.StatusOK); err != nil {
			return nil, err
		}
		for _, pull := range page.Values {
			pr, err := b.toPR(ctx, owner, repo, pull)
			if err != nil {
				return nil, err
			}
			out = append(out, pr)
		}
		next = page.Next
	}
	return out, nil
}

// MergeQueueHead is always "": Bitbucket Cloud has no merge queue.
func (b *BitbucketForge) MergeQueueHead(context.Context, string, string, int) (string, error) {
	return "", nil
}

// PostDependencyStatus sets the loom/dependencies build status on sha:
// INPROGRESS while waiting, SUCCESSFUL once every predecessor landed. The
// status is identified by its exact key, so there is no app ID (0).
func (b *BitbucketForge) PostDependencyStatus(ctx context.Context, owner, repo, sha string, status DependencyStatus) (int64, error) {
	state, ok := map[string]string{"pending": "INPROGRESS", "success": "SUCCESSFUL"}[status.State]
	if !ok {
		return 0, fmt.Errorf("bitbucket: unknown loom/dependencies state %q", status.State)
	}
	return 0, b.call(ctx, http.MethodPost, fmt.Sprintf("/repositories/%s/%s/commit/%s/statuses/build", owner, repo, sha),
		map[string]string{"key": DependencyCheckName, "name": DependencyCheckName, "state": state, "description": status.Description,
			"url": fmt.Sprintf("https://bitbucket.org/%s/%s/commits/%s", owner, repo, sha)}, nil, http.StatusOK, http.StatusCreated)
}

// DependencyEnforcement is always not_enforced: Bitbucket Cloud's merge check
// (require_passing_builds_to_merge) counts passing builds of any key, so an
// unrelated green build satisfies it while loom/dependencies is INPROGRESS. No
// Bitbucket rule can require the loom/dependencies key itself. Bitbucket has no
// app pinning, so loomApp is unused.
func (b *BitbucketForge) DependencyEnforcement(context.Context, string, string, string, int64) (string, error) {
	return "not_enforced", nil
}
