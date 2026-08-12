// Ported from github.com/entireio/cli cmd/entire/cli/textutil/ide_tags.go
// (MIT, (c) 2026 Entire Inc.). Inlined here so the transcript package has
// no external dependency for its single call site.

package artifacttranscript

import (
	"regexp"
	"strings"
)

var ideContextTagRegex = regexp.MustCompile(`(?s)<ide_[^>]*>.*?</ide_[^>]*>`)

var systemTagRegexes = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<local-command-caveat[^>]*>.*?</local-command-caveat>`),
	regexp.MustCompile(`(?s)<system-reminder[^>]*>.*?</system-reminder>`),
	regexp.MustCompile(`(?s)<command-name[^>]*>.*?</command-name>`),
	regexp.MustCompile(`(?s)<command-message[^>]*>.*?</command-message>`),
	regexp.MustCompile(`(?s)<command-args[^>]*>.*?</command-args>`),
	regexp.MustCompile(`(?s)<local-command-stdout[^>]*>.*?</local-command-stdout>`),
	regexp.MustCompile(`</?user_query>`),
}

// StripIDEContextTags removes IDE-injected and system-injected context tags
// from prompt text so they don't leak into rendered transcripts.
func StripIDEContextTags(text string) string {
	result := ideContextTagRegex.ReplaceAllString(text, "")
	for _, re := range systemTagRegexes {
		result = re.ReplaceAllString(result, "")
	}
	return strings.TrimSpace(result)
}
