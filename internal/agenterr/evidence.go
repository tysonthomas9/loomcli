package agenterr

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// EvidenceSource names WHICH classification step produced the verdict.
//
// A verdict on its own is not diagnosable: AuthFailure is terminal
// (agentpolicy.Decide → StopFatal), so a single spurious occurrence parks an
// agent until a human re-authenticates, and nothing in the record said which
// step decided or on what text. These constants close that gap.
type EvidenceSource string

const (
	EvidenceHarnessMarker EvidenceSource = "harness_marker"     // classifyHarnessMarkers
	EvidenceWrapper       EvidenceSource = "wrapper_classifier" // fromClassification
	EvidenceResidual      EvidenceSource = "residual_pattern"   // classifyWithPatterns
	EvidenceExitCode      EvidenceSource = "exit_code"          // classifyByExitCode fallback
	EvidenceSupervisor    EvidenceSource = "supervisor"         // synthesized, never text-derived
)

// ScreenEvidence DESCRIBES the rendered screen behind an auth verdict. Every
// field is a description, never a verdict: nothing here changes a class, a
// disposition or a restart decision. See PUPPET-431 — a whole-screen match can
// fire on task prose quoting a banner, which is exactly why BannerMatch records
// the matched LINE and ComposerWitnessed / DialogWitnessed are recorded beside
// it.
type ScreenEvidence struct {
	Scanned           bool   `json:"scanned"`                      // false = we had no screen at all
	BannerRule        string `json:"banner_rule,omitempty"`        // mirrored anchor id that matched
	BannerMatch       string `json:"banner_match,omitempty"`       // the matched LINE, capped + redacted
	ComposerWitnessed *bool  `json:"composer_witnessed,omitempty"` // tri-state: nil = not evaluated
	DialogWitnessed   *bool  `json:"dialog_witnessed,omitempty"`
}

// Evidence records what produced a classification verdict, so a SINGLE
// occurrence can be judged genuine or spurious without needing a cluster.
type Evidence struct {
	Source       EvidenceSource  `json:"source,omitempty"`
	Rule         string          `json:"rule,omitempty"`    // marker const name, pattern id, wrapper status, exit arm
	Detail       string          `json:"detail,omitempty"`  // upstream text verbatim, capped
	Match        string          `json:"match,omitempty"`   // matched substring, capped
	Excerpt      string          `json:"excerpt,omitempty"` // redacted window around the match
	HTTPCode     int             `json:"http_code,omitempty"`
	ExitCode     int             `json:"exit_code,omitempty"`
	ScannedBytes int             `json:"scanned_bytes,omitempty"`
	Screen       *ScreenEvidence `json:"screen,omitempty"` // auth verdicts only
}

const (
	evidenceMatchCap   = 256
	evidenceExcerptCap = 2048
	evidenceDetailCap  = 512
	evidenceWindow     = 400  // bytes either side of the match
	evidenceSummaryCap = 3072 // the whole single-line form stays log/state-file safe
)

// Empty reports whether this is the zero value, i.e. no classification step
// recorded anything.
func (e Evidence) Empty() bool {
	return e.Source == "" && e.Rule == "" && e.Detail == "" && e.Match == "" &&
		e.Excerpt == "" && e.HTTPCode == 0 && e.ExitCode == 0 &&
		e.ScannedBytes == 0 && e.Screen == nil
}

// Summary renders a single-line, log/state-file-safe form (<= ~3 KiB):
//
//	harness_marker rule=AuthRequiredMarker exit=1 screen=banner:claude.loggedout.run_login,composer=true,dialog=false detail="…" match="…"
//
// Excerpt is deliberately NOT rendered: it is the bulk field and belongs in the
// structured record, not in a log line.
func (e Evidence) Summary() string {
	if e.Empty() {
		return ""
	}
	var parts []string
	if e.Source != "" {
		parts = append(parts, string(e.Source))
	}
	if e.Rule != "" {
		parts = append(parts, "rule="+oneLine(e.Rule))
	}
	if e.HTTPCode != 0 {
		parts = append(parts, fmt.Sprintf("http=%d", e.HTTPCode))
	}
	if e.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit=%d", e.ExitCode))
	}
	if s := e.Screen.summary(); s != "" {
		parts = append(parts, "screen="+s)
	}
	if e.Detail != "" {
		parts = append(parts, fmt.Sprintf("detail=%q", oneLine(e.Detail)))
	}
	if e.Match != "" {
		parts = append(parts, fmt.Sprintf("match=%q", oneLine(e.Match)))
	}
	return capString(strings.Join(parts, " "), evidenceSummaryCap)
}

