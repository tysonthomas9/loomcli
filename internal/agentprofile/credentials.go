package agentprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CredentialsName is the harness-owned credential file inside a claude profile
// root. Unlike everything the provisioner writes, this file is NOT in the
// manifest's allowlist: the harness owns it and rewrites it at runtime on token
// refresh, so fingerprinting it would fail every spawn after the first refresh.
//
// That exemption is exactly why nothing read it until a hollow one caused an
// outage — see the "Content is checked, not just presence" property in
// docs/design/agent-home-profiles.md.
const CredentialsName = ".credentials.json"

// Credential-content faults, split by CONFIDENCE rather than by repair: only
// the definite one is allowed to refuse a boot.
//
// A hollow file is stable, unambiguous and (when this was written) twelve days
// old, so callers block on it. An unreadable or unrecognized one may be a read
// that lost the race with the harness's own rewrite, or a harness format
// change; refusing every boot in the fleet on either would cost far more than
// the single turn deadline that is the status quo, so those only warn.
var (
	ErrCredentialsHollow       = errors.New("profile credentials file carries no access token")
	ErrCredentialsUnreadable   = errors.New("profile credentials file unreadable")
	ErrCredentialsUnknownShape = errors.New("profile credentials file has an unrecognized shape")
)

// credentialsFile maps a harness root to the credential file whose CONTENT is
// inspected. Only claude has one today; a harness absent from the map verifies
// vacuously (codex). Adding codex's auth.json later is one entry here plus one
// extractor below.
var credentialsFile = map[string]string{"claude": CredentialsName}

// credentialsReadRetryDelay spaces the single retry that absorbs a read racing
// the harness's own rewrite of this file, and credentialsRetrySleep performs
// the wait. Both are package vars so a test can zero the delay and, through the
// sleep, stage the rewrite the retry exists to absorb — a retry nothing can
// observe between the two attempts is indistinguishable from a decorative one.
var (
	credentialsReadRetryDelay = 50 * time.Millisecond
	credentialsRetrySleep     = time.Sleep
)

// CredentialsPath returns the file inspected for harness in dir, or "" when the
// harness has no credential file or dir is empty.
func CredentialsPath(dir, harness string) string {
	name := credentialsFile[harness]
	if dir == "" || name == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// VerifyCredentials reports why the credential file in dir must not be used, or
// nil when there is nothing wrong — INCLUDING when the file is absent, which is
// the supported configuration. A profile with no .credentials.json
// authenticates with the CLAUDE_CODE_OAUTH_TOKEN the supervisor injects from
// <dir>/oauth-token, and there is nothing in the config root to shadow it.
//
// Only the access token is judged. expiresAt is refreshed by the harness and
// refreshToken/scopes are policy this has no business deciding; the check stays
// on the single fact that caused the outage — no access token bytes.
//
// Neither the token nor any prefix of it appears in any returned error: they
// carry the path and the reason only.
func VerifyCredentials(dir, harness string) error {
	path := CredentialsPath(dir, harness)
	if path == "" {
		return nil
	}

	doc, err := loadCredentials(path)
	if err != nil || doc == nil {
		return err
	}

	token, found, ok := extractAccessToken(doc)
	switch {
	case !found:
		return fmt.Errorf("%w: %s: no claudeAiOauth.accessToken and no top-level accessToken", ErrCredentialsUnknownShape, path)
	case !ok:
		return fmt.Errorf("%w: %s: accessToken is not a string", ErrCredentialsHollow, path)
	case strings.TrimSpace(token) == "":
		return fmt.Errorf("%w: %s: accessToken is empty", ErrCredentialsHollow, path)
	}
	return nil
}

// loadCredentials reads and parses path, retrying ONCE on anything other than
// "absent". The harness rewrites this file underneath us on token refresh, so a
// single attempt can catch a truncated write or a rename in flight; a file that
// is still bad on the second look is bad for real. Absence is not retried — it
// is the healthy state, and (nil, nil) is how it is reported.
func loadCredentials(path string) (map[string]json.RawMessage, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			credentialsRetrySleep(credentialsReadRetryDelay)
		}
		raw, err := os.ReadFile(path) //nolint:gosec // G304: path derived from the workspace profile layout, not user input
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			last = fmt.Errorf("%w: %s: %v", ErrCredentialsUnreadable, path, err)
			continue
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			last = fmt.Errorf("%w: %s: not valid JSON", ErrCredentialsUnreadable, path)
			continue
		}
		if doc == nil {
			// Literal `null` parses without error into a nil map. There is no
			// object to look in, which is the same fact a parse failure states.
			last = fmt.Errorf("%w: %s: not valid JSON", ErrCredentialsUnreadable, path)
			continue
		}
		return doc, nil
	}
	return nil, last
}

// extractAccessToken finds the access token in the shape confirmed on disk —
// {"claudeAiOauth":{"accessToken",...}} — falling back to a top-level
// accessToken.
//
// It returns (token, found, isString): "found" distinguishes a shape this
// version does not recognize (warn, forward compatibility) from a recognized
// shape holding nothing usable (block). An accessToken KEY present inside a
// recognized object counts as found even when its value is null or a number:
// that is certainly not a usable token, not a format we failed to understand.
func extractAccessToken(doc map[string]json.RawMessage) (token string, found, isString bool) {
	raw, found := doc["accessToken"]
	if oauth, ok := doc["claudeAiOauth"]; ok {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(oauth, &inner); err != nil {
			// A claudeAiOauth that is not an object is a recognized key holding
			// no token: hollow, not an unknown shape.
			return "", true, false
		}
		raw, found = inner["accessToken"]
		if !found {
			// The object is there and the key is not. The shape IS recognized,
			// so this must block rather than warn.
			return "", true, false
		}
	}
	if !found {
		return "", false, false
	}
	if err := json.Unmarshal(raw, &token); err != nil {
		return "", true, false
	}
	return token, true, true
}
