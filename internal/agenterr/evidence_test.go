package agenterr

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func boolp(b bool) *bool { return &b }

func TestEvidenceSummaryShape(t *testing.T) {
	tests := []struct {
		name string
		ev   Evidence
		want string
	}{
		{
			name: "harness marker with a full screen description",
			ev: Evidence{
				Source:   EvidenceHarnessMarker,
				Rule:     "AuthRequiredMarker",
				Detail:   "authentication required",
				Match:    AuthRequiredMarker,
				ExitCode: 1,
				Screen: &ScreenEvidence{
					Scanned:           true,
					BannerRule:        "claude.loggedout.run_login",
					ComposerWitnessed: boolp(true),
					DialogWitnessed:   boolp(false),
				},
			},
			want: `harness_marker rule=AuthRequiredMarker exit=1 screen=banner:claude.loggedout.run_login,composer=true,dialog=false detail="authentication required" match="` + AuthRequiredMarker + `"`,
		},
		{
			name: "wrapper classifier carries the HTTP code",
			ev:   Evidence{Source: EvidenceWrapper, Rule: "retry_later/RateLimited", Detail: "rate limited", HTTPCode: 429},
			want: `wrapper_classifier rule=retry_later/RateLimited http=429 detail="rate limited"`,
		},
		{
			name: "residual pattern, no screen",
			ev:   Evidence{Source: EvidenceResidual, Rule: "residual.auth", Match: "401 Unauthorized", ExitCode: 1},
			want: `residual_pattern rule=residual.auth exit=1 match="401 Unauthorized"`,
		},
		{
			name: "exit code fallback",
			ev:   Evidence{Source: EvidenceExitCode, Rule: "exit.137_sigkill", ExitCode: 137},
			want: `exit_code rule=exit.137_sigkill exit=137`,
		},
		{
			name: "supervisor-synthesized evidence is never text-derived",
			ev:   Evidence{Source: EvidenceSupervisor, Rule: "spawn"},
			want: `supervisor rule=spawn`,
		},
		{
			name: "an unscanned screen still says so",
			ev:   Evidence{Source: EvidenceHarnessMarker, Rule: "AuthRequiredMarker", Screen: &ScreenEvidence{Scanned: false}},
			want: `harness_marker rule=AuthRequiredMarker screen=unscanned`,
		},
		{
			name: "multi-line detail is flattened to one line",
			ev:   Evidence{Source: EvidenceWrapper, Rule: "api_error/AuthFailure", Detail: "line one\nline two"},
			want: `wrapper_classifier rule=api_error/AuthFailure detail="line one line two"`,
		},
		{name: "zero value renders nothing", ev: Evidence{}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ev.Summary(); got != tc.want {
				t.Errorf("Summary()\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestEvidenceEmpty(t *testing.T) {
	if !(Evidence{}).Empty() {
		t.Error("zero Evidence must be Empty")
	}
	for name, ev := range map[string]Evidence{
		"source":  {Source: EvidenceResidual},
		"rule":    {Rule: "residual.auth"},
		"detail":  {Detail: "x"},
		"match":   {Match: "x"},
		"excerpt": {Excerpt: "x"},
		"http":    {HTTPCode: 401},
		"exit":    {ExitCode: 1},
		"scanned": {ScannedBytes: 10},
		"screen":  {Screen: &ScreenEvidence{}},
	} {
		if ev.Empty() {
			t.Errorf("Evidence with %s set must not be Empty", name)
		}
	}
	if (&AgentError{Class: OutcomeFromHarness(0), Backend: "claude", Message: "boom"}).Error() != "agenterr: [None] claude: boom" {
		t.Error("empty evidence must not decorate Error()")
	}
}

func TestErrorAppendsEvidenceSummary(t *testing.T) {
	err := &AgentError{
		Class:    OutcomeFromDomain(SpawnFailureOutcome),
		Backend:  "claude",
		Message:  "boom",
		Evidence: Evidence{Source: EvidenceExitCode, Rule: "exit.default", ExitCode: 1},
	}
	want := "agenterr: [SpawnFailure] claude: boom [evidence: exit_code rule=exit.default exit=1]"
	if got := err.Error(); got != want {
		t.Errorf("Error()\n got: %s\nwant: %s", got, want)
	}
}

// A stored excerpt travels into state files, events and checkpoints, so a
// credential must be masked at CONSTRUCTION, not at display time.
func TestRedactionBeforeStorage(t *testing.T) {
	secret := "sk-ant-api03-AAAABBBBCCCCDDDDEEEE"
	t.Setenv("ANTHROPIC_API_KEY", secret)
	envOnly := "0123456789abcdefghij" // no sk- shape; only the env lookup can catch it
	t.Setenv("GITHUB_TOKEN", envOnly)

	text := strings.Join([]string{
		"running the agent",
		"ANTHROPIC_API_KEY=" + secret,
		"git remote set-url origin https://x-access-token:" + envOnly + "@github.com/o/r.git",
		"401 Unauthorized: invalid api key",
	}, "\n")

	ev := newTextEvidence(EvidenceResidual, "residual.auth", residualPatterns[1].re, text)
	ev.Detail = redactEvidence(text)

	for field, v := range map[string]string{"Match": ev.Match, "Excerpt": ev.Excerpt, "Detail": ev.Detail} {
		if strings.Contains(v, secret) {
			t.Errorf("%s leaked the API key: %q", field, v)
		}
		if strings.Contains(v, envOnly) {
			t.Errorf("%s leaked the token: %q", field, v)
		}
	}
	if !strings.Contains(ev.Excerpt, "x-access-token:***@") {
		t.Errorf("x-access-token URL not masked in excerpt: %q", ev.Excerpt)
	}

	// The literal sk- form is masked even when no env var is set.
	if got := redactEvidence("key=sk-proj-ZZZZYYYYXXXXWWWW done"); strings.Contains(got, "ZZZZYYYY") {
		t.Errorf("literal sk- key not masked: %q", got)
	}

	// And on the screen description path.
	screen := describeScreen("Invalid API key · Please run /login\nANTHROPIC_API_KEY=" + secret)
	if strings.Contains(screen.BannerMatch, secret) {
		t.Errorf("BannerMatch leaked the API key: %q", screen.BannerMatch)
	}
}

func TestEvidenceCapsOnRuneBoundaries(t *testing.T) {
	// 1 MiB of a multi-byte glyph, with the match buried in the middle.
	filler := strings.Repeat("❯", 350_000) // 3 bytes each -> ~1 MiB
	text := filler + "401 Unauthorized" + filler
	ev := newTextEvidence(EvidenceResidual, "residual.auth", residualPatterns[1].re, text)

	if len(ev.Excerpt) > evidenceExcerptCap {
		t.Errorf("Excerpt = %d bytes, want <= %d", len(ev.Excerpt), evidenceExcerptCap)
	}
	if len(ev.Match) > evidenceMatchCap {
		t.Errorf("Match = %d bytes, want <= %d", len(ev.Match), evidenceMatchCap)
	}
	for _, s := range []string{ev.Excerpt, ev.Match} {
		if !utf8.ValidString(s) {
			t.Errorf("capped value is not valid UTF-8: %q", s)
		}
	}
	// A split rune corrupts the JSON state file this evidence is written to.
	if _, err := json.Marshal(ev); err != nil {
		t.Fatalf("evidence must marshal: %v", err)
	}

	// A glyph landing exactly on the cap is dropped whole, not halved.
	for pad := 0; pad < 4; pad++ {
		s := strings.Repeat("a", evidenceMatchCap-3-pad) + strings.Repeat("✻", 8)
		got := capString(s, evidenceMatchCap)
		if len(got) > evidenceMatchCap {
			t.Errorf("pad=%d: capString len %d > %d", pad, len(got), evidenceMatchCap)
		}
		if !utf8.ValidString(got) {
			t.Errorf("pad=%d: capString split a rune: %q", pad, got)
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("pad=%d: truncation must append an ellipsis: %q", pad, got)
		}
	}
	if got := capString("short", 256); got != "short" {
		t.Errorf("under-cap string must pass through, got %q", got)
	}
}

func TestSanitizeStripsANSIAndControls(t *testing.T) {
	raw := "\x1b[2J\x1b[1;31mInvalid API key\x1b[0m\x07\nkeep\ttabs\n\x1b]0;title\x07done"
	got := sanitizeText(raw)
	want := "Invalid API key\nkeep\ttabs\ndone"
	if got != want {
		t.Errorf("sanitizeText()\n got: %q\nwant: %q", got, want)
	}
	if got := sanitizeText("plain ❯ text"); got != "plain ❯ text" {
		t.Errorf("clean text must pass through unchanged, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// describeScreen — one case per reading the design exists to support.
// ─────────────────────────────────────────────────────────────────────────────

func TestDescribeScreen(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		wantScanned  bool
		wantRule     string
		wantComposer *bool
		wantDialog   *bool
		wantMatch    string
	}{
		{
			// A genuine expired login: the banner is there and NO composer is.
			name:         "logged-out banner, no composer",
			text:         "  Claude Code needs you to authenticate.\n  Please run /login to continue.\n",
			wantScanned:  true,
			wantRule:     "claude.loggedout.run_login",
			wantComposer: boolp(false),
			wantDialog:   boolp(false),
			wantMatch:    "Please run /login to continue.",
		},
		{
			// THE CASE THIS DESIGN EXISTS FOR. A logged-out banner sitting on a
			// screen that also shows a live composer ("Claude Code" + "❯") is a
			// READINESS MISS, not an expired login: the harness was usable and
			// we stopped the agent fatally anyway. composer_witnessed=true is
			// what makes that judgeable from a single occurrence.
			name:         "readiness miss: logged-out banner WITH a live composer",
			text:         "Claude Code v2.1.251\n  Not logged in · run /login\n❯ ",
			wantScanned:  true,
			wantRule:     "claude.loggedout.run_login",
			wantComposer: boolp(true),
			wantDialog:   boolp(false),
			wantMatch:    "Not logged in · run /login",
		},
		{
			// Precedence: onboarding rules are evaluated before logged-out
			// rules, mirroring chat.authRequired's `onboardingWall || loggedOut`
			// order, so the recorded id names the arm the wrapper would take.
			name:         "onboarding wizard outranks a logged-out banner",
			text:         "Claude Code\n  Select login method\n  1. Claude account\n  Not logged in\n❯ ",
			wantScanned:  true,
			wantRule:     "claude.onboarding.select_login_method",
			wantComposer: boolp(true),
			wantDialog:   boolp(false),
			wantMatch:    "Select login method",
		},
		{
			name:         "codex onboarding",
			text:         "› Sign in with ChatGPT\n  Provide your own API key\n",
			wantScanned:  true,
			wantRule:     "codex.onboarding.sign_in_with_chatgpt",
			wantComposer: boolp(true),
			wantDialog:   boolp(false),
			wantMatch:    "› Sign in with ChatGPT",
		},
		{
			name:         "codex logged out",
			text:         "stream error: 401 Unauthorized\n",
			wantScanned:  true,
			wantRule:     "codex.loggedout.401_unauthorized",
			wantComposer: boolp(false),
			wantDialog:   boolp(false),
			wantMatch:    "stream error: 401 Unauthorized",
		},
		{
			// The verdict is about a DIALOG, not a login. One occurrence of this
			// is enough to reopen the ADR (revisit trigger 2).
			name:         "folder-trust dialog",
			text:         "Claude Code\n Do you trust the files in this folder?\n 1. Yes, proceed\n❯ ",
			wantScanned:  true,
			wantRule:     "",
			wantComposer: boolp(true),
			wantDialog:   boolp(true),
		},
		{
			name:         "bypass acceptance dialog",
			text:         "Claude Code\n  Bypass Permissions mode\n  1. No, exit\n❯ ",
			wantScanned:  true,
			wantComposer: boolp(true),
			wantDialog:   boolp(true),
		},
		{
			// INTENDED: a whole-screen anchor fires on task prose that merely
			// QUOTES a banner. Recording it is the point — the record describes
			// the screen, it does not gate the verdict (PUPPET-431). BannerMatch
			// shows the quoting line, which is what lets a human see it is prose.
			name:         "task prose quoting a banner still matches, and says which line",
			text:         "I updated the docs to explain that users must run /login when the\nsession expires. Tests pass.\n",
			wantScanned:  true,
			wantRule:     "claude.loggedout.run_login",
			wantComposer: boolp(false),
			wantDialog:   boolp(false),
			wantMatch:    "I updated the docs to explain that users must run /login when the",
		},
		{
			name:         "a screen with no banner at all is still scanned",
			text:         "Claude Code\n⏺ Done.\n❯ ",
			wantScanned:  true,
			wantRule:     "",
			wantComposer: boolp(true),
			wantDialog:   boolp(false),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := describeScreen(tc.text)
			if got == nil {
				t.Fatal("describeScreen must never return nil")
			}
			if got.Scanned != tc.wantScanned {
				t.Errorf("Scanned = %v, want %v", got.Scanned, tc.wantScanned)
			}
			if got.BannerRule != tc.wantRule {
				t.Errorf("BannerRule = %q, want %q", got.BannerRule, tc.wantRule)
			}
			if tc.wantMatch != "" && got.BannerMatch != tc.wantMatch {
				t.Errorf("BannerMatch = %q, want %q", got.BannerMatch, tc.wantMatch)
			}
			if !eqBoolp(got.ComposerWitnessed, tc.wantComposer) {
				t.Errorf("ComposerWitnessed = %v, want %v", strp(got.ComposerWitnessed), strp(tc.wantComposer))
			}
			if !eqBoolp(got.DialogWitnessed, tc.wantDialog) {
				t.Errorf("DialogWitnessed = %v, want %v", strp(got.DialogWitnessed), strp(tc.wantDialog))
			}
		})
	}
}

// "We had no screen" is a FINDING — it is the ADR's fourth revisit trigger —
// so this must never be a nil pointer or an omitted field.
func TestDescribeScreenEmptyIsAFinding(t *testing.T) {
	for _, text := range []string{"", "   \n\t\n  "} {
		got := describeScreen(text)
		if got == nil {
			t.Fatalf("describeScreen(%q) returned nil; must report Scanned=false", text)
		}
		if got.Scanned {
			t.Errorf("describeScreen(%q).Scanned = true, want false", text)
		}
		if got.BannerRule != "" || got.BannerMatch != "" || got.ComposerWitnessed != nil || got.DialogWitnessed != nil {
			t.Errorf("describeScreen(%q) must leave every other field zero, got %+v", text, got)
		}
	}
}

// TestMirrorDriftPin guards the mirrored anchors against a silent
// harness-wrapper bump. See the header comment in evidence.go.
func TestMirrorDriftPin(t *testing.T) {
	const upstream = "harness-wrapper@v0.7.7 pkg/chat/ready.go (claudeOnboardingRE :225, " +
		"codexOnboardingRE :229, claudeLoggedOutRE :235, codexLoggedOutRE :240) and " +
		"pkg/turns/harness/claudecode/claudecode.go :88-93"

	fixtures := map[string]string{
		"claude.onboarding.theme_picker":        "Let's get started.\n  Choose the text style that looks best\n",
		"claude.onboarding.select_login_method": "Select login method\n  1. Claude account with subscription\n",
		"claude.loggedout.run_login":            "Please run /login to continue\n",
		"claude.loggedout.not_logged_in":        "You are not logged in\n",
		"claude.loggedout.invalid_api_key":      "Invalid API key · Fix external API key\n",
		"codex.onboarding.sign_in_with_chatgpt": "› Sign in with ChatGPT\n",
		"codex.onboarding.browser_signin":       "Finish signing in via your browser\n",
		"codex.loggedout.401_unauthorized":      "401 Unauthorized\n",
		"codex.loggedout.missing_bearer":        "Missing bearer or basic authentication in header\n",
		"codex.loggedout.not_logged_in":         "not logged in\n",
		"codex.loggedout.codex_login":           "run codex login to authenticate\n",
	}

	var ids []string
	for _, group := range [][]screenAnchor{onboardingAnchors, loggedOutAnchors} {
		for _, a := range group {
			ids = append(ids, a.id)
			fixture, ok := fixtures[a.id]
			if !ok {
				t.Errorf("anchor %q has no fixture: the mirrored anchor set drifted from %s — "+
					"add the fixture here, or remove the anchor if upstream removed it", a.id, upstream)
				continue
			}
			if !a.re.MatchString(fixture) {
				t.Errorf("anchor %q no longer matches its fixture: it drifted from %s — "+
					"update the pattern and KEEP the id (docs/adr/0002-authfailure-stays-terminal.md "+
					"names it in a revisit trigger)", a.id, upstream)
			}
		}
	}
	if len(ids) != len(fixtures) {
		t.Errorf("anchor id set = %v (%d ids) but %d fixtures are documented; the mirror drifted from %s",
			ids, len(ids), len(fixtures), upstream)
	}

	for _, a := range dialogAnchors {
		if got := describeScreen("Claude Code\n " + a + "\n❯ "); !eqBoolp(got.DialogWitnessed, boolp(true)) {
			t.Errorf("dialog anchor %q no longer detected; it mirrors claudecode.DetectInput in %s", a, upstream)
		}
	}
}

func eqBoolp(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func strp(b *bool) string {
	if b == nil {
		return "nil"
	}
	if *b {
		return "true"
	}
	return "false"
}
