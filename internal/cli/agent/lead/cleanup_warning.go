package lead

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// claudeSettingsFile is the settings file claude reads out of its config root.
const claudeSettingsFile = "settings.json"

// claudeCleanupPeriodKey is the setting that decides how long claude keeps a
// transcript before deleting it. Absent, claude's own default applies.
const claudeCleanupPeriodKey = "cleanupPeriodDays"

// claudeDefaultCleanupPeriodDays is claude's behaviour when the key is unset.
// It is claude's number, not loom's, and it is quoted only in the warning.
const claudeDefaultCleanupPeriodDays = 30

// claudeSettingsMaxBytes caps the settings read. The file is a small hand-
// written config; anything larger is not one, and a startup warning must not
// read an arbitrary file into memory to decide it has nothing to say.
const claudeSettingsMaxBytes = 1 << 20

// warnClaudeTranscriptCleanup prints one line to stderr when this lead's
// claude profile has not set cleanupPeriodDays, so an operator learns that
// resumable transcripts expire BEFORE they try to resume a two-month-old one.
//
// It is a warning and nothing more. `loom lead` must never write the setting
// itself: a provisioned profile is fingerprinted by a manifest that
// agentprofile.Verify checks on every launch, so editing a file inside it
// would make the very next `loom lead` refuse to start. Changing the shipped
// default belongs to the provisioning script, which does not live in this repo.
func warnClaudeTranscriptCleanup(out io.Writer) {
	if warning := claudeCleanupWarning(os.Getenv("CLAUDE_CONFIG_DIR")); warning != "" {
		fmt.Fprintln(out, warning)
	}
}

// claudeCleanupWarning returns the line to print, or "" when there is nothing
// to say. Silence is the answer for every uncertainty — no config root, no
// settings file, or a file that does not parse — because a startup warning
// that fires on a profile it could not actually read is noise the operator
// learns to ignore.
func claudeCleanupWarning(configDir string) string {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return ""
	}
	path := filepath.Join(configDir, claudeSettingsFile)
	settings, ok := readClaudeSettings(path)
	if !ok {
		return ""
	}
	if _, set := settings[claudeCleanupPeriodKey]; set {
		return ""
	}
	return fmt.Sprintf(
		"Warning: %s does not set %q, so claude deletes this lead's transcripts after %d days "+
			"and 'loom lead --continue' will not find them. Set it in that file to keep them longer.",
		path, claudeCleanupPeriodKey, claudeDefaultCleanupPeriodDays)
}

// readClaudeSettings decodes the settings object, reporting whether it is
// readable AND a JSON object. A missing file, an unreadable one, and a file
// holding anything but an object all report false.
func readClaudeSettings(path string) (map[string]json.RawMessage, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > claudeSettingsMaxBytes {
		return nil, false
	}
	// #nosec G304 -- path is <CLAUDE_CONFIG_DIR>/settings.json, a config root
	// this process already resolved and verified in enforceLeadProfile.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
		return nil, false
	}
	return settings, true
}
