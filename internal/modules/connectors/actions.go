package connectors

// Connector action vocabulary is owned here rather than by provider
// adapters. Callers request these stable capabilities; adapters only
// translate them to provider-specific wire operations.
const (
	ActionGitHubPullRequestRead  = "github.pull_request.read"
	ActionGitHubReviewPost       = "github.review.post"
	ActionGitHubMerge            = "github.merge"
	ActionGitHubPullsList        = "github.pulls.list"
	ActionGitHubCompareRead      = "github.compare.read"
	ActionGitHubIssueCommentPost = "github.issue_comment.post"

	ActionSlackChatPost          = "slack.chat.post"
	ActionSlackConversationsRead = "slack.conversations.read"

	ActionDatadogMonitorsRead   = "datadog.monitors.read"
	ActionDatadogAlertRead      = "datadog.alert.read"
	ActionDatadogIncidentsWrite = "datadog.incidents.write"
)

var actionsBySource = map[ConnectorSourceKind][]string{
	ConnectorSourceGitHub: {
		ActionGitHubPullRequestRead,
		ActionGitHubReviewPost,
		ActionGitHubMerge,
		ActionGitHubPullsList,
		ActionGitHubCompareRead,
		ActionGitHubIssueCommentPost,
	},
	ConnectorSourceSlack: {
		ActionSlackChatPost,
		ActionSlackConversationsRead,
	},
	ConnectorSourceDatadog: {
		ActionDatadogMonitorsRead,
		ActionDatadogAlertRead,
		ActionDatadogIncidentsWrite,
	},
}

// ActionsForSource returns the immutable owner vocabulary implemented by one
// provider family. The returned slice is always a copy.
func ActionsForSource(source ConnectorSourceKind) []string {
	return append([]string(nil), actionsBySource[source]...)
}
