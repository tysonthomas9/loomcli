package metricscmd

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const prReviewerRoleName = "pr-reviewer"
const prReviewerRoleLabel = "Review"

var (
	prReviewerPRNumberSuffix = regexp.MustCompile(`-pr-(\d+)$`)
	prReviewerHashedBody     = regexp.MustCompile(`^review-(.+)-([0-9a-f]{8})$`)
)

// prReviewerDisplayFields returns optional UI labels for a pr-reviewer agent.
// display_name is "repo#number" when both can be derived; role_label is always
// "Review" for the pr-reviewer role.
func prReviewerDisplayFields(agent *domain.Agent) (displayName, roleLabel string) {
	if agent == nil || strings.TrimSpace(agent.RoleName) != prReviewerRoleName {
		return "", ""
	}
	roleLabel = prReviewerRoleLabel
	repo, number, ok := derivePRReviewerRepoNumber(agent.Name, agent.Repos)
	if !ok {
		return "", roleLabel
	}
	return repo + "#" + strconv.Itoa(number), roleLabel
}

func derivePRReviewerRepoNumber(agentName string, repos []string) (repo string, number int, ok bool) {
	name := strings.TrimSpace(strings.ToLower(agentName))
	match := prReviewerPRNumberSuffix.FindStringSubmatch(name)
	if len(match) != 2 {
		return "", 0, false
	}
	number, err := strconv.Atoi(match[1])
	if err != nil || number <= 0 {
		return "", 0, false
	}
	if repo = firstNonEmptyRepo(repos); repo != "" {
		return repo, number, true
	}
	body := strings.TrimSuffix(name, match[0])
	hashed := prReviewerHashedBody.FindStringSubmatch(body)
	if len(hashed) == 3 {
		if repo = lastSegment(hashed[1]); repo != "" {
			return repo, number, true
		}
	}
	// Legacy: review-{repo}-pr-N (no identity hash).
	if strings.HasPrefix(body, "review-") {
		if repo = strings.TrimPrefix(body, "review-"); repo != "" {
			return repo, number, true
		}
	}
	return "", 0, false
}

func firstNonEmptyRepo(repos []string) string {
	for _, r := range repos {
		if r = strings.TrimSpace(r); r != "" {
			return r
		}
	}
	return ""
}

func lastSegment(value string) string {
	value = strings.Trim(value, "-")
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "-")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}
