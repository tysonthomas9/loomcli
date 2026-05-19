package log

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadLastNLinesPublicWrappersAndPagination(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", runtimeDir)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")

	logDir := filepath.Join(runtimeDir, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	path := filepath.Join(logDir, "agent.log")
	content := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	lines, start, err := ReadLastNLines(path, 2)
	if err != nil {
		t.Fatalf("ReadLastNLines: %v", err)
	}
	if start != 4 || !reflect.DeepEqual(lines, []string{"four", "five"}) {
		t.Fatalf("ReadLastNLines = start %d lines %#v", start, lines)
	}

	lines, start, err = ReadFileLastLines(path, 2, 4)
	if err != nil {
		t.Fatalf("ReadFileLastLines beforeLine: %v", err)
	}
	if start != 2 || !reflect.DeepEqual(lines, []string{"two", "three"}) {
		t.Fatalf("ReadFileLastLines beforeLine = start %d lines %#v", start, lines)
	}

	lines, start, err = ReadFileLastLines(path, 2, 1)
	if err != nil {
		t.Fatalf("ReadFileLastLines before first: %v", err)
	}
	if start != 1 || len(lines) != 0 {
		t.Fatalf("before first = start %d lines %#v", start, lines)
	}

	lines, start, err = ReadFileLastLines(path, 2, 99)
	if err != nil {
		t.Fatalf("ReadFileLastLines beyond end: %v", err)
	}
	if start != 99 || len(lines) != 0 {
		t.Fatalf("beyond end = start %d lines %#v", start, lines)
	}
}

func TestLineReaderInternalHelpers(t *testing.T) {
	if got := clampLineCount(0); got != LogReadDefaultLines {
		t.Fatalf("clampLineCount(0) = %d", got)
	}
	if got := clampLineCount(LogReadMaxLines + 1); got != LogReadMaxLines {
		t.Fatalf("clampLineCount(max+1) = %d", got)
	}

	path := filepath.Join(t.TempDir(), "lines.log")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma"), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer file.Close()

	offset, err := findLineByteOffset(file, 2)
	if err != nil {
		t.Fatalf("findLineByteOffset: %v", err)
	}
	if offset != int64(len("alpha\n")) {
		t.Fatalf("line 2 offset = %d", offset)
	}
	start, err := countLinesBeforeOffset(file, offset)
	if err != nil {
		t.Fatalf("countLinesBeforeOffset: %v", err)
	}
	if start != 2 {
		t.Fatalf("start line = %d", start)
	}
}
