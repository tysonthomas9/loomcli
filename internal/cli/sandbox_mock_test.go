package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createMockOpenshell writes a small shell script that logs every invocation
// to a file and simulates basic openshell subcommands.  It returns the path
// to the script and the log file it writes to.
//
// Supported behaviour:
//   - Every call appends its arguments (space-separated) as a line in logFile.
//   - "sandbox create ... -- <cmd>": extracts and execs the trailing command.
//   - "sandbox delete ...": exits 0 (or OPENSHELL_MOCK_EXIT if set).
//   - "sandbox list --names": prints $OPENSHELL_MOCK_SANDBOXES (newline-separated).
//   - "status": prints "Status: Connected".
//   - Any other input: exits with $OPENSHELL_MOCK_EXIT (default 0).
func createMockOpenshell(t *testing.T) (scriptPath, logFile string) {
	t.Helper()
	dir := t.TempDir()
	logFile = filepath.Join(dir, "calls.log")
	scriptPath = filepath.Join(dir, "openshell")

	script := `#!/bin/sh
# Log this invocation — one line per call with all args.
printf '%s\0' "$*" >> "` + logFile + `"

exit_code="${OPENSHELL_MOCK_EXIT:-0}"

# Handle subcommands
case "$1 $2" in
    "sandbox create")
        shift  # remove "sandbox"
        shift  # remove "create"
        while [ $# -gt 0 ]; do
            if [ "$1" = "--" ]; then
                shift
                exec "$@"
            fi
            shift
        done
        exit "$exit_code"
        ;;
    "sandbox delete")
        exit "$exit_code"
        ;;
    "sandbox list")
        if [ -n "$OPENSHELL_MOCK_SANDBOXES" ]; then
            printf '%s\n' "$OPENSHELL_MOCK_SANDBOXES"
        fi
        exit 0
        ;;
    *)
        if [ "$1" = "status" ]; then
            echo "Status: Connected"
            exit 0
        fi
        exit "$exit_code"
        ;;
esac
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock openshell script: %v", err)
	}
	return scriptPath, logFile
}

// createMockOpenshellExit is like createMockOpenshell but the script
// always exits with the given code for "sandbox create" and "sandbox delete".
func createMockOpenshellExit(t *testing.T, exitCode int) (scriptPath, logFile string) {
	t.Helper()
	dir := t.TempDir()
	logFile = filepath.Join(dir, "calls.log")
	scriptPath = filepath.Join(dir, "openshell")

	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\0' "$*" >> "%s"
exit %d
`, logFile, exitCode)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock openshell script: %v", err)
	}
	return scriptPath, logFile
}

// readMockLog returns the entries written to the mock log file.
// Each entry is one invocation's arguments, delimited by NUL bytes.
func readMockLog(t *testing.T, logFile string) []string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}
	raw := strings.Split(string(data), "\x00")
	var lines []string
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
