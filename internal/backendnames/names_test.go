package backendnames

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"testing"
)

func TestLocalTaskRunnerBackendsMatchTypeScriptRunner(t *testing.T) {
	source, err := os.ReadFile("../workflows/builtin/local-task-runner.ts")
	if err != nil {
		t.Fatalf("read local task runner source: %v", err)
	}
	blockPattern := regexp.MustCompile(`(?s)const SUPPORTED = \{(.*?)\n\};`)
	match := blockPattern.FindSubmatch(source)
	if len(match) != 2 {
		t.Fatal("local task runner SUPPORTED map not found")
	}
	keyPattern := regexp.MustCompile(`(?m)^\s*([a-z][a-z0-9_-]*):`)
	keyMatches := keyPattern.FindAllSubmatch(match[1], -1)
	got := make([]string, 0, len(keyMatches))
	for _, keyMatch := range keyMatches {
		got = append(got, string(keyMatch[1]))
	}
	want := LocalTaskRunnerBackends()
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TypeScript SUPPORTED backends = %v, Go backends = %v", got, want)
	}
}