// summary renders the screen half of Evidence.Summary. A nil receiver renders
// nothing; a scanned-but-blank screen still renders "none" so "we looked and
// saw no banner" is distinguishable from "we never looked".
func (s *ScreenEvidence) summary() string {
	if s == nil {
		return ""
	}
	if !s.Scanned {
		return "unscanned"
	}
	fields := []string{"banner:" + orNone(s.BannerRule)}
	if s.ComposerWitnessed != nil {
		fields = append(fields, fmt.Sprintf("composer=%t", *s.ComposerWitnessed))
	}
	if s.DialogWitnessed != nil {
		fields = append(fields, fmt.Sprintf("dialog=%t", *s.DialogWitnessed))
	}
	return strings.Join(fields, ",")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// newTextEvidence builds Evidence for a regex hit: locate the match, cap it,
// cut a window either side, sanitize and redact. When the regex does not match
// (a caller that already knows it did should not hit this), only the source and
// rule are recorded.
func newTextEvidence(src EvidenceSource, rule string, re *regexp.Regexp, text string) Evidence {
	ev := Evidence{Source: src, Rule: rule}
	if re == nil || text == "" {
		return ev
	}
	loc := re.FindStringIndex(text)
	if loc == nil {
		return ev
	}
	ev.Match = capString(redactEvidence(sanitizeText(text[loc[0]:loc[1]])), evidenceMatchCap)

	start := loc[0] - evidenceWindow
	if start < 0 {
		start = 0
	}
	end := loc[1] + evidenceWindow
	if end > len(text) {
		end = len(text)
	}
	// Widen to rune boundaries so the window never splits a glyph before the
	// sanitizer sees it.
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	ev.Excerpt = capString(redactEvidence(sanitizeText(text[start:end])), evidenceExcerptCap)
	return ev
}

// ─────────────────────────────────────────────────────────────────────────────
// Mirrored screen anchors.
//
// These are COPIES of unexported regexes in harness-wrapper@v0.7.7
// `pkg/chat/ready.go` (claudeOnboardingRE :225, codexOnboardingRE :229,
// claudeLoggedOutRE :235, codexLoggedOutRE :240) and of the folder-trust /
// bypass anchors in `pkg/turns/harness/claudecode/claudecode.go` :88-93. They
// are mirrored rather than imported because the upstream identifiers are not
// exported.
//
// THEY DESCRIBE AND NEVER GATE. Nothing here changes a class, a disposition or
// a restart decision; the ids only annotate a verdict that was already reached.
//
// Each `id` is a STABLE CONTRACT: docs/adr/0002-authfailure-stays-terminal.md
// names them in its revisit triggers, so renaming one silently breaks the
// trigger it belongs to. A wrapper bump that changes an upstream regex lands
// here: update the pattern, keep the id, and bump the version named above.
// evidence_test.go's mirror-drift pin is the guard.
// ─────────────────────────────────────────────────────────────────────────────

type screenAnchor struct {
	id string
	re *regexp.Regexp
}

// onboardingAnchors are evaluated BEFORE loggedOutAnchors, mirroring
// chat.authRequired's `onboardingWall || loggedOut` precedence, so the recorded
// id names the arm the wrapper itself would have taken.
var onboardingAnchors = []screenAnchor{
	{"claude.onboarding.theme_picker", regexp.MustCompile(`(?i)choose the text style`)},
	{"claude.onboarding.select_login_method", regexp.MustCompile(`(?i)select login method`)},
	{"codex.onboarding.sign_in_with_chatgpt", regexp.MustCompile(`(?i)sign in with chatgpt`)},
	{"codex.onboarding.browser_signin", regexp.MustCompile(`(?i)finish signing in via your browser`)},
}

var loggedOutAnchors = []screenAnchor{
	{"claude.loggedout.run_login", regexp.MustCompile(`(?i)\brun /login\b`)},
	{"claude.loggedout.not_logged_in", regexp.MustCompile(`(?i)\bnot logged in\b`)},
	{"claude.loggedout.invalid_api_key", regexp.MustCompile(`(?i)\binvalid api key\b`)},
	{"codex.loggedout.401_unauthorized", regexp.MustCompile(`(?i)\b401 unauthorized\b`)},
	{"codex.loggedout.missing_bearer", regexp.MustCompile(`(?i)missing bearer or basic authentication`)},
	{"codex.loggedout.not_logged_in", regexp.MustCompile(`(?i)\bnot logged in\b`)},
	{"codex.loggedout.codex_login", regexp.MustCompile(`(?i)\bcodex(?: mcp)? login\b`)},
}

// dialogAnchors mirror the blocking arms of claudecode.DetectInput — the
// folder-trust dialog (two phrasings) and the bypass-acceptance screen. Both
// paint the "Claude Code" header and a "❯" selector, so a verdict taken over
// one of these screens is about a DIALOG, not a login.
var dialogAnchors = []string{
	"Do you trust the files in this folder?",
	"Is this a project you created or one you trust?",
	"Bypass Permissions mode",
}

// describeScreen fills ScreenEvidence for auth-class verdicts only.
//
// It returns &ScreenEvidence{Scanned:false} when text is empty — "we had no
// screen" is a finding (it is the ADR's fourth revisit trigger), not an
// omission, so this never returns nil for that case.
func describeScreen(text string) *ScreenEvidence {
	if strings.TrimSpace(text) == "" {
		return &ScreenEvidence{Scanned: false}
	}
	clean := sanitizeText(text)
	ev := &ScreenEvidence{Scanned: true}

	for _, group := range [][]screenAnchor{onboardingAnchors, loggedOutAnchors} {
		for _, a := range group {
			loc := a.re.FindStringIndex(clean)
			if loc == nil {
				continue
			}
			ev.BannerRule = a.id
			ev.BannerMatch = capString(redactEvidence(lineAround(clean, loc[0])), evidenceMatchCap)
			break
		}
		if ev.BannerRule != "" {
			break
		}
	}

	// Composer anchors mirror chat.readyForInput (ready.go:148-190): claude's
	// header plus its "❯" selector, codex's "›" prompt. A composer on screen
	// beside a logged-out banner is the readiness-miss shape this whole record
	// exists to make visible.
	composer := (strings.Contains(clean, "Claude Code") && strings.Contains(clean, "❯")) ||
		strings.Contains(clean, "›")
	dialog := false
	for _, a := range dialogAnchors {
		if strings.Contains(clean, a) {
			dialog = true
			break
		}
	}
	ev.ComposerWitnessed = &composer
	ev.DialogWitnessed = &dialog
	return ev
}

// lineAround returns the whole line containing byte offset idx.
func lineAround(s string, idx int) string {
	start := strings.LastIndexByte(s[:idx], '\n') + 1
	end := strings.IndexByte(s[idx:], '\n')
	if end < 0 {
		end = len(s)
	} else {
		end += idx
	}
	return strings.TrimSpace(s[start:end])
}

// evidenceSecretEnv names the environment variables whose VALUES must never
// reach a stored excerpt. The residual auth pattern matches on
// `ANTHROPIC_API_KEY` and `invalid.*key`, so the window around an auth match is
// exactly where a key would be.
var evidenceSecretEnv = []string{
	"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY",
	"GEMINI_API_KEY", "GOOGLE_API_KEY", "CURSOR_API_KEY",
	"GITHUB_TOKEN", "GH_TOKEN",
	"LOOM_FLEET_DB_API_KEY", "LOOM_TASK_RUN_LEASE_TOKEN",
}

var (
	evidenceSkKeyRe       = regexp.MustCompile(`sk-[A-Za-z0-9_-]{12,}`)
	evidenceAccessTokenRe = regexp.MustCompile(`x-access-token:[^@\s]+@`)
)

// redactEvidence masks credentials BEFORE anything is stored, not at display
// time — a stored excerpt is copied into state files, events and checkpoints,
// and a display-time filter would be one missed call site away from leaking.
//
// The shape is ported from internal/stackpublish/scrub.go, but deliberately
// kept agenterr-local so the two packages stay independent (agenterr imports
// nothing outside stdlib and the wrapper).
func redactEvidence(text string) string {
	if text == "" {
		return text
	}
	out := text
	for _, name := range evidenceSecretEnv {
		// Short values are skipped: an 8-char floor keeps a placeholder like
		// "unset" or "n/a" from masking ordinary words out of the record.
		if v := os.Getenv(name); len(v) >= 8 {
			out = strings.ReplaceAll(out, v, "***")
		}
	}
	out = evidenceSkKeyRe.ReplaceAllString(out, "sk-***")
	return evidenceAccessTokenRe.ReplaceAllString(out, "x-access-token:***@")
}

// sanitizeText strips ANSI escape sequences and C0 control bytes other than
// newline and tab. The one-shot classification path is fed raw PTY bytes (child
// 2 of PUPPET-573); emulator-rendered text is unaffected because it contains
// none of these. UTF-8 continuation bytes are >= 0x80 and pass through
// untouched, so multi-byte glyphs survive.
func sanitizeText(s string) string {
	if s == "" || !strings.ContainsFunc(s, isControlRune) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		if c == 0x7f || (c < 0x20 && c != '\n' && c != '\t') {
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

func isControlRune(r rune) bool {
	return r == 0x7f || (r < 0x20 && r != '\n' && r != '\t')
}

// skipEscape returns the index just past the escape sequence starting at i.
func skipEscape(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return j
	}
	switch s[j] {
	case '[': // CSI: params, intermediates, one final byte
		j++
		for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
			j++
		}
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			j++
		}
		if j < len(s) {
			j++
		}
	case ']': // OSC: terminated by BEL or ST (ESC \)
		j++
		for j < len(s) {
			if s[j] == 0x07 {
				j++
				break
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				j += 2
				break
			}
			j++
		}
	default: // two-byte escape
		j++
	}
	return j
}

// capString truncates on a RUNE boundary and appends "…", never raw byte
// slicing: screens are full of box-drawing and ✻/⏺/❯, and a split rune
// corrupts the JSON state file this evidence is written to. The returned string
// is never longer than max bytes.
func capString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const ellipsis = "…"
	cut := max - len(ellipsis)
	if cut <= 0 {
		return ""
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

// oneLine flattens text for the single-line Summary form.
func oneLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s))
}
