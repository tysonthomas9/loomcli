package doctor

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Strip GIT_* env vars that can redirect git subprocesses to the outer repo
	// even when tests chdir into a temp directory.
	for _, k := range []string{
		"GIT_DIR",
		"GIT_WORK_TREE",
		"GIT_INDEX_FILE",
		"GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_CEILING_DIRECTORIES",
		"GIT_COMMON_DIR",
	} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
