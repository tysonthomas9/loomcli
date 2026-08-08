package stackpublish

import (
	"os"
	"regexp"
	"strings"
)

var xAccessTokenRe = regexp.MustCompile(`x-access-token:[^@\s]+@`)

// scrubSecrets removes credentials from a string before it is surfaced in an
// error, log, or report. It masks the provided tokens, any known secret-bearing
// env values, and the `x-access-token:...@` URL form. Ported from the proven
// local-task-runner.ts scrubToken/redactTranscriptSecrets pattern.
func scrubSecrets(text string, tokens ...string) string {
	out := text
	all := append([]string{}, tokens...)
	for _, name := range []string{
		"GITHUB_TOKEN", "GH_TOKEN", "LOOM_PR_GIT_PASSWORD",
		"LOOM_FLEET_DB_API_KEY", "LOOM_TASK_RUN_LEASE_TOKEN",
	} {
		if v := os.Getenv(name); len(v) >= 8 {
			all = append(all, v)
		}
	}
	for _, t := range all {
		if t != "" {
			out = strings.ReplaceAll(out, t, "***")
		}
	}
	return xAccessTokenRe.ReplaceAllString(out, "x-access-token:***@")
}
